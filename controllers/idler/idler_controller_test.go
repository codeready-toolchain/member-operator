package idler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	memberoperatortest "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"gopkg.in/h2non/gock.v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	openshiftappsv1 "github.com/openshift/api/apps/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	fakescale "k8s.io/client-go/scale/fake"
	clienttest "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	RestartCountWithinThresholdContainer1 = 30
	RestartCountWithinThresholdContainer2 = 24
	RestartCountOverThreshold             = 52
	TestIdlerTimeOutSeconds               = 60 * 60 * 3 // 3 hours
	apiEndpoint                           = "https://api.openshift.com:6443"
)

func TestReconcile(t *testing.T) {

	t.Run("No Idler resource found", func(t *testing.T) {
		// given
		requestName := "not-existing-name"
		reconciler, req, _ := prepareReconcile(t, requestName, getHostCluster)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then - there should not be any error, the controller should only log that the resource was not found
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("Fail to get Idler resource", func(t *testing.T) {
		// given
		reconciler, req, fakeClients := prepareReconcile(t, "cant-get-idler", getHostCluster)
		fakeClients.DefaultClient.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if key.Name == "cant-get-idler" {
				return errors.New("can't get idler")
			}
			return nil
		}

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.EqualError(t, err, "can't get idler")
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("Idler being deleted", func(t *testing.T) {
		// given
		now := metav1.Now()
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "being-deleted",
				Finalizers:        []string{"toolchain.dev.openshift.com"},
				DeletionTimestamp: &now,
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 30},
		}
		reconciler, req, _ := prepareReconcile(t, "being-deleted", getHostCluster, idler)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then - ignore the idler which is being deleted
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	})
}

func TestEnsureIdling(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))

	t.Run("No pods in namespace managed by idler, requeue time equal to the idler timeout", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "john-dev",
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: TestIdlerTimeOutSeconds},
		}

		reconciler, req, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, idler)
		preparePayloads(t, fakeClients, "another-namespace", "", payloadStartTimes{time.Now(), time.Now()}) // noise

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, time.Duration(idler.Spec.TimeoutSeconds)*time.Second, res.RequeueAfter)
		memberoperatortest.AssertThatIdler(t, idler.Name, fakeClients).HasConditions(memberoperatortest.Running())
	})

	t.Run("pods without startTime", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "john-dev",
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: TestIdlerTimeOutSeconds},
		}

		reconciler, req, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, idler)
		preparePayloads(t, fakeClients, idler.Name, "", payloadStartTimes{})

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		// the pods (without startTime) contain also a VM pod, so the next reconcile will be scheduled to the 1/12th of the timeout
		assert.Equal(t, time.Duration(idler.Spec.TimeoutSeconds)*time.Second/12, res.RequeueAfter)
	})

	t.Run("Idle pods", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "alex-stage",
				Labels: map[string]string{
					toolchainv1alpha1.SpaceLabelKey: "alex",
				},
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: TestIdlerTimeOutSeconds},
		}
		namespaces := []string{"dev", "stage"}
		usernames := []string{"alex"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
		mur := newMUR("alex")
		reconciler, req, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)

		podsTooEarlyToKill := preparePayloads(t, fakeClients, idler.Name, "", freshStartTimes(idler.Spec.TimeoutSeconds))
		podsCrashLoopingWithinThreshold := preparePayloadCrashloopingPodsWithinThreshold(
			t, fakeClients, idler.Name, "inThreshRestarts-", freshStartTimes(idler.Spec.TimeoutSeconds))
		podsCrashLooping := preparePayloadCrashloopingAboveThreshold(t, fakeClients, idler.Name, "restartCount-")
		podsRunningForTooLong := preparePayloads(t, fakeClients, idler.Name, "todelete-", expiredStartTimes(idler.Spec.TimeoutSeconds))

		noise := preparePayloads(t, fakeClients, "another-namespace", "", expiredStartTimes(idler.Spec.TimeoutSeconds))

		t.Run("Delete long running and crashlooping pods.", func(t *testing.T) {
			//when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			// Too long running pods are gone. All long running controllers are scaled down.
			// Crashlooping pods are gone.
			// The rest of the pods are still there and controllers are scaled up.
			memberoperatortest.AssertThatInIdleableCluster(t, fakeClients).
				PodsDoNotExist(podsRunningForTooLong.standalonePods).
				PodsExist(podsTooEarlyToKill.standalonePods).
				PodsExist(noise.standalonePods).
				PodsDoNotExist(podsCrashLooping.standalonePods).
				DaemonSetDoesNotExist(podsRunningForTooLong.daemonSet).
				DaemonSetExists(podsTooEarlyToKill.daemonSet).
				DaemonSetExists(noise.daemonSet).
				JobDoesNotExist(podsRunningForTooLong.job).
				JobExists(podsTooEarlyToKill.job).
				JobExists(noise.job).
				DeploymentScaledDown(podsRunningForTooLong.deployment).
				ScaleSubresourceScaledDown(podsRunningForTooLong.integration).
				ScaleSubresourceScaledDown(podsRunningForTooLong.kameletBinding).
				DeploymentScaledDown(podsCrashLooping.deployment).
				DeploymentScaledUp(podsTooEarlyToKill.deployment).
				ScaleSubresourceScaledUp(podsTooEarlyToKill.integration).
				ScaleSubresourceScaledUp(podsTooEarlyToKill.kameletBinding).
				DeploymentScaledUp(noise.deployment).
				ScaleSubresourceScaledUp(noise.integration).
				ScaleSubresourceScaledUp(noise.kameletBinding).
				ReplicaSetScaledDown(podsRunningForTooLong.replicaSet).
				ReplicaSetScaledUp(podsTooEarlyToKill.replicaSet).
				ReplicaSetScaledUp(noise.replicaSet).
				DeploymentConfigScaledDown(podsRunningForTooLong.deploymentConfig).
				DeploymentConfigScaledUp(podsTooEarlyToKill.deploymentConfig).
				DeploymentConfigScaledUp(noise.deploymentConfig).
				ReplicationControllerScaledDown(podsRunningForTooLong.replicationController).
				ReplicationControllerScaledUp(podsTooEarlyToKill.replicationController).
				ReplicationControllerScaledUp(noise.replicationController).
				StatefulSetScaledDown(podsRunningForTooLong.statefulSet).
				StatefulSetScaledUp(podsTooEarlyToKill.statefulSet).
				StatefulSetScaledUp(noise.statefulSet).
				StatefulSetScaledUp(podsCrashLoopingWithinThreshold.statefulSet).
				VMStopped(podsRunningForTooLong.vmStopCallCounter).
				VMRunning(podsTooEarlyToKill.vmStopCallCounter).
				VMRunning(noise.vmStopCallCounter).
				AAPIdled(podsRunningForTooLong.aap).
				AAPRunning(podsTooEarlyToKill.aap).
				AAPRunning(noise.aap).
				InferenceServiceDoesNotExist(podsRunningForTooLong.inferenceService).
				InferenceServiceExists(podsTooEarlyToKill.inferenceService).
				InferenceServiceExists(noise.inferenceService)

			memberoperatortest.AssertThatIdler(t, idler.Name, fakeClients).
				HasConditions(memberoperatortest.Running(), memberoperatortest.IdlerNotificationCreated())

			// something was idled, expect the next reconcile in 5% of the timeout
			assertRequeueTimeInDelta(t, res.RequeueAfter, int32(float32(idler.Spec.TimeoutSeconds)*0.05/12))

			t.Run("No pods. requeue after idler timeout.", func(t *testing.T) {
				//given
				// cleanup remaining pods
				pods := slices.Concat(podsTooEarlyToKill.allPods, podsRunningForTooLong.controlledPods, podsCrashLoopingWithinThreshold.allPods, podsCrashLooping.controlledPods)
				for _, pod := range pods {
					if err := fakeClients.AllNamespacesClient.Delete(context.TODO(), pod); err != nil && !apierrors.IsNotFound(err) {
						require.NoError(t, err)
					}
				}

				//when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)
				memberoperatortest.AssertThatIdler(t, idler.Name, fakeClients).
					HasConditions(memberoperatortest.Running(), memberoperatortest.IdlerNotificationCreated())

				// no pods being tracked -> requeue after idler timeout
				assert.Equal(t, time.Duration(idler.Spec.TimeoutSeconds)*time.Second, res.RequeueAfter)
			})
		})

		t.Run("requeue time with workloads", func(t *testing.T) {
			// given
			startTimes := payloadStartTimes{
				defaultStartTime: time.Now(),
				vmStartTime:      time.Now(),
			}
			t.Run("with VM", func(t *testing.T) {
				// given
				reconciler, req, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
				payloads := preparePayloads(t, fakeClients, idler.Name, "", startTimes)
				preparePayloadCrashloopingPodsWithinThreshold(t, fakeClients, idler.Name, "inThreshRestarts-", startTimes)
				preparePayloadCrashloopingAboveThreshold(t, fakeClients, idler.Name, "restartCount-")

				//when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)
				// with VMs, it needs to be approx one twelfth of the idler timeout plus-minus one second
				assertRequeueTimeInDelta(t, res.RequeueAfter, idler.Spec.TimeoutSeconds/12)

				t.Run("without VM", func(t *testing.T) {
					// given
					// delete all VM pods
					for _, pod := range payloads.allPods {
						if len(pod.OwnerReferences) > 0 && strings.HasPrefix(pod.OwnerReferences[0].Name, payloads.virtualmachineinstance.GetName()) {
							require.NoError(t, reconciler.AllNamespacesClient.Delete(context.TODO(), pod))
						}
					}

					//when
					res, err := reconciler.Reconcile(context.TODO(), req)

					// then
					require.NoError(t, err)
					// without VMs, it needs to be approx the idler timeout plus-minus one second
					assertRequeueTimeInDelta(t, res.RequeueAfter, idler.Spec.TimeoutSeconds)
				})
			})
		})

		t.Run("one error won't affect other pods", func(t *testing.T) {
			// given
			reconciler, req, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
			toKill := preparePayloads(t, fakeClients, idler.Name, "tokill-", expiredStartTimes(idler.Spec.TimeoutSeconds))
			fakeClients.DynamicClient.PrependReactor("patch", "replicasets", func(action clienttest.Action) (bool, runtime.Object, error) {
				return true, nil, fmt.Errorf("some error")
			})

			// when
			_, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.Error(t, err)
			memberoperatortest.AssertThatInIdleableCluster(t, fakeClients).
				// not idled
				ReplicaSetScaledUp(toKill.replicaSet).
				// idled
				PodsDoNotExist(toKill.standalonePods).
				DaemonSetDoesNotExist(toKill.daemonSet).
				JobDoesNotExist(toKill.job).
				DeploymentScaledDown(toKill.deployment).
				ScaleSubresourceScaledDown(toKill.integration).
				ScaleSubresourceScaledDown(toKill.kameletBinding).
				DeploymentConfigScaledDown(toKill.deploymentConfig).
				ReplicationControllerScaledDown(toKill.replicationController).
				StatefulSetScaledDown(toKill.statefulSet).
				VMStopped(toKill.vmStopCallCounter).
				AAPIdled(toKill.aap).
				InferenceServiceDoesNotExist(toKill.inferenceService)

			memberoperatortest.AssertThatIdler(t, idler.Name, fakeClients).
				ContainsCondition(memberoperatortest.FailedToIdle(strings.Split(err.Error(), ": ")[1]))
		})
	})

	t.Run("Create notification the first time resources Idled", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "alex-stage",
				Labels: map[string]string{
					toolchainv1alpha1.SpaceLabelKey: "alex",
				},
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: TestIdlerTimeOutSeconds},
		}
		namespaces := []string{"dev", "stage"}
		usernames := []string{"alex"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
		mur := newMUR("alex")
		reconciler, req, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
		preparePayloads(t, fakeClients, idler.Name, "todelete-", expiredStartTimes(idler.Spec.TimeoutSeconds))

		// when
		// first reconcile should delete pods and create notification
		res, err := reconciler.Reconcile(context.TODO(), req)

		//then
		require.NoError(t, err)
		// something was idled, expect the next reconcile in 5% of the timeout
		assert.Equal(t, time.Duration(int32(float32(idler.Spec.TimeoutSeconds)*0.05/12))*time.Second, res.RequeueAfter)
		memberoperatortest.AssertThatIdler(t, idler.Name, fakeClients).
			HasConditions(memberoperatortest.Running(), memberoperatortest.IdlerNotificationCreated())
		//check the notification is actually created
		hostCl, _ := reconciler.GetHostCluster()
		notification := &toolchainv1alpha1.Notification{}
		err = hostCl.Client.Get(context.TODO(), types.NamespacedName{
			Namespace: test.HostOperatorNs,
			Name:      "alex-stage-idled",
		}, notification)
		require.NoError(t, err)
		notificationCreationTime := notification.CreationTimestamp
		require.Equal(t, "alex@test.com", notification.Spec.Recipient)
		require.Equal(t, "idled", notification.Labels[toolchainv1alpha1.NotificationTypeLabelKey])

		t.Run("second reconcile doesn't create notification", func(t *testing.T) {
			// when
			res, err = reconciler.Reconcile(context.TODO(), req)

			//then
			require.NoError(t, err)
			// pods (exceeding the timeout) are still running, expect the next reconcile in 5% of the timeout
			assert.Equal(t, time.Duration(int32(float32(idler.Spec.TimeoutSeconds)*0.05/12))*time.Second, res.RequeueAfter)
			memberoperatortest.AssertThatIdler(t, idler.Name, fakeClients).
				HasConditions(memberoperatortest.Running(), memberoperatortest.IdlerNotificationCreated())

			err = hostCl.Client.Get(context.TODO(), types.NamespacedName{
				Namespace: test.HostOperatorNs,
				Name:      "alex-stage-idled",
			}, notification)
			require.NoError(t, err)
			require.Equal(t, notificationCreationTime, notification.CreationTimestamp)
		})
	})
}

