package test

import (
	"context"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	openshiftappsv1 "github.com/openshift/api/apps/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var vmGVR = schema.GroupVersionResource{Group: "kubevirt.io", Version: "v1", Resource: "virtualmachines"}

type IdlerAssertion struct {
	idler          *toolchainv1alpha1.Idler
	client         client.Client
	namespacedName types.NamespacedName
	t              *testing.T
}

func (a *IdlerAssertion) loadIdlerAssertion() error {
	if a.idler != nil {
		return nil
	}
	idler := &toolchainv1alpha1.Idler{}
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
		startTimeNoMilSec := pod.Status.StartTime.Truncate(time.Second)
		expected := toolchainv1alpha1.Pod{
			Name:      pod.Name,
			StartTime: metav1.NewTime(startTimeNoMilSec),
		}
		assert.Contains(a.t, a.idler.Status.Pods, expected)
	}
	return a
}

func (a *IdlerAssertion) HasConditions(expected ...toolchainv1alpha1.Condition) *IdlerAssertion {
	err := a.loadIdlerAssertion()
	require.NoError(a.t, err)
	test.AssertConditionsMatch(a.t, a.idler.Status.Conditions, expected...)
	return a
}

func (a *IdlerAssertion) ContainsCondition(expected toolchainv1alpha1.Condition) *IdlerAssertion {
	err := a.loadIdlerAssertion()
	require.NoError(a.t, err)
	test.AssertContainsCondition(a.t, a.idler.Status.Conditions, expected)
	return a
}

func FailedToIdle(message string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.IdlerUnableToEnsureIdlingReason,
		Message: message,
	}
}

func Running() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionTrue,
		Reason: toolchainv1alpha1.IdlerRunningReason,
	}
}

func IdlerNoDeactivation() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionTrue,
		Reason: toolchainv1alpha1.IdlerNoDeactivationReason,
	}
}

func IdlerNotificationCreated() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.IdlerTriggeredNotificationCreated,
		Status: corev1.ConditionTrue,
		Reason: toolchainv1alpha1.IdlerTriggeredReason,
	}
}

func IdlerNotificationCreationFailed(message string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.IdlerTriggeredNotificationCreated,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.IdlerTriggeredNotificationCreationFailedReason,
		Message: message,
	}
}

type IdleablePayloadAssertion struct {
	client        client.Client
	t             *testing.T
	dynamicClient *fakedynamic.FakeDynamicClient
}

func AssertThatInIdleableCluster(t *testing.T, client client.Client, dynamicClient *fakedynamic.FakeDynamicClient) *IdleablePayloadAssertion {
	return &IdleablePayloadAssertion{
		client:        client,
		t:             t,
		dynamicClient: dynamicClient,
	}
}

func (a *IdleablePayloadAssertion) PodsDoNotExist(pods []*corev1.Pod) *IdleablePayloadAssertion {
	for _, pod := range pods {
		p := &corev1.Pod{}
		err := a.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, p)
		require.Error(a.t, err, "pod %s still exists", p.Name)
		assert.True(a.t, apierrors.IsNotFound(err))
	}
	return a
}

func (a *IdleablePayloadAssertion) PodsExist(pods []*corev1.Pod) *IdleablePayloadAssertion {
	for _, pod := range pods {
		p := &corev1.Pod{}
		err := a.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, p)
		require.NoError(a.t, err)
	}
	return a
}

