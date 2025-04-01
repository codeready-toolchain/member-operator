package idler

import (
	"context"
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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	openshiftappsv1 "github.com/openshift/api/apps/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
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
		reconciler, req, _, _, _ := prepareReconcile(t, requestName, getHostCluster)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then - there should not be any error, the controller should only log that the resource was not found
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("Fail to get Idler resource", func(t *testing.T) {
		// given
		reconciler, req, cl, _, _ := prepareReconcile(t, "cant-get-idler", getHostCluster)
		cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
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
		reconciler, req, _, _, _ := prepareReconcile(t, "being-deleted", getHostCluster, idler)

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

		reconciler, req, cl, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler)
		preparePayloads(t, reconciler, "another-namespace", "", payloadStartTimes{time.Now(), time.Now()}) // noise

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.True(t, res.Requeue)
		assert.Equal(t, time.Duration(idler.Spec.TimeoutSeconds)*time.Second, res.RequeueAfter)
		memberoperatortest.AssertThatIdler(t, idler.Name, cl).HasConditions(memberoperatortest.Running())
	})

	t.Run("pods without startTime", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "john-dev",
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: TestIdlerTimeOutSeconds},
		}

		reconciler, req, cl, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler)
		preparePayloads(t, reconciler, idler.Name, "", payloadStartTimes{})

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.True(t, res.Requeue)
		// the pods (without startTime) contain also a VM pod, so the next reconcile will be scheduled to the 1/12th of the timeout
		assert.Equal(t, time.Duration(idler.Spec.TimeoutSeconds)*time.Second/12, res.RequeueAfter)
		memberoperatortest.AssertThatIdler(t, idler.Name, cl).HasConditions(memberoperatortest.Running()).TracksPods(nil)
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
		reconciler, req, cl, allCl, dynamicClient := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)

		podsTooEarlyToKill := preparePayloads(t, reconciler, idler.Name, "", freshStartTimes(idler.Spec.TimeoutSeconds))
		podsCrashLoopingWithinThreshold := preparePayloadCrashloopingPodsWithinThreshold(
			t, clientSetForReconciler(reconciler), idler.Name, "inThreshRestarts-", freshStartTimes(idler.Spec.TimeoutSeconds))
		podsCrashLooping := preparePayloadCrashloopingAboveThreshold(
			t, clientSetForReconciler(reconciler), idler.Name, "restartCount-")
		podsRunningForTooLong := preparePayloads(t, reconciler, idler.Name, "todelete-", expiredStartTimes(idler.Spec.TimeoutSeconds))

		noise := preparePayloads(t, reconciler, "another-namespace", "", expiredStartTimes(idler.Spec.TimeoutSeconds))

		t.Run("First reconcile. Start tracking.", func(t *testing.T) {
			//when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			// Idler tracks all pods now but pods have not been deleted yet
			memberoperatortest.AssertThatInIdleableCluster(t, allCl, dynamicClient).
				PodsExist(podsRunningForTooLong.standalonePods).
				PodsExist(podsTooEarlyToKill.standalonePods).
				PodsExist(noise.standalonePods).
				PodsExist(podsCrashLooping.standalonePods).
				DaemonSetExists(podsRunningForTooLong.daemonSet).
				DaemonSetExists(podsTooEarlyToKill.daemonSet).
				DaemonSetExists(noise.daemonSet).
				JobExists(podsRunningForTooLong.job).
				JobExists(podsTooEarlyToKill.job).
				JobExists(noise.job).
				DeploymentScaledUp(podsRunningForTooLong.deployment).
				DeploymentScaledUp(podsRunningForTooLong.integration).
				DeploymentScaledUp(podsRunningForTooLong.kameletBinding).
				DeploymentScaledUp(podsTooEarlyToKill.deployment).
				DeploymentScaledUp(podsTooEarlyToKill.integration).
				DeploymentScaledUp(podsTooEarlyToKill.kameletBinding).
				DeploymentScaledUp(noise.deployment).
				DeploymentScaledUp(noise.integration).
				DeploymentScaledUp(noise.kameletBinding).
				DeploymentScaledUp(podsCrashLooping.deployment).
				ReplicaSetScaledUp(podsRunningForTooLong.replicaSet).
				ReplicaSetScaledUp(podsTooEarlyToKill.replicaSet).
				ReplicaSetScaledUp(noise.replicaSet).
				DeploymentConfigScaledUp(podsRunningForTooLong.deploymentConfig).
				DeploymentConfigScaledUp(podsTooEarlyToKill.deploymentConfig).
				DeploymentConfigScaledUp(noise.deploymentConfig).
				ReplicationControllerScaledUp(podsRunningForTooLong.replicationController).
				ReplicationControllerScaledUp(podsTooEarlyToKill.replicationController).
				ReplicationControllerScaledUp(noise.replicationController).
				StatefulSetScaledUp(podsRunningForTooLong.statefulSet).
				StatefulSetScaledUp(podsTooEarlyToKill.statefulSet).
				StatefulSetScaledUp(noise.statefulSet).
				StatefulSetScaledUp(podsCrashLoopingWithinThreshold.statefulSet).
				VMRunning(podsRunningForTooLong.vmStopCallCounter).
				VMRunning(podsTooEarlyToKill.vmStopCallCounter).
				VMRunning(noise.vmStopCallCounter)

			// Tracked pods
			memberoperatortest.AssertThatIdler(t, idler.Name, cl).
				TracksPods(slices.Concat(podsTooEarlyToKill.allPods, podsRunningForTooLong.allPods, podsCrashLooping.allPods, podsCrashLoopingWithinThreshold.allPods)).
				HasConditions(memberoperatortest.Running())

			assert.True(t, res.Requeue)
			assert.Equal(t, 0, int(res.RequeueAfter)) // pods running for too long should be killed immediately

			t.Run("Second Reconcile. Delete long running and crashlooping pods.", func(t *testing.T) {
				//when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)
				// Too long running pods are gone. All long running controllers are scaled down.
				// Crashlooping pods are gone.
				// The rest of the pods are still there and controllers are scaled up.
				memberoperatortest.AssertThatInIdleableCluster(t, allCl, dynamicClient).
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
					DeploymentScaledDown(podsRunningForTooLong.integration).
					DeploymentScaledDown(podsRunningForTooLong.kameletBinding).
					DeploymentScaledDown(podsCrashLooping.deployment).
					DeploymentScaledUp(podsTooEarlyToKill.deployment).
					DeploymentScaledUp(podsTooEarlyToKill.integration).
					DeploymentScaledUp(podsTooEarlyToKill.kameletBinding).
					DeploymentScaledUp(noise.deployment).
					DeploymentScaledUp(noise.integration).
					DeploymentScaledUp(noise.kameletBinding).
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
					VMRunning(noise.vmStopCallCounter)

				// Only tracks pods that have not been deleted
				memberoperatortest.AssertThatIdler(t, idler.Name, cl).
					TracksPods(append(podsTooEarlyToKill.allPods, podsCrashLoopingWithinThreshold.allPods...)).
					HasConditions(memberoperatortest.Running(), memberoperatortest.IdlerNotificationCreated())

				assert.True(t, res.Requeue)
				assertRequeueTimeInDelta(t, res.RequeueAfter, idler.Spec.TimeoutSeconds/24)

				t.Run("Third Reconcile. Stop tracking deleted pods.", func(t *testing.T) {
					//when
					res, err := reconciler.Reconcile(context.TODO(), req)

					// then
					require.NoError(t, err)
					// Tracking existing pods only.
					memberoperatortest.AssertThatIdler(t, idler.Name, cl).
						TracksPods(slices.Concat(podsTooEarlyToKill.allPods, podsRunningForTooLong.controlledPods, podsCrashLoopingWithinThreshold.allPods, podsCrashLooping.controlledPods)). // controlledPods are being tracked again because in unit tests scaling down doesn't delete pods
						HasConditions(memberoperatortest.Running(), memberoperatortest.IdlerNotificationCreated())

					assert.True(t, res.Requeue)
					assert.Empty(t, res.RequeueAfter) // controlledPods are being tracked again and scheduled to be scaled down because in unit tests scaling down doesn't delete pods

					t.Run("No pods. requeue after idler timeout.", func(t *testing.T) {
						//given
						// cleanup remaining pods
						pods := slices.Concat(podsTooEarlyToKill.allPods, podsRunningForTooLong.controlledPods, podsCrashLoopingWithinThreshold.allPods, podsCrashLooping.controlledPods)
						for _, pod := range pods {
							err := allCl.Delete(context.TODO(), pod)
							require.NoError(t, err)
						}

						//when
						res, err := reconciler.Reconcile(context.TODO(), req)

						// then
						require.NoError(t, err)
						// No pods tracked
						memberoperatortest.AssertThatIdler(t, idler.Name, cl).
							TracksPods([]*corev1.Pod{}).
							HasConditions(memberoperatortest.Running(), memberoperatortest.IdlerNotificationCreated())

						// no pods being tracked -> requeue after idler timeout
						assert.True(t, res.Requeue)
						assert.Equal(t, time.Duration(idler.Spec.TimeoutSeconds)*time.Second, res.RequeueAfter)
					})
				})
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
				reconciler, req, _, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
				payloads := preparePayloads(t, reconciler, idler.Name, "", startTimes)
				preparePayloadCrashloopingPodsWithinThreshold(t, clientSetForReconciler(reconciler), idler.Name, "inThreshRestarts-", startTimes)
				preparePayloadCrashloopingAboveThreshold(t, clientSetForReconciler(reconciler), idler.Name, "restartCount-")
				// start tracking
				_, err := reconciler.Reconcile(context.TODO(), req)
				require.NoError(t, err)

				//when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)
				assert.True(t, res.Requeue)
				// with VMs, it needs to be approx one twelfth of the idler timeout plus-minus one second
				assertRequeueTimeInDelta(t, res.RequeueAfter, idler.Spec.TimeoutSeconds/12)

				t.Run("without VM", func(t *testing.T) {
					// given
					// delete all VM pods
					for _, pod := range payloads.controlledPods {
						if len(pod.OwnerReferences) > 0 && pod.OwnerReferences[0].Name == payloads.virtualmachineinstance.GetName() {
							require.NoError(t, reconciler.AllNamespacesClient.Delete(context.TODO(), pod))
						}
					}

					//when
					res, err := reconciler.Reconcile(context.TODO(), req)

					// then
					require.NoError(t, err)
					assert.True(t, res.Requeue)
					// without VMs, it needs to be approx the idler timeout plus-minus one second
					assertRequeueTimeInDelta(t, res.RequeueAfter, idler.Spec.TimeoutSeconds)
				})
			})
		})

		t.Run("one error won't affect other pods", func(t *testing.T) {
			// given
			reconciler, req, cl, allCl, dynamicClient := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
			toKill := preparePayloads(t, reconciler, idler.Name, "tokill-", expiredStartTimes(idler.Spec.TimeoutSeconds))

			// to start tracking
			_, err := reconciler.Reconcile(context.TODO(), req)
			require.NoError(t, err)
			allCl.MockPatch = func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if reflect.TypeOf(obj) == reflect.TypeOf(&appsv1.ReplicaSet{}) {
					return fmt.Errorf("some error")
				}
				return allCl.Client.Patch(ctx, obj, patch, opts...)
			}

			// when
			_, err = reconciler.Reconcile(context.TODO(), req)

			// then
			require.Error(t, err)
			memberoperatortest.AssertThatInIdleableCluster(t, allCl, dynamicClient).
				// not idled
				ReplicaSetScaledUp(toKill.replicaSet).
				// idled
				PodsDoNotExist(toKill.standalonePods).
				DaemonSetDoesNotExist(toKill.daemonSet).
				JobDoesNotExist(toKill.job).
				DeploymentScaledDown(toKill.deployment).
				DeploymentScaledDown(toKill.integration).
				DeploymentScaledDown(toKill.kameletBinding).
				DeploymentConfigScaledDown(toKill.deploymentConfig).
				ReplicationControllerScaledDown(toKill.replicationController).
				StatefulSetScaledDown(toKill.statefulSet).
				VMStopped(toKill.vmStopCallCounter)

			var beingTracked []*corev1.Pod
			for _, pod := range toKill.controlledPods {
				if pod.OwnerReferences[0].Name == toKill.replicaSet.Name {
					beingTracked = append(beingTracked, pod)
				}
			}

			// Only tracks pods that have not been deleted
			memberoperatortest.AssertThatIdler(t, idler.Name, cl).
				TracksPods(beingTracked).
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
		reconciler, req, cl, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
		preparePayloads(t, reconciler, idler.Name, "todelete-", expiredStartTimes(idler.Spec.TimeoutSeconds))

		// first reconcile to track pods
		res, err := reconciler.Reconcile(context.TODO(), req)
		require.NoError(t, err)
		assert.True(t, res.Requeue)
		assert.Equal(t, 0, int(res.RequeueAfter))

		// second reconcile should delete pods and create notification
		res, err = reconciler.Reconcile(context.TODO(), req)
		//then
		require.NoError(t, err)
		memberoperatortest.AssertThatIdler(t, idler.Name, cl).
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

		// third reconcile should not create a notification
		res, err = reconciler.Reconcile(context.TODO(), req)
		//then
		require.NoError(t, err)
		memberoperatortest.AssertThatIdler(t, idler.Name, cl).
			HasConditions(memberoperatortest.Running(), memberoperatortest.IdlerNotificationCreated())

		err = hostCl.Client.Get(context.TODO(), types.NamespacedName{
			Namespace: test.HostOperatorNs,
			Name:      "alex-stage-idled",
		}, notification)
		require.NoError(t, err)
		require.Equal(t, notificationCreationTime, notification.CreationTimestamp)
	})
}