func TestEnsureIdlingFailed(t *testing.T) {

	t.Run("Ignore when Idler.Spec.TimeoutSec is zero", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "john-dev",
			},
			Spec: toolchainv1alpha1.IdlerSpec{
				TimeoutSeconds: 0,
			},
		}
		reconciler, req, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, idler)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		memberoperatortest.AssertThatIdler(t, idler.Name, fakeClients).HasConditions(memberoperatortest.IdlerNoDeactivation())
	})

	t.Run("Fail if Idler.Spec.TimeoutSec is invalid", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "john-dev",
			},
			Spec: toolchainv1alpha1.IdlerSpec{
				TimeoutSeconds: -1,
			},
		}
		reconciler, req, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, idler)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		memberoperatortest.AssertThatIdler(t, idler.Name, fakeClients).HasConditions(memberoperatortest.FailedToIdle("timeoutSeconds should be bigger than 0"))
	})

	t.Run("Fail if can't list pods", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "john-dev",
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 30},
		}

		reconciler, req, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, idler)
		fakeClients.AllNamespacesClient.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			pl := &corev1.PodList{}
			if reflect.TypeOf(list) == reflect.TypeOf(pl) && len(opts) == 1 {
				return errors.New("can't list pods")
			}
			return nil
		}

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.EqualError(t, err, "failed to ensure idling 'john-dev': can't list pods")
		assert.Equal(t, reconcile.Result{}, res)
		memberoperatortest.AssertThatIdler(t, idler.Name, fakeClients).HasConditions(memberoperatortest.FailedToIdle("can't list pods"))
	})

	t.Run("Fail if can't access payloads", func(t *testing.T) {
		idler := toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "alex-stage",
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: TestIdlerTimeOutSeconds},
		}

		vm := &unstructured.Unstructured{}
		err := vm.UnmarshalJSON(virtualmachineJSON)
		require.NoError(t, err)

		vmi := &unstructured.Unstructured{}
		err = vmi.UnmarshalJSON(virtualmachineinstanceJSON)
		require.NoError(t, err)

		t.Run("can't delete pod", func(t *testing.T) {
			// given
			reconciler, req, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, &idler)
			preparePayloads(t, fakeClients, idler.Name, "", expiredStartTimes(idler.Spec.TimeoutSeconds))

			dlt := fakeClients.AllNamespacesClient.MockDelete
			defer func() { fakeClients.AllNamespacesClient.MockDelete = dlt }()
			fakeClients.AllNamespacesClient.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return errors.New("can't delete pod")
			}

			//when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.ErrorContains(t, err, "failed to ensure idling 'alex-stage': can't delete pod")
			assert.Equal(t, reconcile.Result{}, res)
			memberoperatortest.AssertThatIdler(t, idler.Name, fakeClients).
				ContainsCondition(memberoperatortest.FailedToIdle(strings.Split(err.Error(), ": ")[1]))
		})
	})

	t.Run("Fail if cannot update notification creation doesn't affect idling of other pods", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "john-dev",
				Labels: map[string]string{
					toolchainv1alpha1.SpaceLabelKey: "john",
				},
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 30},
		}
		namespaces := []string{"dev", "stage"}
		usernames := []string{"john"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "john", "advanced", "abcde11", namespaces, usernames)
		reconciler, req, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet) // not adding mur
		toKill := preparePayloads(t, fakeClients, idler.Name, "todelete-", expiredStartTimes(idler.Spec.TimeoutSeconds))
		fakeClients.DefaultClient.MockStatusUpdate = func(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			return fmt.Errorf("cannot set status to fail")
		}
		// when
		_, err := reconciler.Reconcile(context.TODO(), req)

		// then
		// error is not wrapped since it's returned by the final update of the status
		require.EqualError(t, err, "cannot set status to fail")
		memberoperatortest.AssertThatInIdleableCluster(t, fakeClients).
			// idled
			PodsDoNotExist(toKill.standalonePods).
			DaemonSetDoesNotExist(toKill.daemonSet).
			JobDoesNotExist(toKill.job).
			ReplicaSetScaledDown(toKill.replicaSet).
			DeploymentScaledDown(toKill.deployment).
			ScaleSubresourceScaledDown(toKill.integration).
			ScaleSubresourceScaledDown(toKill.kameletBinding).
			DeploymentConfigScaledDown(toKill.deploymentConfig).
			ReplicationControllerScaledDown(toKill.replicationController).
			StatefulSetScaledDown(toKill.statefulSet).
			VMStopped(toKill.vmStopCallCounter).
			AAPIdled(toKill.aap).
			InferenceServiceDoesNotExist(toKill.inferenceService)
	})
}

