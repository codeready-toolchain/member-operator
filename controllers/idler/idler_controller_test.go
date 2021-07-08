package idler

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	memberoperatortest "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	openshiftappsv1 "github.com/openshift/api/apps/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcile(t *testing.T) {

	t.Run("No Idler resource found", func(t *testing.T) {
		// given
		requestName := "not-existing-name"
		reconciler, req, _, _ := prepareReconcile(t, requestName)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then - there should not be any error, the controller should only log that the resource was not found
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("Fail to get Idler resource", func(t *testing.T) {
		// given
		reconciler, req, cl, _ := prepareReconcile(t, "cant-get-idler")
		cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
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
				DeletionTimestamp: &now,
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 30},
		}
		reconciler, req, _, _ := prepareReconcile(t, "being-deleted", idler)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then - ignore the idler which is being deleted
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	})
}

func TestEnsureIdling(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))

	t.Run("No pods in namespace managed by idler", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "john-dev",
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 30},
		}

		reconciler, req, cl, _ := prepareReconcile(t, idler.Name, idler)
		preparePayloads(t, reconciler, "another-namespace", "", time.Now()) // noise

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		// no pods found - the controller will requeue after the idler's timeout
		assert.Equal(t, reconcile.Result{
			Requeue:      true,
			RequeueAfter: 30 * time.Second,
		}, res)
		memberoperatortest.AssertThatIdler(t, idler.Name, cl).HasConditions(memberoperatortest.Running())
	})

	t.Run("Idle pods", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "alex-stage",
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 60},
		}
		reconciler, req, cl, allCl := prepareReconcile(t, idler.Name, idler)
		halfOfIdlerTimeoutAgo := time.Now().Add(-time.Duration(idler.Spec.TimeoutSeconds/2) * time.Second)
		podsTooEarlyToKill := preparePayloads(t, reconciler, idler.Name, "", halfOfIdlerTimeoutAgo)
		idlerTimeoutPlusOneSecondAgo := time.Now().Add(-time.Duration(idler.Spec.TimeoutSeconds+1) * time.Second)
		podsRunningForTooLong := preparePayloads(t, reconciler, idler.Name, "todelete-", idlerTimeoutPlusOneSecondAgo)
		noise := preparePayloads(t, reconciler, "another-namespace", "", idlerTimeoutPlusOneSecondAgo)

		t.Run("First reconcile. Start tracking.", func(t *testing.T) {
			//when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			// Idler tracks all pods now but pods have not been deleted yet
			memberoperatortest.AssertThatInIdleableCluster(t, allCl).
				PodsExist(podsRunningForTooLong.standalonePods).
				PodsExist(podsTooEarlyToKill.standalonePods).
				PodsExist(noise.standalonePods).
				DaemonSetExists(podsRunningForTooLong.daemonSet).
				DaemonSetExists(podsTooEarlyToKill.daemonSet).
				DaemonSetExists(noise.daemonSet).
				JobExists(podsRunningForTooLong.job).
				JobExists(podsTooEarlyToKill.job).
				JobExists(noise.job).
				DeploymentScaledUp(podsRunningForTooLong.deployment).
				DeploymentScaledUp(podsTooEarlyToKill.deployment).
				DeploymentScaledUp(noise.deployment).
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
				StatefulSetScaledUp(noise.statefulSet)

			// Tracked pods
			memberoperatortest.AssertThatIdler(t, idler.Name, cl).
				TracksPods(append(podsTooEarlyToKill.allPods, podsRunningForTooLong.allPods...)).
				HasConditions(memberoperatortest.Running())

			assert.True(t, res.Requeue)
			assert.Equal(t, int(res.RequeueAfter), 0) // pods running for too long should be killed immediately

			t.Run("Second Reconcile. Delete long running pods.", func(t *testing.T) {
				//when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)
				// Too long running pods are gone. All long running controllers are scaled down.
				// The rest of the pods are still there and controllers are scaled up.
				memberoperatortest.AssertThatInIdleableCluster(t, allCl).
					PodsDoNotExist(podsRunningForTooLong.standalonePods).
					PodsExist(podsTooEarlyToKill.standalonePods).
					PodsExist(noise.standalonePods).
					DaemonSetDoesNotExist(podsRunningForTooLong.daemonSet).
					DaemonSetExists(podsTooEarlyToKill.daemonSet).
					DaemonSetExists(noise.daemonSet).
					JobDoesNotExist(podsRunningForTooLong.job).
					JobExists(podsTooEarlyToKill.job).
					JobExists(noise.job).
					DeploymentScaledDown(podsRunningForTooLong.deployment).
					DeploymentScaledUp(podsTooEarlyToKill.deployment).
					DeploymentScaledUp(noise.deployment).
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
					StatefulSetScaledUp(noise.statefulSet)

				// Still tracking all pods. Even deleted ones.
				memberoperatortest.AssertThatIdler(t, idler.Name, cl).
					TracksPods(podsTooEarlyToKill.allPods).
					HasConditions(memberoperatortest.Running())

				assert.True(t, res.Requeue)
				assert.Less(t, int64(res.RequeueAfter), int64(time.Duration(idler.Spec.TimeoutSeconds)*time.Second))

				t.Run("Third Reconcile. Stop tracking deleted pods.", func(t *testing.T) {
					//when
					res, err := reconciler.Reconcile(context.TODO(), req)

					// then
					require.NoError(t, err)
					// Tracking existing pods only.
					memberoperatortest.AssertThatIdler(t, idler.Name, cl).
						TracksPods(append(podsTooEarlyToKill.allPods, podsRunningForTooLong.controlledPods...)).
						HasConditions(memberoperatortest.Running())

					assert.True(t, res.Requeue)
					assert.Less(t, int64(res.RequeueAfter), int64(time.Duration(idler.Spec.TimeoutSeconds)*time.Second))

					t.Run("No pods. No requeue.", func(t *testing.T) {
						//given
						// cleanup remaining pods
						pods := append(podsTooEarlyToKill.allPods, podsRunningForTooLong.controlledPods...)
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
							HasConditions(memberoperatortest.Running())

						// requeue after the idler timeout
						assert.Equal(t, reconcile.Result{
							Requeue:      true,
							RequeueAfter: 60 * time.Second,
						}, res)
					})
				})
			})
		})
	})
}

