package idler

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/codeready-toolchain/api/pkg/apis"
	"github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	openshiftappsv1 "github.com/openshift/api/apps/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcile(t *testing.T) {
	t.Run("No Idler resource found", func(t *testing.T) {
		// given
		requestName := "not-existing-name"
		reconciler, req, _ := prepareReconcile(t, requestName)

		// when
		res, err := reconciler.Reconcile(req)

		// then - there should not be any error, the controller should only log that the resource was not found
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("Fail to get Idler resource", func(t *testing.T) {
		// given
		reconciler, req, cl := prepareReconcile(t, "cant-get-idler")
		cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
			if key.Name == "cant-get-idler" {
				return errors.New("can't get idler")
			}
			return nil
		}

		// when
		res, err := reconciler.Reconcile(req)

		// then
		require.EqualError(t, err, "can't get idler")
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("Idler being deleted", func(t *testing.T) {
		// given
		now := metav1.Now()
		idler := &v1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "being-deleted",
				DeletionTimestamp: &now,
			},
			Spec: v1alpha1.IdlerSpec{TimeoutSeconds: 30},
		}
		reconciler, req, _ := prepareReconcile(t, "being-deleted", idler)

		// when
		res, err := reconciler.Reconcile(req)

		// then - ignore the idler which is being deleted
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	})
}

func TestEnsureIdling(t *testing.T) {
	johnDevIdler := &v1alpha1.Idler{
		ObjectMeta: metav1.ObjectMeta{
			Name: "john-dev",
		},
		Spec: v1alpha1.IdlerSpec{TimeoutSeconds: 30},
	}

	t.Run("Fail to list pods", func(t *testing.T) {
		// given
		reconciler, req, cl := prepareReconcile(t, johnDevIdler.Name, johnDevIdler)
		cl.MockList = func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
			pl := &corev1.PodList{}
			if reflect.TypeOf(list) == reflect.TypeOf(pl) && len(opts) == 1 {
				return errors.New("can't list pods")
			}
			return nil
		}

		// when
		res, err := reconciler.Reconcile(req)

		// then
		require.EqualError(t, err, "failed to ensure idling 'john-dev': can't list pods")
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("No pods in namespace managed by idler", func(t *testing.T) {
		// given
		reconciler, req, _ := prepareReconcile(t, johnDevIdler.Name, johnDevIdler)
		preparePods(t, reconciler, "another-namespace", time.Now()) // noise

		// when
		res, err := reconciler.Reconcile(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("Idle pods", func(t *testing.T) {
		// given
		//reconciler, req, cl := prepareReconcile(t, johnDevIdler.Name, johnDevIdler)
		//podsToTrackOnly := preparePods(t, reconciler, johnDevIdler.Name, time.Now())
		//idlerTimeoutPlusOneSecondAgo := time.Now().Add(-time.Duration(johnDevIdler.Spec.TimeoutSeconds + 1))
		//podsToBeKilled := preparePods(t, reconciler, johnDevIdler.Name, idlerTimeoutPlusOneSecondAgo)
		//noise := preparePods(t, reconciler, "another-namespace", idlerTimeoutPlusOneSecondAgo)
		//
		//// when
		//res, err := reconciler.Reconcile(req)
		//
		//// then
		//require.NoError(t, err)
		//assert.Equal(t, reconcile.Result{}, res)
	})
}

type pods struct {
	podsToKill  []*corev1.Pod
	podsToTrack []*corev1.Pod

	deployment            *appsv1.Deployment
	replicaSet            *appsv1.ReplicaSet
	daemonSet             *appsv1.DaemonSet
	statefulSer           *appsv1.StatefulSet
	deploymentConfig      *openshiftappsv1.DeploymentConfig
	replicationController *corev1.ReplicationController
}

func preparePods(t *testing.T, r *ReconcileIdler, namespace string, startTime time.Time) pods {
	podsToKill := make([]*corev1.Pod, 0)

	sTime := metav1.NewTime(startTime)
	replicas := int32(3)

	// Deployment
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: namespace + "-deployment", Namespace: namespace},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	err := r.client.Create(context.TODO(), d)
	require.NoError(t, err)
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: d.Name + "-replicaset", Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err = controllerutil.SetControllerReference(d, rs, r.scheme)
	require.NoError(t, err)
	err = r.client.Create(context.TODO(), rs)
	require.NoError(t, err)
	podsToTrack := createPods(t, r, rs, sTime, make([]*corev1.Pod, 3))

	// Standalone ReplicaSet
	standaloneRs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: namespace + "-replicaset", Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err = r.client.Create(context.TODO(), standaloneRs)
	require.NoError(t, err)
	podsToTrack = createPods(t, r, standaloneRs, sTime, podsToTrack)

	// DaemonSet
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: namespace + "-daemonset", Namespace: namespace},
	}
	err = r.client.Create(context.TODO(), ds)
	require.NoError(t, err)
	podsToTrack = createPods(t, r, ds, sTime, podsToTrack)

	// StatefulSet
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: namespace + "-statefulset", Namespace: namespace},
	}
	err = r.client.Create(context.TODO(), sts)
	require.NoError(t, err)
	podsToTrack = createPods(t, r, sts, sTime, podsToTrack)

	// DeploymentConfig
	dc := &openshiftappsv1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{Name: namespace + "-deploymentconfig", Namespace: namespace},
		Spec:       openshiftappsv1.DeploymentConfigSpec{Replicas: replicas},
	}
	err = r.client.Create(context.TODO(), dc)
	require.NoError(t, err)
	rc := &corev1.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{Name: dc.Name + "-replicationcontroller", Namespace: namespace},
		Spec:       corev1.ReplicationControllerSpec{Replicas: &replicas},
	}
	err = controllerutil.SetControllerReference(dc, rc, r.scheme)
	require.NoError(t, err)
	err = r.client.Create(context.TODO(), rc)
	require.NoError(t, err)
	podsToTrack = createPods(t, r, rc, sTime, podsToTrack)

	// Standalone ReplicationController
	standaloneRC := &corev1.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{Name: namespace + "-replicationcontroller", Namespace: namespace},
		Spec:       corev1.ReplicationControllerSpec{Replicas: &replicas},
	}
	err = r.client.Create(context.TODO(), standaloneRC)
	require.NoError(t, err)
	podsToTrack = createPods(t, r, standaloneRC, sTime, podsToTrack)

	return pods{
		podsToKill:            podsToKill,
		podsToTrack:           podsToTrack,
		deployment:            d,
		replicaSet:            standaloneRs,
		daemonSet:             ds,
		statefulSer:           sts,
		deploymentConfig:      dc,
		replicationController: standaloneRC,
	}
}