func TestNotificationAppNameTypeForPods(t *testing.T) {
	//given
	idler := &toolchainv1alpha1.Idler{
		ObjectMeta: metav1.ObjectMeta{
			Name: "feny-stage",
			Labels: map[string]string{
				toolchainv1alpha1.SpaceLabelKey: "feny",
			},
		},
		Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 60},
	}
	namespaces := []string{"dev", "stage"}
	usernames := []string{"feny"}
	nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "feny", "advanced", "abcde11", namespaces, usernames)
	mur := newMUR("feny")
	testpod := map[string]struct {
		preparePayload              func(fakeClients *memberoperatortest.FakeClientSet) (*corev1.Pod, string)
		expectedAppType             string
		expectedNotificationCreated bool
	}{
		"Individual completed pod": {
			expectedAppType:             "Pod",
			expectedNotificationCreated: false,
			preparePayload: func(fakeClients *memberoperatortest.FakeClientSet) (*corev1.Pod, string) {
				pod := newPod(t, fakeClients, idler.Name, expiredStartTimes(idler.Spec.TimeoutSeconds), corev1.PodCondition{Type: "Ready", Reason: "PodCompleted"})
				return pod, pod.Name
			},
		},
		"Individual nonCompleted pod": {
			expectedAppType:             "Pod",
			expectedNotificationCreated: true,
			preparePayload: func(fakeClients *memberoperatortest.FakeClientSet) (*corev1.Pod, string) {
				pod := newPod(t, fakeClients, idler.Name, expiredStartTimes(idler.Spec.TimeoutSeconds), corev1.PodCondition{Type: "Ready"})
				return pod, pod.Name
			},
		},
		"Controlled by deployment": {
			expectedAppType:             "Deployment",
			expectedNotificationCreated: true,
			preparePayload: func(fakeClients *memberoperatortest.FakeClientSet) (*corev1.Pod, string) {
				plds := preparePayloads(t, fakeClients, idler.Name, "", expiredStartTimes(idler.Spec.TimeoutSeconds))
				return plds.getFirstControlledPod(fmt.Sprintf("%s-replicaset", plds.deployment.Name)), plds.deployment.Name
			},
		},
		"Controlled by VM": {
			expectedAppType:             "VirtualMachine",
			expectedNotificationCreated: true,
			preparePayload: func(fakeClients *memberoperatortest.FakeClientSet) (*corev1.Pod, string) {
				plds := preparePayloads(t, fakeClients, idler.Name, "", expiredStartTimes(idler.Spec.TimeoutSeconds))
				return plds.getFirstControlledPod(plds.virtualmachineinstance.GetName()), plds.virtualmachine.GetName()
			},
		},
	}

	for pt, tcs := range testpod {
		t.Run(pt, func(t *testing.T) {
			reconciler, _, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
			ownerIdler := newOwnerIdler(idler, reconciler)
			pod, appName := tcs.preparePayload(fakeClients)

			// when
			err := reconciler.deletePodsAndCreateNotification(context.TODO(), *pod, idler.DeepCopy(), ownerIdler)

			//then
			require.NoError(t, err)
			if tcs.expectedNotificationCreated {
				memberoperatortest.AssertThatIdler(t, idler.Name, fakeClients).
					HasConditions(memberoperatortest.IdlerNotificationCreated())
			} else {
				memberoperatortest.AssertThatIdler(t, idler.Name, fakeClients).
					HasConditions()
			}
			//check the notification is actually created
			hostCl, _ := reconciler.GetHostCluster()
			notification := &toolchainv1alpha1.Notification{}
			err = hostCl.Client.Get(context.TODO(), types.NamespacedName{
				Namespace: test.HostOperatorNs,
				Name:      "feny-stage-idled",
			}, notification)
			if tcs.expectedNotificationCreated {
				require.NoError(t, err)
				require.Equal(t, "feny@test.com", notification.Spec.Recipient)
				require.Equal(t, "idled", notification.Labels[toolchainv1alpha1.NotificationTypeLabelKey])
				require.Equal(t, tcs.expectedAppType, notification.Spec.Context["AppType"])
				require.Equal(t, appName, notification.Spec.Context["AppName"])
			} else {
				require.EqualError(t, err, "notifications.toolchain.dev.openshift.com \"feny-stage-idled\" not found")
			}
		})
	}

}
func TestCreateNotification(t *testing.T) {
	idler := &toolchainv1alpha1.Idler{
		ObjectMeta: metav1.ObjectMeta{
			Name: "alex-stage",
			Labels: map[string]string{
				toolchainv1alpha1.SpaceLabelKey: "alex",
			},
		},
		Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 60},
	}

	t.Run("Creates a notification the first time", func(t *testing.T) {
		// given
		namespaces := []string{"dev", "stage"}
		usernames := []string{"alex"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
		mur := newMUR("alex")
		reconciler, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)

		//when
		err := reconciler.createNotification(context.TODO(), idler, "testPodName", "testapptype")
		//then
		require.NoError(t, err)
		require.True(t, condition.IsTrue(idler.Status.Conditions, toolchainv1alpha1.IdlerTriggeredNotificationCreated))
		//check notification was created
		hostCl, _ := reconciler.GetHostCluster()
		notification := toolchainv1alpha1.Notification{}
		err = hostCl.Client.Get(context.TODO(), types.NamespacedName{Name: "alex-stage-idled", Namespace: hostCl.OperatorNamespace}, &notification)
		require.NoError(t, err)
		createdTime := notification.CreationTimestamp

		t.Run("Notification not created if already sent", func(t *testing.T) {
			//when
			err = reconciler.createNotification(context.TODO(), idler, "testPodName", "testapptype")
			//then
			require.NoError(t, err)
			err = hostCl.Client.Get(context.TODO(), types.NamespacedName{Name: "alex-stage-idled", Namespace: hostCl.OperatorNamespace}, &notification)
			require.NoError(t, err)
			require.Equal(t, createdTime, notification.CreationTimestamp)
		})
	})

	t.Run("Creates notification when notification creation had failed previously", func(t *testing.T) {
		idler.Status.Conditions = []toolchainv1alpha1.Condition{
			{
				Type:    toolchainv1alpha1.IdlerTriggeredNotificationCreated,
				Status:  corev1.ConditionFalse,
				Reason:  toolchainv1alpha1.IdlerTriggeredNotificationCreationFailedReason,
				Message: "notification wasn't created before",
			},
		}
		namespaces := []string{"dev", "stage"}
		usernames := []string{"alex"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
		mur := newMUR("alex")
		reconciler, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)

		//when
		err := reconciler.createNotification(context.TODO(), idler, "testPodName", "testapptype")
		//then
		require.NoError(t, err)
		require.True(t, condition.IsTrue(idler.Status.Conditions, toolchainv1alpha1.IdlerTriggeredNotificationCreated))
	})

	t.Run("Condition is set when setting failed previously, and notification is only sent once", func(t *testing.T) {
		//This is to check the scenario when setting condition fails after creating a notification. Second reconcile expects to attempt the condition again, but not create the notification
		// given
		idler.Status.Conditions = nil
		namespaces := []string{"dev", "stage"}
		usernames := []string{"alex"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
		mur := newMUR("alex")
		reconciler, _, fakeClients := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
		fakeClients.DefaultClient.MockStatusUpdate = func(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			return errors.New("can't update condition")
		}
		//when
		err := reconciler.createNotification(context.TODO(), idler, "testPodName", "testapptype")

		//then
		require.EqualError(t, err, "can't update condition")
		err = fakeClients.DefaultClient.Get(context.TODO(), types.NamespacedName{Name: idler.Name}, idler)
		require.NoError(t, err)
		_, found := condition.FindConditionByType(idler.Status.Conditions, toolchainv1alpha1.IdlerTriggeredNotificationCreated)
		require.False(t, found)

		// second reconcile will not create the notification again but set the status
		fakeClients.DefaultClient.MockStatusUpdate = nil
		err = reconciler.createNotification(context.TODO(), idler, "testPodName", "testapptype")
		require.NoError(t, err)
		require.True(t, condition.IsTrue(idler.Status.Conditions, toolchainv1alpha1.IdlerTriggeredNotificationCreated))
	})

	t.Run("Error in creating notification because MUR not found", func(t *testing.T) {
		idler.Status.Conditions = nil
		namespaces := []string{"dev", "stage"}
		usernames := []string{"alex"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
		reconciler, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet)

		//when
		err := reconciler.createNotification(context.TODO(), idler, "testPodName", "testapptype")
		//then
		require.EqualError(t, err, "could not get the MUR: masteruserrecords.toolchain.dev.openshift.com \"alex\" not found")
	})

	t.Run("Error in creating notification because no user email found in MUR", func(t *testing.T) {
		// given
		idler.Status.Conditions = nil
		namespaces := []string{"dev", "stage"}
		usernames := []string{"alex"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
		mur := newMUR("alex")
		mur.Spec.PropagatedClaims.Email = ""
		reconciler, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
		//when
		err := reconciler.createNotification(context.TODO(), idler, "testPodName", "testapptype")
		require.EqualError(t, err, "no email found for the user in MURs")
	})

	t.Run("Error in creating notification due to invalid email address", func(t *testing.T) {
		idler.Status.Conditions = nil
		namespaces := []string{"dev", "stage"}
		usernames := []string{"alex"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
		mur := newMUR("alex")
		mur.Spec.PropagatedClaims.Email = "invalid-email-address"
		reconciler, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
		//when
		err := reconciler.createNotification(context.TODO(), idler, "testPodName", "testapptype")
		require.EqualError(t, err, "unable to create Notification CR from Idler: The specified recipient [invalid-email-address] is not a valid email address: mail: missing '@' or angle-addr")
	})
}

func TestGetUserEmailFromMUR(t *testing.T) {
	// given
	idler := &toolchainv1alpha1.Idler{
		ObjectMeta: metav1.ObjectMeta{
			Name: "alex-stage",
			Labels: map[string]string{
				toolchainv1alpha1.SpaceLabelKey: "alex",
			},
		},
		Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 60},
	}

	t.Run("Get user email when only space has only one user - DevSandbox", func(t *testing.T) {
		//given
		namespaces := []string{"dev", "stage"}
		usernames := []string{"alex"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
		mur := newMUR("alex")
		reconciler, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
		hostCluster, _ := reconciler.GetHostCluster()
		//when
		emails, err := reconciler.getUserEmailsFromMURs(context.TODO(), hostCluster, idler)
		//then
		require.NoError(t, err)
		require.NotEmpty(t, emails)
		require.Len(t, emails, 1)
		require.Equal(t, "alex@test.com", emails[0])
	})

	t.Run("Get user email when space has more than one user - AppStudio", func(t *testing.T) {
		//given
		namespaces := []string{"dev", "stage"}
		usernames := []string{"alex", "brian", "charlie"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
		mur := newMUR("alex")
		mur2 := newMUR("brian")
		mur3 := newMUR("charlie")
		reconciler, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur, mur2, mur3)
		hostCluster, _ := reconciler.GetHostCluster()
		//when
		emails, err := reconciler.getUserEmailsFromMURs(context.TODO(), hostCluster, idler)
		//then
		require.NoError(t, err)
		require.NotEmpty(t, emails)
		require.Len(t, emails, 3)
		require.Contains(t, emails, "alex@test.com")
		require.Contains(t, emails, "brian@test.com")
		require.Contains(t, emails, "charlie@test.com")
	})

	t.Run("unable to get NSTemplateSet", func(t *testing.T) {
		//given
		reconciler, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler)
		hostCluster, _ := reconciler.GetHostCluster()
		//when
		emails, err := reconciler.getUserEmailsFromMURs(context.TODO(), hostCluster, idler)
		//then
		require.EqualError(t, err, "nstemplatesets.toolchain.dev.openshift.com \"alex\" not found")
		assert.Empty(t, emails)
	})

	t.Run("unable to get MUR, no error but no email found", func(t *testing.T) {
		//given
		namespaces := []string{"dev", "stage"}
		usernames := []string{"alex"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
		reconciler, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet)
		hostCluster, _ := reconciler.GetHostCluster()
		//when
		emails, err := reconciler.getUserEmailsFromMURs(context.TODO(), hostCluster, idler)
		//then
		require.Error(t, err)
		assert.Empty(t, emails)
	})
}

type payloads struct {
	// standalonePods are pods which are supposed to be directly deleted by the Idler controller
	// if run for too long
	standalonePods []*corev1.Pod
	// controlledPods are pods which are managed by Deployment/ReplicaSet/etc controllers and not supposed to be deleted
	// by the Idler controller directly
	controlledPods []*corev1.Pod
	// standalonePods + controlledPods
	allPods []*corev1.Pod

	deployment                *appsv1.Deployment
	integration               *unstructured.Unstructured
	kameletBinding            *unstructured.Unstructured
	replicaSet                *appsv1.ReplicaSet
	replicaSetsWithDeployment []*appsv1.ReplicaSet
	daemonSet                 *appsv1.DaemonSet
	statefulSet               *appsv1.StatefulSet
	deploymentConfig          *openshiftappsv1.DeploymentConfig
	replicationController     *corev1.ReplicationController
	job                       *batchv1.Job
	virtualmachine            *unstructured.Unstructured
	vmStopCallCounter         *int
	virtualmachineinstance    *unstructured.Unstructured
	aap                       *unstructured.Unstructured
	servingRuntime            *unstructured.Unstructured
	inferenceService          *unstructured.Unstructured
}

func (p payloads) getFirstControlledPod(ownerName string) *corev1.Pod {
	for _, pod := range p.controlledPods {
		for _, owner := range pod.OwnerReferences {
			if owner.Name == ownerName {
				return pod
			}
		}
	}
	return nil
}

type payloadStartTimes struct {
	defaultStartTime time.Time
	vmStartTime      time.Time
}

func createDeployment(t *testing.T, clients *memberoperatortest.FakeClientSet, namespace, namePrefix, nameSuffix string, owner client.Object) (*appsv1.Deployment, *appsv1.ReplicaSet) {
	replicas := int32(3)
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s%s%s", namePrefix, namespace, nameSuffix),
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
	}
	if owner != nil {
		require.NoError(t, controllerutil.SetOwnerReference(owner, d, scheme.Scheme))
	}
	createObjectWithDynamicClient(t, clients.DynamicClient, d)
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-replicaset", d.Name), Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err := controllerutil.SetControllerReference(d, rs, scheme.Scheme)
	require.NoError(t, err)
	createObjectWithDynamicClient(t, clients.DynamicClient, rs)
	return d, rs
}