func TestAAPIdlerIsCalled(t *testing.T) {

	t.Run("pods without startTime", func(t *testing.T) {
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
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", []string{"dev"}, []string{"alex"})
		mur := newMUR("alex")

		fakeDiscovery := newFakeDiscoveryClient(withAAPResourceList(t)...)
		runningAAP := newAAP(t, false, "running-test", idler.Name)
		dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme.Scheme, aapGVK, runningAAP)

		reconciler, req, cl, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
		reconciler.DynamicClient = dynamicClient
		reconciler.DiscoveryClient = fakeDiscovery

		// clientSet needed to create the owning resources in the dynamic client
		clients := clientSet{
			allNamespacesClient: reconciler.AllNamespacesClient,
			dynamicClient:       dynamicClient,
			createOwnerObjects: func(ctx context.Context, object client.Object) error {
				return createObjectWithDynamicClient(t, dynamicClient, object, func(kind schema.GroupVersionKind, object client.Object) {
					if kind.Kind == "Deployment" {
						require.NoError(t, controllerutil.SetOwnerReference(runningAAP, object, scheme.Scheme))
					}
				})
			},
		}

		startTimes := payloadStartTimes{
			// to make it expired for AAP workload, but not for normal one
			defaultStartTime: time.Now().Add(-time.Duration(aapTimeoutSeconds(idler.Spec.TimeoutSeconds)+1) * time.Second),
			// not expired
			vmStartTime: time.Now(),
		}
		podsRunningForTooLong := preparePayloadsWithCreateFunc(t, clients, idler.Name, "long-", startTimes)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.True(t, res.Requeue)

		// the workload resources should all exist, we cannot verify the other resources as they are in the dynamic client
		// and this logic uses allNamespacesClient by default
		memberoperatortest.AssertThatInIdleableCluster(t, reconciler.AllNamespacesClient, dynamicClient).PodsExist(podsRunningForTooLong.standalonePods)

		// the pods (without startTime) contain also a VM pod, so the next reconcile will be scheduled approx to the 1/12th of the timeout
		assertRequeueTimeInDelta(t, res.RequeueAfter, idler.Spec.TimeoutSeconds/12)
		// idler tracks all pods
		memberoperatortest.AssertThatIdler(t, idler.Name, cl).
			TracksPods(podsRunningForTooLong.allPods).
			// aap idler idled the AAP CR and sent the notification
			HasConditions(memberoperatortest.Running(), memberoperatortest.IdlerNotificationCreated())
		// hack to verify that he AAP CR is idled
		aapIdler, _ := prepareAAPIdler(t, idler)
		aapIdler.dynamicClient = dynamicClient
		assertAAPsIdled(t, aapIdler, idler.Name, runningAAP.GetName())
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
		reconciler, req, cl, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		memberoperatortest.AssertThatIdler(t, idler.Name, cl).HasConditions(memberoperatortest.IdlerNoDeactivation())
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
		reconciler, req, cl, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		memberoperatortest.AssertThatIdler(t, idler.Name, cl).HasConditions(memberoperatortest.FailedToIdle("timeoutSeconds should be bigger than 0"))
	})

	t.Run("Fail if can't list pods", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "john-dev",
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 30},
		}

		reconciler, req, cl, allCl, _ := prepareReconcile(t, idler.Name, getHostCluster, idler)
		allCl.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
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
		memberoperatortest.AssertThatIdler(t, idler.Name, cl).HasConditions(memberoperatortest.FailedToIdle("can't list pods"))
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

		t.Run("can't get controllers because of general error", func(t *testing.T) {
			assertCanNotGetObject := func(inaccessible runtime.Object, errMsg string) {
				// given
				reconciler, req, cl, allCl, dynamicCl := prepareReconcileWithPodsRunningTooLong(t, idler)

				get := allCl.MockGet
				defer func() { allCl.MockGet = get }()
				allCl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if reflect.TypeOf(obj) == reflect.TypeOf(inaccessible) {
						return errors.New(errMsg)
					}
					return allCl.Client.Get(ctx, key, obj, opts...)
				}

				originalReactions := make([]clienttest.Reactor, len(dynamicCl.ReactionChain))
				copy(originalReactions, dynamicCl.ReactionChain)
				defer func() {
					dynamicCl.ReactionChain = originalReactions
				}()
				if reflect.TypeOf(inaccessible) == reflect.TypeOf(&unstructured.Unstructured{}) {
					resource := strings.ToLower(inaccessible.(*unstructured.Unstructured).GetKind()) + "s"
					dynamicCl.PrependReactor("get", resource, func(action clienttest.Action) (handled bool, ret runtime.Object, err error) {
						return true, nil, errors.New(errMsg)
					})
				}

				//when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.ErrorContains(t, err, fmt.Sprintf("failed to ensure idling 'alex-stage': %s", errMsg))
				assert.Equal(t, reconcile.Result{}, res)
				memberoperatortest.AssertThatIdler(t, idler.Name, cl).
					ContainsCondition(memberoperatortest.FailedToIdle(strings.Split(err.Error(), ": ")[1]))
			}

			assertCanNotGetObject(&appsv1.Deployment{}, "can't get deployment")
			assertCanNotGetObject(&appsv1.ReplicaSet{}, "can't get replicaset")
			assertCanNotGetObject(&appsv1.DaemonSet{}, "can't get daemonset")
			assertCanNotGetObject(&batchv1.Job{}, "can't get job")
			assertCanNotGetObject(&appsv1.StatefulSet{}, "can't get statefulset")
			assertCanNotGetObject(&openshiftappsv1.DeploymentConfig{}, "can't get deploymentconfig")
			assertCanNotGetObject(&corev1.ReplicationController{}, "can't get replicationcontroller")
			assertCanNotGetObject(vm, "can't get virtualmachine")
			assertCanNotGetObject(vmi, "can't get virtualmachineinstance")
		})

		t.Run("can't get controllers because not found", func(t *testing.T) {
			assertCanNotGetObject := func(inaccessible runtime.Object) {
				// given
				reconciler, req, cl, allCl, dynamicCl := prepareReconcileWithPodsRunningTooLong(t, idler)

				get := allCl.MockGet
				defer func() { allCl.MockGet = get }()
				allCl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if reflect.TypeOf(obj) == reflect.TypeOf(inaccessible) {
						return apierrors.NewNotFound(schema.GroupResource{
							Group:    "",
							Resource: reflect.TypeOf(obj).Name(),
						}, key.Name)
					}
					return allCl.Client.Get(ctx, key, obj, opts...)
				}

				originalReactions := make([]clienttest.Reactor, len(dynamicCl.ReactionChain))
				copy(originalReactions, dynamicCl.ReactionChain)
				defer func() {
					dynamicCl.ReactionChain = originalReactions
				}()
				if reflect.TypeOf(inaccessible) == reflect.TypeOf(&unstructured.Unstructured{}) {
					resource := strings.ToLower(inaccessible.(*unstructured.Unstructured).GetKind()) + "s"
					dynamicCl.PrependReactor("get", resource, func(action clienttest.Action) (handled bool, ret runtime.Object, err error) {
						return true, nil, apierrors.NewNotFound(schema.GroupResource{
							Group:    "",
							Resource: resource,
						}, inaccessible.(*unstructured.Unstructured).GetName())
					})
				}

				//when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err) // 'NotFound' errors are ignored!
				// no other pods being tracked
				assert.True(t, res.Requeue)
				assert.Equal(t, time.Duration(idler.Spec.TimeoutSeconds)*time.Second, res.RequeueAfter)
				memberoperatortest.AssertThatIdler(t, idler.Name, cl).ContainsCondition(memberoperatortest.Running())
			}

			assertCanNotGetObject(&appsv1.Deployment{})
			assertCanNotGetObject(&appsv1.ReplicaSet{})
			assertCanNotGetObject(&appsv1.DaemonSet{})
			assertCanNotGetObject(&batchv1.Job{})
			assertCanNotGetObject(&appsv1.StatefulSet{})
			assertCanNotGetObject(&openshiftappsv1.DeploymentConfig{})
			assertCanNotGetObject(&corev1.ReplicationController{})
			assertCanNotGetObject(vm)
			assertCanNotGetObject(vmi)
		})

		t.Run("can't update controllers", func(t *testing.T) {
			assertCanNotUpdateObject := func(inaccessible runtime.Object, errMsg string) {
				// given
				reconciler, req, cl, allCl, dynamicCl := prepareReconcileWithPodsRunningTooLong(t, idler)
				gock.Off()
				// mock stop call
				mockStopVMCalls(".*", ".*", http.StatusInternalServerError)

				update := allCl.MockUpdate
				defer func() { allCl.MockUpdate = update }()
				allCl.MockPatch = func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if reflect.TypeOf(obj) == reflect.TypeOf(inaccessible) {
						return errors.New(errMsg)
					}
					return allCl.Client.Patch(ctx, obj, patch, opts...)
				}

				// dynamic client for vms
				originalReactions := make([]clienttest.Reactor, len(dynamicCl.ReactionChain))
				copy(originalReactions, dynamicCl.ReactionChain)
				defer func() {
					dynamicCl.ReactionChain = originalReactions
				}()
				if reflect.TypeOf(inaccessible) == reflect.TypeOf(&unstructured.Unstructured{}) {
					resource := strings.ToLower(inaccessible.(*unstructured.Unstructured).GetKind()) + "s"
					dynamicCl.PrependReactor("patch", resource, func(action clienttest.Action) (handled bool, ret runtime.Object, err error) {
						return true, nil, errors.New(errMsg)
					})
				}

				//when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.ErrorContains(t, err, fmt.Sprintf("failed to ensure idling 'alex-stage': %s", errMsg))
				assert.Equal(t, reconcile.Result{}, res)
				memberoperatortest.AssertThatIdler(t, idler.Name, cl).
					ContainsCondition(memberoperatortest.FailedToIdle(strings.Split(err.Error(), ": ")[1]))
			}

			assertCanNotUpdateObject(&appsv1.Deployment{}, "can't update deployment")
			assertCanNotUpdateObject(&appsv1.ReplicaSet{}, "can't update replicaset")
			assertCanNotUpdateObject(&appsv1.StatefulSet{}, "can't update statefulset")
			assertCanNotUpdateObject(&openshiftappsv1.DeploymentConfig{}, "can't update deploymentconfig")
			assertCanNotUpdateObject(&corev1.ReplicationController{}, "can't update replicationcontroller")
			assertCanNotUpdateObject(vm, "an error on the server (\"\") has prevented the request from succeeding (put virtualmachines.authentication.k8s.io alex-stage-virtualmachine)")
		})

		t.Run("can't delete payloads", func(t *testing.T) {
			assertCanNotDeleteObject := func(inaccessible runtime.Object, errMsg string) {
				// given
				reconciler, req, cl, allCl, _ := prepareReconcileWithPodsRunningTooLong(t, idler)

				dlt := allCl.MockDelete
				defer func() { allCl.MockDelete = dlt }()
				allCl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
					if reflect.TypeOf(obj) == reflect.TypeOf(inaccessible) {
						return errors.New(errMsg)
					}
					return allCl.Client.Delete(ctx, obj, opts...)
				}

				//when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.ErrorContains(t, err, fmt.Sprintf("failed to ensure idling 'alex-stage': %s", errMsg))
				assert.Equal(t, reconcile.Result{}, res)
				memberoperatortest.AssertThatIdler(t, idler.Name, cl).
					ContainsCondition(memberoperatortest.FailedToIdle(strings.Split(err.Error(), ": ")[1]))
			}

			assertCanNotDeleteObject(&appsv1.DaemonSet{}, "can't delete daemonset")
			assertCanNotDeleteObject(&batchv1.Job{}, "can't delete job")
			assertCanNotDeleteObject(&corev1.Pod{}, "can't delete pod")
		})
	})

	t.Run("Fail if cannot update notification creation failed status", func(t *testing.T) {
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
		reconciler, req, cl, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet) // not adding mur
		preparePayloads(t, reconciler, idler.Name, "todelete-", expiredStartTimes(idler.Spec.TimeoutSeconds))

		// when
		// first reconcile to start tracking pods
		_, err := reconciler.Reconcile(context.TODO(), req)
		require.NoError(t, err)

		// second reconcile to delete pods and create notification
		cl.MockStatusUpdate = func(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			return fmt.Errorf("cannot set status to fail")
		}
		_, err = reconciler.Reconcile(context.TODO(), req)

		// then
		// since no mur, error should have been wrapped as status update, but status update fails
		require.EqualError(t, err, "failed to ensure idling 'john-dev': cannot set status to fail")
	})

	t.Run("aap idler failures", func(t *testing.T) {
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "john-dev",
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: TestIdlerTimeOutSeconds * 100},
		}

		t.Run("aap idler init failed", func(t *testing.T) {
			// given
			reconciler, req, cl, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler)
			fakeDiscovery := newFakeDiscoveryClient(noAAPResourceList(t)...)
			fakeDiscovery.ServerPreferredResourcesError = fmt.Errorf("some error")
			reconciler.DiscoveryClient = fakeDiscovery

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.True(t, res.Requeue)
			assert.Equal(t, time.Duration(idler.Spec.TimeoutSeconds)*time.Second, res.RequeueAfter)
			memberoperatortest.AssertThatIdler(t, idler.Name, cl).
				HasConditions(memberoperatortest.FailedToIdle("failed to init aap idler 'john-dev': some error"))
		})

		t.Run("aap idler execution failed", func(t *testing.T) {
			// given
			reconciler, req, cl, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler)
			dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme.Scheme, aapGVK)
			dynamicClient.PrependReactor("list", "ansibleautomationplatforms", func(action clienttest.Action) (handled bool, ret runtime.Object, err error) {
				return true, nil, fmt.Errorf("some list error")
			})
			fakeDiscovery := newFakeDiscoveryClient(withAAPResourceList(t)...)
			reconciler.DynamicClient = dynamicClient
			reconciler.DiscoveryClient = fakeDiscovery

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.True(t, res.Requeue)
			assert.Equal(t, time.Duration(idler.Spec.TimeoutSeconds)*time.Second, res.RequeueAfter)
			memberoperatortest.AssertThatIdler(t, idler.Name, cl).
				HasConditions(memberoperatortest.FailedToIdle("failed to ensure aap idling 'john-dev': some list error"))
		})
	})
}