func TestEnsureIdlingFailed(t *testing.T) {
	t.Run("Fail if Idler.Spec.TimeoutSec is invalid", func(t *testing.T) {
		assertInvalidTimeout := func(timeout int32) {
			// given
			idler := &toolchainv1alpha1.Idler{
				ObjectMeta: metav1.ObjectMeta{
					Name: "john-dev",
				},
				Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: timeout},
			}
			reconciler, req, cl, _ := prepareReconcile(t, idler.Name, idler)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			memberoperatortest.AssertThatIdler(t, idler.Name, cl).HasConditions(memberoperatortest.FailedToIdle("timeoutSeconds should be bigger than 0"))
		}

		assertInvalidTimeout(0)
		assertInvalidTimeout(-1)
	})

	t.Run("Fail if can't list pods", func(t *testing.T) {
		// given
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "john-dev",
			},
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 30},
		}

		reconciler, req, cl, allCl := prepareReconcile(t, idler.Name, idler)
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
			Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: 60},
		}

		t.Run("can't get controllers because of general error", func(t *testing.T) {
			assertCanNotGetObject := func(inaccessible runtime.Object, errMsg string) {
				// given
				reconciler, req, cl, allCl := prepareReconcileWithPodsRunningTooLong(t, idler)

				get := allCl.MockGet
				defer func() { allCl.MockGet = get }()
				allCl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
					if reflect.TypeOf(obj) == reflect.TypeOf(inaccessible) {
						return errors.New(errMsg)
					}
					return allCl.Client.Get(ctx, key, obj)
				}

				//when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.EqualError(t, err, fmt.Sprintf("failed to ensure idling 'alex-stage': %s", errMsg))
				assert.Equal(t, reconcile.Result{}, res)
				memberoperatortest.AssertThatIdler(t, idler.Name, cl).HasConditions(memberoperatortest.FailedToIdle(errMsg))
			}

			assertCanNotGetObject(&appsv1.Deployment{}, "can't get deployment")
			assertCanNotGetObject(&appsv1.ReplicaSet{}, "can't get replicaset")
			assertCanNotGetObject(&appsv1.DaemonSet{}, "can't get daemonset")
			assertCanNotGetObject(&batchv1.Job{}, "can't get job")
			assertCanNotGetObject(&appsv1.StatefulSet{}, "can't get statefulset")
			assertCanNotGetObject(&openshiftappsv1.DeploymentConfig{}, "can't get deploymentconfig")
			assertCanNotGetObject(&corev1.ReplicationController{}, "can't get replicationcontroller")
		})

		t.Run("can't get controllers because not found", func(t *testing.T) {
			assertCanNotGetObject := func(inaccessible runtime.Object) {
				// given
				reconciler, req, cl, allCl := prepareReconcileWithPodsRunningTooLong(t, idler)

				get := allCl.MockGet
				defer func() { allCl.MockGet = get }()
				allCl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
					if reflect.TypeOf(obj) == reflect.TypeOf(inaccessible) {
						return apierrors.NewNotFound(schema.GroupResource{
							Group:    "",
							Resource: reflect.TypeOf(obj).Name(),
						}, key.Name)
					}
					return allCl.Client.Get(ctx, key, obj)
				}

				//when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				assert.NoError(t, err) // 'NotFound' errors are ignored!
				assert.Equal(t, reconcile.Result{
					Requeue:      true,
					RequeueAfter: 60 * time.Second,
				}, res)
				memberoperatortest.AssertThatIdler(t, idler.Name, cl).HasConditions(memberoperatortest.Running())
			}

			assertCanNotGetObject(&appsv1.Deployment{})
			assertCanNotGetObject(&appsv1.ReplicaSet{})
			assertCanNotGetObject(&appsv1.DaemonSet{})
			assertCanNotGetObject(&batchv1.Job{})
			assertCanNotGetObject(&appsv1.StatefulSet{})
			assertCanNotGetObject(&openshiftappsv1.DeploymentConfig{})
			assertCanNotGetObject(&corev1.ReplicationController{})
		})

		t.Run("can't update controllers", func(t *testing.T) {
			assertCanNotUpdateObject := func(inaccessible runtime.Object, errMsg string) {
				// given
				reconciler, req, cl, allCl := prepareReconcileWithPodsRunningTooLong(t, idler)

				update := allCl.MockUpdate
				defer func() { allCl.MockUpdate = update }()
				allCl.MockUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
					if reflect.TypeOf(obj) == reflect.TypeOf(inaccessible) {
						return errors.New(errMsg)
					}
					return allCl.Client.Update(ctx, obj, opts...)
				}

				//when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.EqualError(t, err, fmt.Sprintf("failed to ensure idling 'alex-stage': %s", errMsg))
				assert.Equal(t, reconcile.Result{}, res)
				memberoperatortest.AssertThatIdler(t, idler.Name, cl).HasConditions(memberoperatortest.FailedToIdle(errMsg))
			}

			assertCanNotUpdateObject(&appsv1.Deployment{}, "can't update deployment")
			assertCanNotUpdateObject(&appsv1.ReplicaSet{}, "can't update replicaset")
			assertCanNotUpdateObject(&appsv1.StatefulSet{}, "can't update statefulset")
			assertCanNotUpdateObject(&openshiftappsv1.DeploymentConfig{}, "can't update deploymentconfig")
			assertCanNotUpdateObject(&corev1.ReplicationController{}, "can't update replicationcontroller")
		})

		t.Run("can't delete payloads", func(t *testing.T) {
			assertCanNotDeleteObject := func(inaccessible runtime.Object, errMsg string) {
				// given
				reconciler, req, cl, allCl := prepareReconcileWithPodsRunningTooLong(t, idler)

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
				require.EqualError(t, err, fmt.Sprintf("failed to ensure idling 'alex-stage': %s", errMsg))
				assert.Equal(t, reconcile.Result{}, res)
				memberoperatortest.AssertThatIdler(t, idler.Name, cl).HasConditions(memberoperatortest.FailedToIdle(errMsg))
			}

			assertCanNotDeleteObject(&appsv1.DaemonSet{}, "can't delete daemonset")
			assertCanNotDeleteObject(&batchv1.Job{}, "can't delete job")
			assertCanNotDeleteObject(&corev1.Pod{}, "can't delete pod")
		})
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

	deployment            *appsv1.Deployment
	replicaSet            *appsv1.ReplicaSet
	daemonSet             *appsv1.DaemonSet
	statefulSet           *appsv1.StatefulSet
	deploymentConfig      *openshiftappsv1.DeploymentConfig
	replicationController *corev1.ReplicationController
	job                   *batchv1.Job
}