func preparePayloads(t *testing.T, clients *memberoperatortest.FakeClientSet, namespace, namePrefix string, startTimes payloadStartTimes) payloads {
	sTime := &metav1.Time{}
	if !startTimes.defaultStartTime.IsZero() {
		sTime = &metav1.Time{Time: startTimes.defaultStartTime}
	}
	replicas := int32(3)

	// Deployment
	d, rs := createDeployment(t, clients, namespace, namePrefix, "-deployment", nil)
	replicaSetsWithDeployment := []*appsv1.ReplicaSet{rs}
	controlledPods := createPods(t, clients.AllNamespacesClient, rs, sTime, make([]*corev1.Pod, 0, 3), noRestart())

	// create evicted pods owned by the ReplicaSet, they should be deleted if timeout is reached
	standalonePods := createPodsWithSuffix(t, "-evicted", clients.AllNamespacesClient, rs, make([]*corev1.Pod, 0, 3),
		corev1.PodStatus{StartTime: sTime, Reason: "Evicted"})

	camelInt := &unstructured.Unstructured{}
	camelInt.SetAPIVersion("camel.apache.org/v1")
	camelInt.SetKind("Integration")
	camelInt.SetNamespace(namespace)
	camelInt.SetName(fmt.Sprintf("%s%s-integration", namePrefix, namespace))
	createObjectWithDynamicClient(t, clients.DynamicClient, camelInt)

	// Deployment with Camel K integration as an owner reference and a scale sub resource
	_, integrationRS := createDeployment(t, clients, namespace, namePrefix, "-integration-deployment", camelInt)
	replicaSetsWithDeployment = append(replicaSetsWithDeployment, integrationRS)
	controlledPods = createPods(t, clients.AllNamespacesClient, integrationRS, sTime, controlledPods, noRestart())

	camelBinding := &unstructured.Unstructured{}
	camelBinding.SetAPIVersion("camel.apache.org/v1alpha1")
	camelBinding.SetKind("KameletBinding")
	camelBinding.SetNamespace(namespace)
	camelBinding.SetName(fmt.Sprintf("%s%s-binding", namePrefix, namespace))
	createObjectWithDynamicClient(t, clients.DynamicClient, camelBinding)

	// Deployment with Camel K integration as an owner reference and a scale sub resource
	_, bindingRS := createDeployment(t, clients, namespace, namePrefix, "-binding-deployment", camelBinding)
	replicaSetsWithDeployment = append(replicaSetsWithDeployment, bindingRS)
	controlledPods = createPods(t, clients.AllNamespacesClient, bindingRS, sTime, controlledPods, noRestart())

	// Standalone ReplicaSet
	standaloneRs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-replicaset", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	createObjectWithDynamicClient(t, clients.DynamicClient, standaloneRs)
	controlledPods = createPods(t, clients.AllNamespacesClient, standaloneRs, sTime, controlledPods, noRestart())

	// DaemonSet
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-daemonset", namePrefix, namespace), Namespace: namespace},
	}
	createObjectWithDynamicClient(t, clients.DynamicClient, ds)
	controlledPods = createPods(t, clients.AllNamespacesClient, ds, sTime, controlledPods, noRestart())

	// Job
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-job", namePrefix, namespace), Namespace: namespace},
	}
	createObjectWithDynamicClient(t, clients.DynamicClient, job)
	controlledPods = createPods(t, clients.AllNamespacesClient, job, sTime, controlledPods, noRestart())

	// StatefulSet
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-statefulset", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
	}
	createObjectWithDynamicClient(t, clients.DynamicClient, sts)
	controlledPods = createPods(t, clients.AllNamespacesClient, sts, sTime, controlledPods, noRestart())

	// DeploymentConfig
	dc := &openshiftappsv1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-deploymentconfig", namePrefix, namespace), Namespace: namespace},
		Spec:       openshiftappsv1.DeploymentConfigSpec{Replicas: replicas, Paused: true},
	}
	createObjectWithDynamicClient(t, clients.DynamicClient, dc)
	rc := &corev1.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-replicationcontroller", dc.Name), Namespace: namespace},
		Spec:       corev1.ReplicationControllerSpec{Replicas: &replicas},
	}
	err := controllerutil.SetControllerReference(dc, rc, scheme.Scheme)
	require.NoError(t, err)
	createObjectWithDynamicClient(t, clients.DynamicClient, rc)
	controlledPods = createPods(t, clients.AllNamespacesClient, rc, sTime, controlledPods, noRestart())

	// VirtualMachine
	vm := &unstructured.Unstructured{}
	err = vm.UnmarshalJSON(virtualmachineJSON)
	require.NoError(t, err)
	vm.SetName(fmt.Sprintf("%s%s-virtualmachine", namePrefix, namespace))
	vm.SetNamespace(namespace)
	createObjectWithDynamicClient(t, clients.DynamicClient, vm)

	// mock stop call
	stopCallCounter := mockStopVMCalls(namespace, vm.GetName(), http.StatusAccepted)

	// VirtualMachineInstance
	vmstartTime := metav1.NewTime(startTimes.vmStartTime)
	vmi := &unstructured.Unstructured{}
	err = vmi.UnmarshalJSON(virtualmachineinstanceJSON)
	require.NoError(t, err)
	vmi.SetName(fmt.Sprintf("%s%s-virtualmachineinstance", namePrefix, namespace))
	vmi.SetNamespace(namespace)
	err = controllerutil.SetControllerReference(vm, vmi, scheme.Scheme) // vm controls vmi
	require.NoError(t, err)
	createObjectWithDynamicClient(t, clients.DynamicClient, vmi)
	controlledPods = createPods(t, clients.AllNamespacesClient, vmi, &vmstartTime, controlledPods, noRestart()) // vmi controls pod

	// create completed pods owned by the VM, they should be deleted if timeout is reached
	standalonePods = createPodsWithSuffix(t, "-completed", clients.AllNamespacesClient, vmi, standalonePods,
		corev1.PodStatus{StartTime: &vmstartTime, Conditions: []corev1.PodCondition{{Type: "Ready", Reason: "PodCompleted"}}})

	// Standalone ReplicationController
	standaloneRC := &corev1.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-replicationcontroller", namePrefix, namespace), Namespace: namespace},
		Spec:       corev1.ReplicationControllerSpec{Replicas: &replicas},
	}
	createObjectWithDynamicClient(t, clients.DynamicClient, standaloneRC)
	require.NoError(t, err)
	controlledPods = createPods(t, clients.AllNamespacesClient, standaloneRC, sTime, controlledPods, noRestart())

	// AAP
	aapObject := newAAP(t, false, fmt.Sprintf("%s%s-aap", namePrefix, namespace), namespace)
	createObjectWithDynamicClient(t, clients.DynamicClient, aapObject)
	_, aapRs := createDeployment(t, clients, namespace, namePrefix, "-aap-deployment", aapObject)
	replicaSetsWithDeployment = append(replicaSetsWithDeployment, aapRs)
	controlledPods = createPods(t, clients.AllNamespacesClient, aapRs, sTime, controlledPods, noRestart())

	// ServingRuntime and InferenceServices
	servingRuntimeObject := newServingRuntime(fmt.Sprintf("%s%s-servingruntime", namePrefix, namespace), namespace)
	createObjectWithDynamicClient(t, clients.DynamicClient, servingRuntimeObject)
	_, servingRuntimeRs := createDeployment(t, clients, namespace, namePrefix, "-servingruntime-deployment", servingRuntimeObject)
	replicaSetsWithDeployment = append(replicaSetsWithDeployment, servingRuntimeRs)
	controlledPods = createPods(t, clients.AllNamespacesClient, servingRuntimeRs, sTime, controlledPods, noRestart())

	// Create InferenceServices with the same creationtimestamp as the pod startTime is
	inferenceService := newInferenceService(fmt.Sprintf("%s%s-old-inferenceservice", namePrefix, namespace), namespace)
	inferenceService.SetCreationTimestamp(*sTime)
	createObjectWithDynamicClient(t, clients.DynamicClient, inferenceService)

	// Pods with unknown owner. They are subject of direct management by the Idler.
	// It doesn't have to be Idler. We just need any object as the owner of the pods
	// which is not a supported owner such as Deployment or ReplicaSet.
	idler := &toolchainv1alpha1.Idler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s%s-somename", namePrefix, namespace),
			Namespace: namespace,
		},
		Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 30},
	}
	createObjectWithDynamicClient(t, clients.DynamicClient, idler)
	require.NoError(t, err)
	standalonePods = slices.Concat(standalonePods, createPods(t, clients.AllNamespacesClient, idler, sTime, make([]*corev1.Pod, 0, 3), noRestart()))

	// Pods with no owner.
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-pod-%d", namePrefix, namespace, i), Namespace: namespace},
			Status: corev1.PodStatus{
				StartTime:  sTime,
				Conditions: []corev1.PodCondition{{Type: "Ready", Reason: "Running"}},
			},
		}
		// one of them is completed
		if i == 1 {
			pod.Status.Conditions = []corev1.PodCondition{{Type: "Ready", Reason: "PodCompleted"}}
		}
		require.NoError(t, err)
		standalonePods = append(standalonePods, pod)
		err = clients.AllNamespacesClient.Create(context.TODO(), pod)
		require.NoError(t, err)
	}

	return payloads{
		standalonePods:            standalonePods,
		controlledPods:            controlledPods,
		allPods:                   append(standalonePods, controlledPods...),
		deployment:                d,
		integration:               camelInt,
		kameletBinding:            camelBinding,
		replicaSet:                standaloneRs,
		replicaSetsWithDeployment: replicaSetsWithDeployment,
		daemonSet:                 ds,
		statefulSet:               sts,
		deploymentConfig:          dc,
		replicationController:     standaloneRC,
		job:                       job,
		virtualmachine:            vm,
		vmStopCallCounter:         stopCallCounter,
		virtualmachineinstance:    vmi,
		aap:                       aapObject,
		servingRuntime:            servingRuntimeObject,
		inferenceService:          inferenceService,
	}
}