func TestAppNameTypeForControllers(t *testing.T) {

	idler := &toolchainv1alpha1.Idler{
		ObjectMeta: metav1.ObjectMeta{
			Name: "alex-stage",
			Labels: map[string]string{
				toolchainv1alpha1.SpaceLabelKey: "alex",
			},
		},
		Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 60},
	}
	namespaces := []string{"dev", "stage"}
	usernames := []string{"alex"}
	nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
	mur := newMUR("alex")
	reconciler, _, _, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
	plds := preparePayloads(t, reconciler, idler.Name, "", freshStartTimes(idler.Spec.TimeoutSeconds))

	tests := map[string]struct {
		ownerKind       string
		ownerName       string
		expectedAppType string
		expectedAppName string
	}{
		"Deployment": {
			// We are testing the case with a nested controllers (Deployment -> ReplicaSet -> Pod) here,
			// so we the pod's owner is ReplicaSet but the expected scaled app is the parent Deployment.
			ownerKind:       "ReplicaSet",
			ownerName:       fmt.Sprintf("%s-replicaset", plds.deployment.Name),
			expectedAppType: "Deployment",
			expectedAppName: plds.deployment.Name,
		},
		"ReplicaSet": {
			ownerKind:       "ReplicaSet",
			ownerName:       plds.replicaSet.Name,
			expectedAppType: "ReplicaSet",
			expectedAppName: plds.replicaSet.Name,
		},
		"DaemonSet": {
			ownerKind:       "DaemonSet",
			ownerName:       plds.daemonSet.Name,
			expectedAppType: "DaemonSet",
			expectedAppName: plds.daemonSet.Name,
		},
		"StatefulSet": {
			ownerKind:       "StatefulSet",
			ownerName:       plds.statefulSet.Name,
			expectedAppType: "StatefulSet",
			expectedAppName: plds.statefulSet.Name,
		},
		"DeploymentConfig": {
			// We are testing the case with a nested controllers (DeploymentConfig -> ReplicationController -> Pod) here,
			// so we the pod's owner is ReplicaSet but the expected scaled app is the parent Deployment.
			ownerKind:       "ReplicationController",
			ownerName:       fmt.Sprintf("%s-replicationcontroller", plds.deploymentConfig.Name),
			expectedAppType: "DeploymentConfig",
			expectedAppName: plds.deploymentConfig.Name,
		},
		"ReplicationController": {
			ownerKind:       "ReplicationController",
			ownerName:       plds.replicationController.Name,
			expectedAppType: "ReplicationController",
			expectedAppName: plds.replicationController.Name,
		},
		"Job": {
			ownerKind:       "Job",
			ownerName:       plds.job.Name,
			expectedAppType: "Job",
			expectedAppName: plds.job.Name,
		},
		"VirtualMachineInstance": {
			ownerKind:       "VirtualMachineInstance",
			ownerName:       plds.virtualmachineinstance.GetName(),
			expectedAppType: "VirtualMachine",
			expectedAppName: plds.virtualmachine.GetName(),
		},
	}

	for k, tc := range tests {
		t.Run(k, func(t *testing.T) {
			//given
			p := func() *corev1.Pod {
				for _, pod := range plds.controlledPods {
					for _, owner := range pod.OwnerReferences {
						if owner.Kind == tc.ownerKind && owner.Name == tc.ownerName {
							return pod
						}
					}
				}
				return nil
			}()

			//when
			appType, appName, deletedByController, err := reconciler.scaleControllerToZero(context.TODO(), p.ObjectMeta)

			//then
			require.NoError(t, err)
			require.True(t, deletedByController)
			require.Equal(t, tc.expectedAppType, appType)
			require.Equal(t, tc.expectedAppName, appName)
		})
	}
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
	var pname string
	testpod := map[string]struct {
		pcond                       []corev1.PodCondition
		expectedAppType             string
		expectedNotificationCreated bool
		controllerOwned             bool
	}{
		"Individual-Completed-Pod": {
			pcond:                       []corev1.PodCondition{{Type: "Ready", Reason: "PodCompleted"}},
			expectedAppType:             "Pod",
			expectedNotificationCreated: false,
			controllerOwned:             false,
		},
		"Individual-NonCompleted-Pod": {
			pcond:                       []corev1.PodCondition{{Type: "Ready", Reason: ""}},
			expectedAppType:             "Pod",
			expectedNotificationCreated: true,
			controllerOwned:             false,
		},
		"Controlled-Completed-Pod": {
			pcond:                       []corev1.PodCondition{{Type: "Ready", Reason: "PodCompleted"}},
			expectedAppType:             "Deployment",
			expectedNotificationCreated: true,
			controllerOwned:             true,
		},
		"Controlled-NonCompleted-Pod": {
			pcond:                       []corev1.PodCondition{{Type: "Ready", Reason: ""}},
			expectedAppType:             "Deployment",
			expectedNotificationCreated: true,
			controllerOwned:             true,
		},
		"Controlled-Pod-nocondition": {
			expectedAppType:             "Deployment",
			expectedNotificationCreated: true,
			controllerOwned:             true,
		},
		"Controlled-Pod-multiplecondition": {
			pcond: []corev1.PodCondition{{Type: "Ready", Reason: ""},
				{Type: "Initiated"},
				{Type: "ContainersReady"}},
			expectedAppType:             "Deployment",
			expectedNotificationCreated: true,
			controllerOwned:             true,
		},
	}

	for pt, tcs := range testpod {
		t.Run(pt, func(t *testing.T) {
			reconciler, req, cl, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
			idlerTimeoutPlusOneSecondAgo := time.Now().Add(-time.Duration(idler.Spec.TimeoutSeconds+1) * time.Second)

			if tcs.controllerOwned {
				preparePayloads(t, reconciler, idler.Name, "todelete-", expiredStartTimes(idler.Spec.TimeoutSeconds), tcs.pcond...)
			} else {
				p := preparePayloadsSinglePod(t, reconciler, idler.Name, "todelete-", idlerTimeoutPlusOneSecondAgo, tcs.pcond...).standalonePods[0]
				pname = p.Name
			}

			// first reconcile to track pods
			res, err := reconciler.Reconcile(context.TODO(), req)
			require.NoError(t, err)
			assert.True(t, res.Requeue)

			// second reconcile should delete pods and create notification
			res, err = reconciler.Reconcile(context.TODO(), req)
			//then
			require.NoError(t, err)

			if tcs.expectedNotificationCreated {
				memberoperatortest.AssertThatIdler(t, idler.Name, cl).
					HasConditions(memberoperatortest.Running(), memberoperatortest.IdlerNotificationCreated())
			} else {
				memberoperatortest.AssertThatIdler(t, idler.Name, cl).
					HasConditions(memberoperatortest.Running())
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
				if !tcs.controllerOwned {
					require.Equal(t, pname, notification.Spec.Context["AppName"])
				}
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
		reconciler, _, _, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)

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
		reconciler, _, _, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)

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
		reconciler, _, cl, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
		cl.MockStatusUpdate = func(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			return errors.New("can't update condition")
		}
		//when
		err := reconciler.createNotification(context.TODO(), idler, "testPodName", "testapptype")

		//then
		require.EqualError(t, err, "can't update condition")
		err = cl.Get(context.TODO(), types.NamespacedName{Name: idler.Name}, idler)
		require.NoError(t, err)
		_, found := condition.FindConditionByType(idler.Status.Conditions, toolchainv1alpha1.IdlerTriggeredNotificationCreated)
		require.False(t, found)

		// second reconcile will not create the notification again but set the status
		cl.MockStatusUpdate = nil
		err = reconciler.createNotification(context.TODO(), idler, "testPodName", "testapptype")
		require.NoError(t, err)
		require.True(t, condition.IsTrue(idler.Status.Conditions, toolchainv1alpha1.IdlerTriggeredNotificationCreated))
	})

	t.Run("Error in creating notification because MUR not found", func(t *testing.T) {
		idler.Status.Conditions = nil
		namespaces := []string{"dev", "stage"}
		usernames := []string{"alex"}
		nsTmplSet := newNSTmplSet(test.MemberOperatorNs, "alex", "advanced", "abcde11", namespaces, usernames)
		reconciler, _, _, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet)

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
		reconciler, _, _, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
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
		reconciler, _, _, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
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
		reconciler, _, _, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur)
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
		reconciler, _, _, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet, mur, mur2, mur3)
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
		reconciler, _, _, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler)
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
		reconciler, _, _, _, _ := prepareReconcile(t, idler.Name, getHostCluster, idler, nsTmplSet)
		hostCluster, _ := reconciler.GetHostCluster()
		//when
		emails, err := reconciler.getUserEmailsFromMURs(context.TODO(), hostCluster, idler)
		//then
		require.Error(t, err)
		assert.Empty(t, emails)
	})
}

