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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
		preparePods(t, reconciler, "another-namespace", "", time.Now()) // noise

		// when
		res, err := reconciler.Reconcile(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("Idle pods", func(t *testing.T) {
		// given
		idler := &v1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "alex-stage",
			},
			Spec: v1alpha1.IdlerSpec{TimeoutSeconds: 30},
		}
		reconciler, req, cl := prepareReconcile(t, idler.Name, idler)
		podsTooEarlyToKill := preparePods(t, reconciler, idler.Name, "", time.Now())
		idlerTimeoutPlusOneSecondAgo := time.Now().Add(-time.Duration(idler.Spec.TimeoutSeconds + 1))
		podsRunningForTooLong := preparePods(t, reconciler, idler.Name, "todelete-", idlerTimeoutPlusOneSecondAgo)
		noise := preparePods(t, reconciler, "another-namespace", "", idlerTimeoutPlusOneSecondAgo)

		t.Run("First reconcile. Start tracking", func(t *testing.T) {
			//when
			res, err := reconciler.Reconcile(req)

			// then
			require.NoError(t, err)

			// Idler tracks all pods now.
			AssertThatCluster(t, cl).
				HasPods(podsRunningForTooLong.podsToKill).
				HasPods(podsTooEarlyToKill.podsToKill).
				HasPods(noise.podsToKill)
			// Tracked pods
			AssertThatIdler(t, idler.Name, cl).TracksPods(append(podsTooEarlyToKill.podsToTrack, podsRunningForTooLong.podsToTrack...))

			assert.Equal(t, reconcile.Result{}, res)
		})

		t.Run("Second Reconcile. Delete long running", func(t *testing.T) {
			//when
			res, err := reconciler.Reconcile(req)

			// then
			require.NoError(t, err)

			// Too long running pods are gone. The rest of the pods are still there.
			AssertThatCluster(t, cl).
				DoesnHavePods(podsRunningForTooLong.podsToKill).
				HasPods(podsTooEarlyToKill.podsToKill).
				HasPods(noise.podsToKill)
			// Tracked pods
			AssertThatIdler(t, idler.Name, cl).TracksPods(podsTooEarlyToKill.podsToTrack)

			assert.Equal(t, reconcile.Result{}, res)
		})
	})
}

type IdlerAssertion struct {
	idler          *v1alpha1.Idler
	client         client.Client
	namespacedName types.NamespacedName
	t              *testing.T
}

func (a *IdlerAssertion) loadIdlerAssertion() error {
	if a.idler != nil {
		return nil
	}
	idler := &v1alpha1.Idler{}
	err := a.client.Get(context.TODO(), a.namespacedName, idler)
	a.idler = idler
	return err
}

func AssertThatIdler(t *testing.T, name string, client client.Client) *IdlerAssertion {
	return &IdlerAssertion{
		client:         client,
		namespacedName: types.NamespacedName{Name: name},
		t:              t,
	}
}

func (a *IdlerAssertion) TracksPods(pods []*corev1.Pod) *IdlerAssertion {
	err := a.loadIdlerAssertion()
	require.NoError(a.t, err)

	require.Len(a.t, a.idler.Status.Pods, len(pods))
	for _, pod := range pods {
		expected := v1alpha1.Pod{
			Name:      pod.Name,
			StartTime: *pod.Status.StartTime,
		}
		assert.Contains(a.t, a.idler.Status.Pods, expected)
	}
	return a
}

type ClusterAssertion struct {
	client client.Client
	t      *testing.T
}

func AssertThatCluster(t *testing.T, client client.Client) *ClusterAssertion {
	return &ClusterAssertion{
		client: client,
		t:      t,
	}
}

func (a *ClusterAssertion) DoesnHavePods(pods []*corev1.Pod) *ClusterAssertion {
	for _, pod := range pods {
		p := &corev1.Pod{}
		err := a.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, p)
		require.Error(a.t, err, "pod still exist", p)
		assert.True(a.t, apierrors.IsNotFound(err))
	}
	return a
}

func (a *ClusterAssertion) HasPods(pods []*corev1.Pod) *ClusterAssertion {
	for _, pod := range pods {
		p := &corev1.Pod{}
		err := a.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, p)
		require.NoError(a.t, err)
	}
	return a
}