func newAAP(t *testing.T, idled bool, name, namespace string) *unstructured.Unstructured {
	formatted := fmt.Sprintf(aap, name, namespace, idled)
	aap := &unstructured.Unstructured{}
	require.NoError(t, aap.UnmarshalJSON([]byte(formatted)))
	return aap
}

func newServingRuntime(name, namespace string) *unstructured.Unstructured {
	servingRuntime := &unstructured.Unstructured{}
	servingRuntime.SetAPIVersion("serving.kserve.io/v1alpha1")
	servingRuntime.SetKind("ServingRuntime")
	servingRuntime.SetName(name)
	servingRuntime.SetNamespace(namespace)
	return servingRuntime
}

func newInferenceService(name, namespace string) *unstructured.Unstructured {
	inferenceService := &unstructured.Unstructured{}
	inferenceService.SetAPIVersion("serving.kserve.io/v1beta1")
	inferenceService.SetKind("InferenceService")
	inferenceService.SetName(name)
	inferenceService.SetNamespace(namespace)
	return inferenceService
}

func mockStopVMCalls(namespace, name string, reply int) *int {
	expPath := fmt.Sprintf("/apis/subresources.kubevirt.io/v1/namespaces/%s/virtualmachines/%s/stop", namespace, name)
	stopCallCounter := new(int)
	gock.New(apiEndpoint).
		Put(expPath).
		Persist().
		AddMatcher(func(request *http.Request, request2 *gock.Request) (bool, error) {
			// the matcher function is called before checking the path,
			// so we need to verify that it's really the same VM
			if request.URL.Path == expPath {
				*stopCallCounter++
			}
			return true, nil
		}).
		Reply(reply).
		BodyString("")
	return stopCallCounter
}