type payloads struct {
	// standalonePods are pods which are supposed to be tracked and also deleted directly by the Idler controller
	// if run for too long
	standalonePods []*corev1.Pod
	// controlledPods are pods which are managed by Deployment/ReplicaSet/etc controllers and not supposed to be deleted
	// by the Idler controller directly
	controlledPods []*corev1.Pod
	// standalonePods + controlledPods
	allPods []*corev1.Pod

	deployment             *appsv1.Deployment
	integration            *appsv1.Deployment
	kameletBinding         *appsv1.Deployment
	replicaSet             *appsv1.ReplicaSet
	daemonSet              *appsv1.DaemonSet
	statefulSet            *appsv1.StatefulSet
	deploymentConfig       *openshiftappsv1.DeploymentConfig
	replicationController  *corev1.ReplicationController
	job                    *batchv1.Job
	virtualmachine         *unstructured.Unstructured
	vmStopCallCounter      *int
	virtualmachineinstance *unstructured.Unstructured
}

type payloadStartTimes struct {
	defaultStartTime time.Time
	vmStartTime      time.Time
}

func preparePayloads(t *testing.T, r *Reconciler, namespace, namePrefix string, startTimes payloadStartTimes, conditions ...corev1.PodCondition) payloads {
	return preparePayloadsWithCreateFunc(t, clientSetForReconciler(r), namespace, namePrefix, startTimes, conditions...)
}