type payloads struct {
	// podsToKill are pods which are supposed to be tracked and also deleted directly by the Idler controller
	// if run for too long
	podsToKill []*corev1.Pod
	// podsToTrack are pods which are supposed to be tracked only and won't be deleted by the Idler controller
	// because they are managed by Deployment/ReplicaSet/etc controllers
	podsToTrack []*corev1.Pod

	deployment            *appsv1.Deployment
	replicaSet            *appsv1.ReplicaSet
	daemonSet             *appsv1.DaemonSet
	statefulSer           *appsv1.StatefulSet
	deploymentConfig      *openshiftappsv1.DeploymentConfig
	replicationController *corev1.ReplicationController
}

func preparePods(t *testing.T, r *ReconcileIdler, namespace, namePrefix string, startTime time.Time) payloads {
	sTime := metav1.NewTime(startTime)
	replicas := int32(3)

	// Deployment
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-deployment", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	err := r.client.Create(context.TODO(), d)
	require.NoError(t, err)
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-replicaset", d.Name), Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err = controllerutil.SetControllerReference(d, rs, r.scheme)
	require.NoError(t, err)
	err = r.client.Create(context.TODO(), rs)
	require.NoError(t, err)
	podsToTrack := createPods(t, r, rs, sTime, make([]*corev1.Pod, 0, 3))

	// Standalone ReplicaSet
	standaloneRs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-replicaset", namePrefix, namespace), Namespace: namespace},
		Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
	}
	err = r.client.Create(context.TODO(), standaloneRs)
	require.NoError(t, err)
	podsToTrack = createPods(t, r, standaloneRs, sTime, podsToTrack)

	// DaemonSet
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-daemonset", namePrefix, namespace), Namespace: namespace},
	}
	err = r.client.Create(context.TODO(), ds)
	require.NoError(t, err)
	podsToTrack = createPods(t, r, ds, sTime, podsToTrack)

	// StatefulSet
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-statefulset", namePrefix, namespace), Namespace: namespace},
	}
	err = r.client.Create(context.TODO(), sts)
	require.NoError(t, err)
	podsToTrack = createPods(t, r, sts, sTime, podsToTrack)

	// DeploymentConfig
	dc := &openshiftappsv1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-deploymentconfig", namePrefix, namespace), Namespace: namespace},
		Spec:       openshiftappsv1.DeploymentConfigSpec{Replicas: replicas},
	}
	err = r.client.Create(context.TODO(), dc)
	require.NoError(t, err)
	rc := &corev1.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-replicationcontroller", dc.Name), Namespace: namespace},
		Spec:       corev1.ReplicationControllerSpec{Replicas: &replicas},
	}
	err = controllerutil.SetControllerReference(dc, rc, r.scheme)
	require.NoError(t, err)
	err = r.client.Create(context.TODO(), rc)
	require.NoError(t, err)
	podsToTrack = createPods(t, r, rc, sTime, podsToTrack)

	// Standalone ReplicationController
	standaloneRC := &corev1.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-replicationcontroller", namePrefix, namespace), Namespace: namespace},
		Spec:       corev1.ReplicationControllerSpec{Replicas: &replicas},
	}
	err = r.client.Create(context.TODO(), standaloneRC)
	require.NoError(t, err)
	podsToTrack = createPods(t, r, standaloneRC, sTime, podsToTrack)

	// Pods with unknown owner. They are subject of direct management by the Idler.
	idler := &v1alpha1.Idler{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s%s-somename", namePrefix, namespace),
		},
		Spec: v1alpha1.IdlerSpec{TimeoutSeconds: 30},
	}
	podsToKill := createPods(t, r, idler, sTime, make([]*corev1.Pod, 0, 3))

	// Pods with no owner.
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s%s-pod-%d", namePrefix, namespace, i), Namespace: namespace},
			Status:     corev1.PodStatus{StartTime: &sTime},
		}
		require.NoError(t, err)
		podsToKill = append(podsToKill, pod)
		err = r.client.Create(context.TODO(), pod)
		require.NoError(t, err)
	}

	podsToTrack = append(podsToTrack, podsToKill...)

	return payloads{
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