func preparePayloads(t *testing.T, r *Reconciler, namespace, namePrefix string, startTime time.Time) payloads {
	sTime := metav1.NewTime(startTime)
	replicas := int32(3)

	// Deployment
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-deployment", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	err := r.AllNamespacesClient.Create(context.TODO(), d)
	require.NoError(t, err)
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-replicaset", d.Name), Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err = controllerutil.SetControllerReference(d, rs, r.Scheme)
	require.NoError(t, err)
	err = r.AllNamespacesClient.Create(context.TODO(), rs)
	require.NoError(t, err)
	controlledPods := createPods(t, r, rs, sTime, make([]*corev1.Pod, 0, 3))

	// Standalone ReplicaSet
	standaloneRs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-replicaset", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err = r.AllNamespacesClient.Create(context.TODO(), standaloneRs)
	require.NoError(t, err)
	controlledPods = createPods(t, r, standaloneRs, sTime, controlledPods)

	// DaemonSet
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-daemonset", namePrefix, namespace), Namespace: namespace},
	}
	err = r.AllNamespacesClient.Create(context.TODO(), ds)
	require.NoError(t, err)
	controlledPods = createPods(t, r, ds, sTime, controlledPods)

	// Job
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-job", namePrefix, namespace), Namespace: namespace},
	}
	err = r.AllNamespacesClient.Create(context.TODO(), job)
	require.NoError(t, err)
	controlledPods = createPods(t, r, job, sTime, controlledPods)

	// StatefulSet
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-statefulset", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
	}
	err = r.AllNamespacesClient.Create(context.TODO(), sts)
	require.NoError(t, err)
	controlledPods = createPods(t, r, sts, sTime, controlledPods)

	// DeploymentConfig
	dc := &openshiftappsv1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-deploymentconfig", namePrefix, namespace), Namespace: namespace},
		Spec:       openshiftappsv1.DeploymentConfigSpec{Replicas: replicas},
	}
	err = r.AllNamespacesClient.Create(context.TODO(), dc)
	require.NoError(t, err)
	rc := &corev1.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-replicationcontroller", dc.Name), Namespace: namespace},
		Spec:       corev1.ReplicationControllerSpec{Replicas: &replicas},
	}
	err = controllerutil.SetControllerReference(dc, rc, r.Scheme)
	require.NoError(t, err)
	err = r.AllNamespacesClient.Create(context.TODO(), rc)
	require.NoError(t, err)
	controlledPods = createPods(t, r, rc, sTime, controlledPods)

	// Standalone ReplicationController
	standaloneRC := &corev1.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-replicationcontroller", namePrefix, namespace), Namespace: namespace},
		Spec:       corev1.ReplicationControllerSpec{Replicas: &replicas},
	}
	err = r.AllNamespacesClient.Create(context.TODO(), standaloneRC)
	require.NoError(t, err)
	controlledPods = createPods(t, r, standaloneRC, sTime, controlledPods)

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
	standalonePods := createPods(t, r, idler, sTime, make([]*corev1.Pod, 0, 3))

	// Pods with no owner.
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-pod-%d", namePrefix, namespace, i), Namespace: namespace},
			Status:     corev1.PodStatus{StartTime: &sTime},
		}
		require.NoError(t, err)
		standalonePods = append(standalonePods, pod)
		err = r.AllNamespacesClient.Create(context.TODO(), pod)
		require.NoError(t, err)
	}

	return payloads{
		standalonePods:        standalonePods,
		controlledPods:        controlledPods,
		allPods:               append(standalonePods, controlledPods...),
		deployment:            d,
		replicaSet:            standaloneRs,
		daemonSet:             ds,
		statefulSet:           sts,
		deploymentConfig:      dc,
		replicationController: standaloneRC,
		job:                   job,
	}
}