type createFunc func(context.Context, client.Object) error
type clientSet struct {
	createOwnerObjects  createFunc
	allNamespacesClient client.Client
	dynamicClient       dynamic.Interface
}

func clientSetForReconciler(r *Reconciler) clientSet {
	return clientSet{
		allNamespacesClient: r.AllNamespacesClient,
		dynamicClient:       r.DynamicClient,
		createOwnerObjects: func(ctx context.Context, object client.Object) error {
			return r.AllNamespacesClient.Create(ctx, object)
		},
	}
}

func preparePayloadsWithCreateFunc(t *testing.T, clients clientSet, namespace, namePrefix string, startTimes payloadStartTimes, conditions ...corev1.PodCondition) payloads {
	var sTime *metav1.Time
	if !startTimes.defaultStartTime.IsZero() {
		sTime = &metav1.Time{Time: startTimes.defaultStartTime}
	}
	replicas := int32(3)

	// Deployment
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-deployment", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	err := clients.createOwnerObjects(context.TODO(), d)
	require.NoError(t, err)
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-replicaset", d.Name), Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err = controllerutil.SetControllerReference(d, rs, scheme.Scheme)
	require.NoError(t, err)
	err = clients.createOwnerObjects(context.TODO(), rs)
	require.NoError(t, err)
	controlledPods := createPods(t, clients.allNamespacesClient, rs, sTime, make([]*corev1.Pod, 0, 3), noRestart(), conditions...)

	// Deployment with Camel K integration as an owner reference and a scale sub resource
	integration := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s%s-integration-deployment", namePrefix, namespace),
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "camel.apache.org/v1",
					Kind:       "Integration",
					Name:       fmt.Sprintf("%s%s-integration", namePrefix, namespace),
				},
			},
		},
		Spec: appsv1.DeploymentSpec{Replicas: &replicas},
	}
	err = clients.createOwnerObjects(context.TODO(), integration)
	require.NoError(t, err)
	integrationRS := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-replicaset", integration.Name), Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err = controllerutil.SetControllerReference(integration, integrationRS, scheme.Scheme)
	require.NoError(t, err)
	err = clients.createOwnerObjects(context.TODO(), integrationRS)
	require.NoError(t, err)
	controlledPods = createPods(t, clients.allNamespacesClient, integrationRS, sTime, controlledPods, noRestart())

	// Deployment with Camel K integration as an owner reference and a scale sub resource
	binding := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s%s-binding-deployment", namePrefix, namespace),
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "camel.apache.org/v1alpha1",
					Kind:       "KameletBinding",
					Name:       fmt.Sprintf("%s%s-binding", namePrefix, namespace),
				},
			},
		},
		Spec: appsv1.DeploymentSpec{Replicas: &replicas},
	}
	err = clients.createOwnerObjects(context.TODO(), binding)
	require.NoError(t, err)
	bindingRS := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-replicaset", binding.Name), Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err = controllerutil.SetControllerReference(binding, bindingRS, scheme.Scheme)
	require.NoError(t, err)
	err = clients.createOwnerObjects(context.TODO(), bindingRS)
	require.NoError(t, err)
	controlledPods = createPods(t, clients.allNamespacesClient, bindingRS, sTime, controlledPods, noRestart())

	// Standalone ReplicaSet
	standaloneRs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-replicaset", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err = clients.createOwnerObjects(context.TODO(), standaloneRs)
	require.NoError(t, err)
	controlledPods = createPods(t, clients.allNamespacesClient, standaloneRs, sTime, controlledPods, noRestart())

	// DaemonSet
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-daemonset", namePrefix, namespace), Namespace: namespace},
	}
	err = clients.createOwnerObjects(context.TODO(), ds)
	require.NoError(t, err)
	controlledPods = createPods(t, clients.allNamespacesClient, ds, sTime, controlledPods, noRestart())

	// Job
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-job", namePrefix, namespace), Namespace: namespace},
	}
	err = clients.createOwnerObjects(context.TODO(), job)
	require.NoError(t, err)
	controlledPods = createPods(t, clients.allNamespacesClient, job, sTime, controlledPods, noRestart())

	// StatefulSet
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-statefulset", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
	}
	err = clients.createOwnerObjects(context.TODO(), sts)
	require.NoError(t, err)
	controlledPods = createPods(t, clients.allNamespacesClient, sts, sTime, controlledPods, noRestart())

	// DeploymentConfig
	dc := &openshiftappsv1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-deploymentconfig", namePrefix, namespace), Namespace: namespace},
		Spec:       openshiftappsv1.DeploymentConfigSpec{Replicas: replicas, Paused: true},
	}
	err = clients.createOwnerObjects(context.TODO(), dc)
	require.NoError(t, err)
	rc := &corev1.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-replicationcontroller", dc.Name), Namespace: namespace},
		Spec:       corev1.ReplicationControllerSpec{Replicas: &replicas},
	}
	err = controllerutil.SetControllerReference(dc, rc, scheme.Scheme)
	require.NoError(t, err)
	err = clients.createOwnerObjects(context.TODO(), rc)
	require.NoError(t, err)
	controlledPods = createPods(t, clients.allNamespacesClient, rc, sTime, controlledPods, noRestart())

	// VirtualMachine
	vm := &unstructured.Unstructured{}
	err = vm.UnmarshalJSON(virtualmachineJSON)
	require.NoError(t, err)
	vm.SetName(fmt.Sprintf("%s%s-virtualmachine", namePrefix, namespace))
	vm.SetNamespace(namespace)
	_, err = clients.dynamicClient.Resource(vmGVR).Namespace(namespace).Create(context.TODO(), vm, metav1.CreateOptions{})
	require.NoError(t, err)

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
	_, err = clients.dynamicClient.Resource(vmInstanceGVR).Namespace(namespace).Create(context.TODO(), vmi, metav1.CreateOptions{})
	require.NoError(t, err)
	controlledPods = createPods(t, clients.allNamespacesClient, vmi, &vmstartTime, controlledPods, noRestart()) // vmi controls pod

	// Standalone ReplicationController
	standaloneRC := &corev1.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-replicationcontroller", namePrefix, namespace), Namespace: namespace},
		Spec:       corev1.ReplicationControllerSpec{Replicas: &replicas},
	}
	err = clients.createOwnerObjects(context.TODO(), standaloneRC)
	require.NoError(t, err)
	controlledPods = createPods(t, clients.allNamespacesClient, standaloneRC, sTime, controlledPods, noRestart())

	// Pods with unknown owner. They are subject of direct management by the Idler.
	// It doesn't have to be Idler. We just need any object as the owner of the pods
	// which is not a tracked controller such as Deployment or ReplicaSet.
	idler := &toolchainv1alpha1.Idler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s%s-somename", namePrefix, namespace),
			Namespace: namespace,
		},
		Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 30},
	}
	standalonePods := createPods(t, clients.allNamespacesClient, idler, sTime, make([]*corev1.Pod, 0, 3), noRestart())

	// Pods with no owner.
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-pod-%d", namePrefix, namespace, i), Namespace: namespace},
			Status:     corev1.PodStatus{StartTime: sTime},
		}
		require.NoError(t, err)
		standalonePods = append(standalonePods, pod)
		err = clients.allNamespacesClient.Create(context.TODO(), pod)
		require.NoError(t, err)
	}

	return payloads{
		standalonePods:         standalonePods,
		controlledPods:         controlledPods,
		allPods:                append(standalonePods, controlledPods...),
		deployment:             d,
		integration:            integration,
		kameletBinding:         binding,
		replicaSet:             standaloneRs,
		daemonSet:              ds,
		statefulSet:            sts,
		deploymentConfig:       dc,
		replicationController:  standaloneRC,
		job:                    job,
		virtualmachine:         vm,
		vmStopCallCounter:      stopCallCounter,
		virtualmachineinstance: vmi,
	}
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