func newPod(t *testing.T, fakeClients *memberoperatortest.FakeClientSet, namespace string, startTimes payloadStartTimes, conditions ...corev1.PodCondition) *corev1.Pod {
	sTime := &metav1.Time{Time: startTimes.defaultStartTime}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-standalone-pod", namespace), Namespace: namespace},
		Status:     corev1.PodStatus{StartTime: sTime, Conditions: conditions},
	}
	err := fakeClients.AllNamespacesClient.Create(context.TODO(), pod)
	require.NoError(t, err)
	return pod
}

func preparePayloadCrashloopingAboveThreshold(t *testing.T, clientSet *memberoperatortest.FakeClientSet, namespace, namePrefix string) payloads {
	standalonePods := make([]*corev1.Pod, 0, 1)
	startTime := metav1.Now()
	replicas := int32(3)
	// Create a standalone pod with no owner which has at least one container with restart count > 50
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s%s-pod-fail", namePrefix, namespace),
			Namespace: namespace,
		},
		Status: corev1.PodStatus{StartTime: &startTime, ContainerStatuses: []corev1.ContainerStatus{
			{RestartCount: RestartCountOverThreshold},
			{RestartCount: RestartCountWithinThresholdContainer2},
		}},
	}
	err := clientSet.AllNamespacesClient.Create(context.TODO(), pod)
	require.NoError(t, err)
	standalonePods = append(standalonePods, pod)
	// Deployment
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-deployment", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	createObjectWithDynamicClient(t, clientSet.DynamicClient, d)
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-replicaset", d.Name), Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err = controllerutil.SetControllerReference(d, rs, scheme.Scheme)
	require.NoError(t, err)
	createObjectWithDynamicClient(t, clientSet.DynamicClient, rs)
	controlledPods := createPods(t, clientSet.AllNamespacesClient, rs, &startTime, make([]*corev1.Pod, 0, 3), restartingOverThreshold())

	allPods := append(standalonePods, controlledPods...)
	return payloads{
		standalonePods: standalonePods,
		allPods:        allPods,
		controlledPods: controlledPods,
		deployment:     d,
	}
}