func createPods(t *testing.T, r *ReconcileIdler, owner v1.Object, startTime metav1.Time, podsToTrack []*corev1.Pod) []*corev1.Pod {
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-pod-%d", owner.GetName(), i), Namespace: owner.GetNamespace()},
			Status:     corev1.PodStatus{StartTime: &startTime},
		}
		err := controllerutil.SetControllerReference(owner, pod, r.scheme)
		require.NoError(t, err)
		podsToTrack = append(podsToTrack, pod)
		err = r.client.Create(context.TODO(), pod)
		require.NoError(t, err)
	}
	return podsToTrack
}

func prepareReconcile(t *testing.T, name string, initObjs ...runtime.Object) (*ReconcileIdler, reconcile.Request, *test.FakeClient) {
	s := scheme.Scheme
	addToSchemes := append(apis.AddToSchemes, corev1.AddToScheme)
	addToSchemes = append(addToSchemes, appsv1.AddToScheme)
	addToSchemes = append(addToSchemes, openshiftappsv1.Install)
	err := addToSchemes.AddToScheme(s)
	require.NoError(t, err)

	fakeClient := test.NewFakeClient(t, initObjs...)
	r := &ReconcileIdler{
		client: fakeClient,
		scheme: s,
		config: configuration.LoadConfig(),
	}
	return r, reconcile.Request{NamespacedName: test.NamespacedName(test.MemberOperatorNs, name)}, fakeClient
}