func preparePayloadsSinglePod(t *testing.T, r *Reconciler, namespace, namePrefix string, startTime time.Time, conditions ...corev1.PodCondition) payloads {
	sTime := metav1.NewTime(startTime)

	// Pods with no owner.
	standalonePods := make([]*corev1.Pod, 0, 1)
	for i := 0; i < 1; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-pod-%d", namePrefix, namespace, i), Namespace: namespace},
			Status:     corev1.PodStatus{StartTime: &sTime, Conditions: conditions},
		}
		standalonePods = append(standalonePods, pod)
		err := r.AllNamespacesClient.Create(context.TODO(), pod)
		require.NoError(t, err)
	}
	return payloads{
		standalonePods: standalonePods,
	}
}

func preparePayloadCrashloopingAboveThreshold(t *testing.T, clientSet clientSet, namespace, namePrefix string) payloads {
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
	err := clientSet.allNamespacesClient.Create(context.TODO(), pod)
	require.NoError(t, err)
	standalonePods = append(standalonePods, pod)
	// Deployment
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-deployment", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	err = clientSet.createOwnerObjects(context.TODO(), d)
	require.NoError(t, err)
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-replicaset", d.Name), Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err = controllerutil.SetControllerReference(d, rs, scheme.Scheme)
	require.NoError(t, err)
	err = clientSet.createOwnerObjects(context.TODO(), rs)
	require.NoError(t, err)
	controlledPods := createPods(t, clientSet.allNamespacesClient, rs, &startTime, make([]*corev1.Pod, 0, 3), restartingOverThreshold())

	allPods := append(standalonePods, controlledPods...)
	return payloads{
		standalonePods: standalonePods,
		allPods:        allPods,
		controlledPods: controlledPods,
		deployment:     d,
	}
}