func (a *IdleablePayloadAssertion) DeploymentScaledDown(deployment *appsv1.Deployment) *IdleablePayloadAssertion {
	d := &appsv1.Deployment{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, d)
	require.NoError(a.t, err)
	require.NotNil(a.t, d.Spec.Replicas)
	assert.Equal(a.t, int32(0), *d.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) DeploymentScaledUp(deployment *appsv1.Deployment) *IdleablePayloadAssertion {
	d := &appsv1.Deployment{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, d)
	require.NoError(a.t, err)
	require.NotNil(a.t, d.Spec.Replicas)
	assert.Equal(a.t, int32(3), *d.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) ReplicaSetScaledDown(replicaSet *appsv1.ReplicaSet) *IdleablePayloadAssertion {
	r := &appsv1.ReplicaSet{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: replicaSet.Name, Namespace: replicaSet.Namespace}, r)
	require.NoError(a.t, err)
	require.NotNil(a.t, r.Spec.Replicas)
	assert.Equal(a.t, int32(0), *r.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) ReplicaSetScaledUp(replicaSet *appsv1.ReplicaSet) *IdleablePayloadAssertion {
	r := &appsv1.ReplicaSet{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: replicaSet.Name, Namespace: replicaSet.Namespace}, r)
	require.NoError(a.t, err)
	require.NotNil(a.t, r.Spec.Replicas)
	assert.Equal(a.t, int32(3), *r.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) DeploymentConfigScaledDown(deployment *openshiftappsv1.DeploymentConfig) *IdleablePayloadAssertion {
	d := &openshiftappsv1.DeploymentConfig{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, d)
	require.NoError(a.t, err)
	assert.Equal(a.t, int32(0), d.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) DeploymentConfigScaledUp(deployment *openshiftappsv1.DeploymentConfig) *IdleablePayloadAssertion {
	d := &openshiftappsv1.DeploymentConfig{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, d)
	require.NoError(a.t, err)
	assert.Equal(a.t, int32(3), d.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) ReplicationControllerScaledDown(rc *corev1.ReplicationController) *IdleablePayloadAssertion {
	r := &corev1.ReplicationController{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: rc.Name, Namespace: rc.Namespace}, r)
	require.NoError(a.t, err)
	require.NotNil(a.t, r.Spec.Replicas)
	assert.Equal(a.t, int32(0), *r.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) ReplicationControllerScaledUp(rc *corev1.ReplicationController) *IdleablePayloadAssertion {
	r := &corev1.ReplicationController{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: rc.Name, Namespace: rc.Namespace}, r)
	require.NoError(a.t, err)
	require.NotNil(a.t, r.Spec.Replicas)
	assert.Equal(a.t, int32(3), *r.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) DaemonSetExists(daemonSet *appsv1.DaemonSet) *IdleablePayloadAssertion {
	d := &appsv1.DaemonSet{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: daemonSet.Name, Namespace: daemonSet.Namespace}, d)
	require.NoError(a.t, err)
	return a
}

func (a *IdleablePayloadAssertion) DaemonSetDoesNotExist(daemonSet *appsv1.DaemonSet) *IdleablePayloadAssertion {
	d := &appsv1.DaemonSet{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: daemonSet.Name, Namespace: daemonSet.Namespace}, d)
	require.Error(a.t, err, "daemonSet %s still exists", d.Name)
	assert.True(a.t, apierrors.IsNotFound(err))
	return a
}

func (a *IdleablePayloadAssertion) JobExists(job *batchv1.Job) *IdleablePayloadAssertion {
	j := &batchv1.Job{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, j)
	require.NoError(a.t, err)
	return a
}

func (a *IdleablePayloadAssertion) JobDoesNotExist(job *batchv1.Job) *IdleablePayloadAssertion {
	j := &batchv1.Job{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, j)
	require.Error(a.t, err, "job %s still exists", j.Name)
	assert.True(a.t, apierrors.IsNotFound(err))
	return a
}

func (a *IdleablePayloadAssertion) StatefulSetScaledDown(statefulSet *appsv1.StatefulSet) *IdleablePayloadAssertion {
	s := &appsv1.StatefulSet{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: statefulSet.Name, Namespace: statefulSet.Namespace}, s)
	require.NoError(a.t, err)
	require.NotNil(a.t, s.Spec.Replicas)
	assert.Equal(a.t, int32(0), *s.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) StatefulSetScaledUp(statefulSet *appsv1.StatefulSet) *IdleablePayloadAssertion {
	s := &appsv1.StatefulSet{}
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: statefulSet.Name, Namespace: statefulSet.Namespace}, s)
	require.NoError(a.t, err)
	require.NotNil(a.t, s.Spec.Replicas)
	assert.Equal(a.t, int32(3), *s.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) VMRunning(vm *unstructured.Unstructured) *IdleablePayloadAssertion {
	return a.vmRunning(vm, true)
}

func (a *IdleablePayloadAssertion) VMStopped(vm *unstructured.Unstructured) *IdleablePayloadAssertion {
	return a.vmRunning(vm, false)
}

func (a *IdleablePayloadAssertion) vmRunning(vm *unstructured.Unstructured, running bool) *IdleablePayloadAssertion {
	vm, err := a.dynamicClient.Resource(vmGVR).Namespace(vm.GetNamespace()).Get(context.TODO(), vm.GetName(), metav1.GetOptions{})
	require.NoError(a.t, err)
	val, found, err := unstructured.NestedBool(vm.Object, "spec", "running")
	require.NoError(a.t, err)
	assert.True(a.t, found)
	assert.Equal(a.t, running, val)
	return a
}