func preparePayloadCrashloopingPodsWithinThreshold(t *testing.T, clientSet *memberoperatortest.FakeClientSet, namespace, namePrefix string, times payloadStartTimes) payloads {
	startTime := metav1.NewTime(times.defaultStartTime)
	replicas := int32(3)
	controlledPods := make([]*corev1.Pod, 0, 3)
	// Create a StatefulSet with Crashlooping pods less than threshold
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-statefulset", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
	}
	createObjectWithDynamicClient(t, clientSet.DynamicClient, sts)
	controlledPods = createPods(t, clientSet.AllNamespacesClient, sts, &startTime, controlledPods, restartingUnderThreshold())
	return payloads{
		controlledPods: controlledPods,
		statefulSet:    sts,
		allPods:        controlledPods,
	}
}

func restartingOverThreshold() []corev1.ContainerStatus {
	return []corev1.ContainerStatus{
		{RestartCount: 52},
		{RestartCount: 24},
	}
}

func noRestart() []corev1.ContainerStatus {
	return []corev1.ContainerStatus{
		{RestartCount: 0},
		{RestartCount: 0},
	}
}

func restartingUnderThreshold() []corev1.ContainerStatus {
	return []corev1.ContainerStatus{
		{RestartCount: 48},
		{RestartCount: 24},
	}
}

func createPods(t *testing.T, allNamespacesClient client.Client, owner metav1.Object, startTime *metav1.Time, createdPods []*corev1.Pod, restartStatus []corev1.ContainerStatus) []*corev1.Pod {
	return createPodsWithSuffix(t, "", allNamespacesClient, owner, createdPods, corev1.PodStatus{StartTime: startTime, ContainerStatuses: restartStatus})
}

func createPodsWithSuffix(t *testing.T, suffix string, allNamespacesClient client.Client, owner metav1.Object, createdPods []*corev1.Pod, podStatus corev1.PodStatus) []*corev1.Pod {
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-pod-%d%s", owner.GetName(), i, suffix), Namespace: owner.GetNamespace()},
			Status:     podStatus,
		}
		err := controllerutil.SetControllerReference(owner, pod, scheme.Scheme)
		require.NoError(t, err)
		createdPods = append(createdPods, pod)
		err = allNamespacesClient.Create(context.TODO(), pod)
		require.NoError(t, err)
	}
	return createdPods
}