func preparePayloadCrashloopingPodsWithinThreshold(t *testing.T, clientSet clientSet, namespace, namePrefix string, times payloadStartTimes) payloads {
	startTime := metav1.NewTime(times.defaultStartTime)
	replicas := int32(3)
	controlledPods := make([]*corev1.Pod, 0, 3)
	// Create a StatefulSet with Crashlooping pods less than threshold
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-statefulset", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
	}
	err := clientSet.createOwnerObjects(context.TODO(), sts)
	require.NoError(t, err)
	controlledPods = createPods(t, clientSet.allNamespacesClient, sts, &startTime, controlledPods, restartingUnderThreshold())
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

func createPods(t *testing.T, allNamespacesClient client.Client, owner metav1.Object, startTime *metav1.Time, podsToTrack []*corev1.Pod, restartStatus []corev1.ContainerStatus, conditions ...corev1.PodCondition) []*corev1.Pod {
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-pod-%d", owner.GetName(), i), Namespace: owner.GetNamespace()},
			Status:     corev1.PodStatus{StartTime: startTime, Conditions: conditions, ContainerStatuses: restartStatus},
		}
		err := controllerutil.SetControllerReference(owner, pod, scheme.Scheme)
		require.NoError(t, err)
		podsToTrack = append(podsToTrack, pod)
		err = allNamespacesClient.Create(context.TODO(), pod)
		require.NoError(t, err)
	}
	return podsToTrack
}