func createPods(t *testing.T, r *Reconciler, owner v1.Object, startTime metav1.Time, podsToTrack []*corev1.Pod) []*corev1.Pod {
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-pod-%d", owner.GetName(), i), Namespace: owner.GetNamespace()},
			Status:     corev1.PodStatus{StartTime: &startTime},
		}
		err := controllerutil.SetControllerReference(owner, pod, r.Scheme)
		require.NoError(t, err)
		podsToTrack = append(podsToTrack, pod)
		err = r.AllNamespacesClient.Create(context.TODO(), pod)
		require.NoError(t, err)
	}
	return podsToTrack
}

func prepareReconcile(t *testing.T, name string, initIdlerObjs ...runtime.Object) (*Reconciler, reconcile.Request, *test.FakeClient, *test.FakeClient) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	fakeClient := test.NewFakeClient(t, initIdlerObjs...)
	allNamespacesClient := test.NewFakeClient(t)
	r := &Reconciler{
		Client:              fakeClient,
		AllNamespacesClient: allNamespacesClient,
		Scheme:              s,
	}
	return r, reconcile.Request{NamespacedName: test.NamespacedName(test.MemberOperatorNs, name)}, fakeClient, allNamespacesClient
}

// prepareReconcileWithPodsRunningTooLong prepares a reconcile with an Idler which already tracking pods running for too long
func prepareReconcileWithPodsRunningTooLong(t *testing.T, idler toolchainv1alpha1.Idler) (*Reconciler, reconcile.Request, *test.FakeClient, *test.FakeClient) {
	reconciler, req, cl, allCl := prepareReconcile(t, idler.Name, &idler)
	idlerTimeoutPlusOneSecondAgo := time.Now().Add(-time.Duration(idler.Spec.TimeoutSeconds+1) * time.Second)
	payloads := preparePayloads(t, reconciler, idler.Name, "", idlerTimeoutPlusOneSecondAgo)
	//start tracking pods, so the Idler status is filled with the tracked pods
	_, err := reconciler.Reconcile(context.TODO(), req)
	require.NoError(t, err)
	memberoperatortest.AssertThatIdler(t, idler.Name, cl).TracksPods(payloads.allPods)
	return reconciler, req, cl, allCl
}