func prepareReconcile(t *testing.T, name string, getHostClusterFunc func(fakeClient client.Client) cluster.GetHostClusterFunc, initIdlerObjs ...client.Object) (*Reconciler, reconcile.Request, *memberoperatortest.FakeClientSet) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	fakeClient := test.NewFakeClient(t, initIdlerObjs...)
	allNamespacesClient := test.NewFakeClient(t)
	// Register custom list kinds for KServe resources
	dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme.Scheme, customListKinds)

	fakeDiscovery := newFakeDiscoveryClient(allResourcesList(t)...)

	scalesClient := &fakescale.FakeScaleClient{}

	restClient, err := test.NewRESTClient("dummy-token", apiEndpoint)
	require.NoError(t, err)
	restClient.Client.Transport = gock.DefaultTransport
	t.Cleanup(func() {
		gock.OffAll()
	})

	r := &Reconciler{
		Client:              fakeClient,
		AllNamespacesClient: allNamespacesClient,
		DynamicClient:       dynamicClient,
		DiscoveryClient:     fakeDiscovery,
		RestClient:          restClient,
		ScalesClient:        scalesClient,
		Scheme:              s,
		GetHostCluster:      getHostClusterFunc(fakeClient),
		Namespace:           test.MemberOperatorNs,
	}
	return r, reconcile.Request{NamespacedName: test.NamespacedName(test.MemberOperatorNs, name)}, &memberoperatortest.FakeClientSet{
		DefaultClient:       fakeClient,
		AllNamespacesClient: allNamespacesClient,
		DynamicClient:       dynamicClient,
		ScalesClient:        scalesClient,
	}
}

func getHostCluster(fakeClient client.Client) cluster.GetHostClusterFunc {
	return memberoperatortest.NewGetHostCluster(fakeClient, true, corev1.ConditionTrue)
}

func newNSTmplSet(namespaceName, name, tier string, revision string, namespaces []string, usernames []string) *toolchainv1alpha1.NSTemplateSet { // nolint:unparam
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  namespaceName,
			Name:       name,
			Finalizers: []string{toolchainv1alpha1.FinalizerName},
		},
		Spec: toolchainv1alpha1.NSTemplateSetSpec{
			TierName:   tier,
			Namespaces: []toolchainv1alpha1.NSTemplateSetNamespace{},
		},
	}
	nss := make([]toolchainv1alpha1.NSTemplateSetNamespace, len(namespaces))
	for index, nsType := range namespaces {
		nss[index] = toolchainv1alpha1.NSTemplateSetNamespace{
			TemplateRef: fmt.Sprintf("%s-%s-%s", nsTmplSet.Spec.TierName, nsType, revision),
		}
	}
	nsTmplSet.Spec.Namespaces = nss
	nsTmplUsernames := make([]string, 0)
	nsTmplUsernames = append(nsTmplUsernames, usernames...)
	nsTmplSet.Spec.SpaceRoles = []toolchainv1alpha1.NSTemplateSetSpaceRole{
		{
			Usernames: nsTmplUsernames,
		},
	}
	return nsTmplSet
}

func newMUR(name string) *toolchainv1alpha1.MasterUserRecord {
	return &toolchainv1alpha1.MasterUserRecord{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  test.HostOperatorNs,
			Name:       name,
			Finalizers: []string{toolchainv1alpha1.FinalizerName},
		},
		Spec: toolchainv1alpha1.MasterUserRecordSpec{
			PropagatedClaims: toolchainv1alpha1.PropagatedClaims{
				Email: fmt.Sprintf("%s@test.com", name),
			},
		},
	}
}

func assertRequeueTimeInDelta(t *testing.T, requeueAfter time.Duration, baseLineSeconds int32) {
	// let's set the delta to 10s to have some time cushion in case the test takes a bit more time
	assert.Greater(t, requeueAfter, time.Duration(baseLineSeconds-10)*time.Second)
	assert.Less(t, requeueAfter, time.Duration(baseLineSeconds+10)*time.Second)
}

func freshStartTimes(timeoutSeconds int32) payloadStartTimes {
	halfOfIdlerTimeoutAgo := time.Now().Add(-time.Duration(timeoutSeconds/2) * time.Second)
	// needs to be smaller than 1/12 which is the vm idler time, let's set ot to 1/24
	twentyFourthOfIdlerTimeoutMinusOneSecondAgo := time.Now().Add(-time.Duration(timeoutSeconds/24) * time.Second)
	return payloadStartTimes{
		defaultStartTime: halfOfIdlerTimeoutAgo,
		vmStartTime:      twentyFourthOfIdlerTimeoutMinusOneSecondAgo, // vms are killed in 1/12 of the idler time
	}
}

func expiredStartTimes(timeoutSeconds int32) payloadStartTimes {
	idlerTimeoutPlusOneSecondAgo := time.Now().Add(-time.Duration(timeoutSeconds+1) * time.Second)
	twelfthOfIdlerTimeoutPlusOneSecondAgo := time.Now().Add(-time.Duration(timeoutSeconds/12+1) * time.Second)
	return payloadStartTimes{
		defaultStartTime: idlerTimeoutPlusOneSecondAgo,
		vmStartTime:      twelfthOfIdlerTimeoutPlusOneSecondAgo, // vms are killed in 1/12 of the idler time
	}
}

var virtualmachineinstanceJSON = []byte(`{
	"apiVersion": "kubevirt.io/v1",
	"kind": "VirtualMachineInstance",
	"metadata": {
		"name": "rhel9-rajiv",
		"namespace": "another-namespace"
	}
}`)

var virtualmachineJSON = []byte(`{
	"apiVersion": "kubevirt.io/v1",
	"kind": "VirtualMachine",
	"metadata": {
		"name": "rhel9-rajiv",
		"namespace": "another-namespace"
	},
	"spec": {
		"running": true
	}
}`)

func createObjectWithDynamicClient(t *testing.T, dynamicClient dynamic.Interface, object client.Object) {
	// get GVK and GVR
	kinds, _, err := scheme.Scheme.ObjectKinds(object)
	require.NoError(t, err)
	kind := kinds[0]
	resource, _ := meta.UnsafeGuessKindToResource(kind)

	// convert to unstructured.Unstructured
	object.GetObjectKind().SetGroupVersionKind(kind)
	tmp, err := json.Marshal(object)
	require.NoError(t, err)
	unstructuredObj := &unstructured.Unstructured{}
	err = unstructuredObj.UnmarshalJSON(tmp)
	require.NoError(t, err)
	// create
	_, err = dynamicClient.Resource(resource).Namespace(object.GetNamespace()).Create(context.TODO(), unstructuredObj, metav1.CreateOptions{})
	require.NoError(t, err)
}

var (
	aapHeader = `{
  "apiVersion": "aap.ansible.com/v1alpha1",
  "kind": "AnsibleAutomationPlatform",
  "metadata": {
    "labels": {
      "app.kubernetes.io/managed-by": "aap-operator"
    },
    "name": "%s",
    "namespace": "%s"
  }`

	aap = aapHeader + `,
  "spec": {
    "eda": {
      "api": {
        "replicas": 1,
        "resource_requirements": {
          "limits": {
            "cpu": "500m"
          }
        }
      }
    },
    "idle_aap": %t,
    "no_log": false
  },
  "status": {
    "conditions": {
      "message": "",
      "reason": "",
      "status": "True",
      "type": "Successful"
    }
  }
}`
)