func prepareReconcile(t *testing.T, name string, getHostClusterFunc func(fakeClient client.Client) cluster.GetHostClusterFunc, initIdlerObjs ...client.Object) (*Reconciler, reconcile.Request, *test.FakeClient, *test.FakeClient, *fakedynamic.FakeDynamicClient) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	fakeClient := test.NewFakeClient(t, initIdlerObjs...)
	allNamespacesClient := test.NewFakeClient(t)
	dynamicClient := fakedynamic.NewSimpleDynamicClient(s)

	fakeDiscovery := fakeclientset.NewSimpleClientset().Discovery()

	scalesClient := fakescale.FakeScaleClient{}
	scalesClient.AddReactor("patch", "*", func(rawAction clienttest.Action) (bool, runtime.Object, error) {
		action := rawAction.(clienttest.PatchAction) // nolint: forcetypeassert

		// update owned deployment
		d := &appsv1.Deployment{}
		err := allNamespacesClient.Get(context.TODO(), types.NamespacedName{Name: action.GetName() + "-deployment", Namespace: action.GetNamespace()}, d)
		if err != nil {
			return false, nil, err
		}
		err = allNamespacesClient.Patch(context.TODO(), d, client.RawPatch(types.MergePatchType, action.GetPatch()))
		if err != nil {
			return false, nil, err
		}

		return false, nil, nil
	})

	// Mock internal server error for Camel K integrations in order to replicate default behavior with missing spec.replicas field
	scalesClient.AddReactor("get", "integrations", func(rawAction clienttest.Action) (bool, runtime.Object, error) {
		return true, nil, &apierrors.StatusError{
			ErrStatus: metav1.Status{
				Message: "Internal error occurred: the spec replicas field \".spec.replicas\" does not exist",
				Reason:  metav1.StatusReasonInternalError,
				Code:    http.StatusInternalServerError,
			},
		}
	})

	// Mock proper scale resource for Camel K KameletBinding resources
	scalesClient.AddReactor("get", "kameletbindings", func(rawAction clienttest.Action) (bool, runtime.Object, error) {
		action := rawAction.(clienttest.GetAction) // nolint: forcetypeassert
		obj := &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{
				Name:      action.GetName(),
				Namespace: action.GetNamespace(),
			},
			Spec: autoscalingv1.ScaleSpec{
				Replicas: 3,
			},
		}
		return true, obj, nil
	})

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
		ScalesClient:        &scalesClient,
		Scheme:              s,
		GetHostCluster:      getHostClusterFunc(fakeClient),
		Namespace:           test.MemberOperatorNs,
	}
	return r, reconcile.Request{NamespacedName: test.NamespacedName(test.MemberOperatorNs, name)}, fakeClient, allNamespacesClient, dynamicClient
}

// prepareReconcileWithPodsRunningTooLong prepares a reconcile with an Idler which already tracking pods running for too long
func prepareReconcileWithPodsRunningTooLong(t *testing.T, idler toolchainv1alpha1.Idler) (*Reconciler, reconcile.Request, *test.FakeClient, *test.FakeClient, *fakedynamic.FakeDynamicClient) {
	reconciler, req, cl, allCl, dynamicClient := prepareReconcile(t, idler.Name, getHostCluster, &idler)
	payloads := preparePayloads(t, reconciler, idler.Name, "", expiredStartTimes(idler.Spec.TimeoutSeconds))
	//start tracking pods, so the Idler status is filled with the tracked pods
	_, err := reconciler.Reconcile(context.TODO(), req)
	require.NoError(t, err)
	memberoperatortest.AssertThatIdler(t, idler.Name, cl).TracksPods(payloads.allPods)
	return reconciler, req, cl, allCl, dynamicClient
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
