package test

import (
	"context"
	"fmt"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakescale "k8s.io/client-go/scale/fake"
	k8sgotest "k8s.io/client-go/testing"

	openshiftappsv1 "github.com/openshift/api/apps/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

func AssertThatIdler(t *testing.T, name string, fakeClients *FakeClientSet) *IdlerAssertion {
	return &IdlerAssertion{
		client:         fakeClients.DefaultClient,
		namespacedName: types.NamespacedName{Name: name},
		t:              t,
	}
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

type FakeClientSet struct {
	DefaultClient, AllNamespacesClient *test.FakeClient
	DynamicClient                      *fakedynamic.FakeDynamicClient
	ScalesClient                       *fakescale.FakeScaleClient
}
type IdleablePayloadAssertion struct {
	client        client.Client
	t             *testing.T
	dynamicClient *fakedynamic.FakeDynamicClient
	scalesClient  *fakescale.FakeScaleClient
}

func AssertThatInIdleableCluster(t *testing.T, fakeClients *FakeClientSet) *IdleablePayloadAssertion {
	return &IdleablePayloadAssertion{
		client:        fakeClients.AllNamespacesClient,
		t:             t,
		dynamicClient: fakeClients.DynamicClient,
		scalesClient:  fakeClients.ScalesClient,
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
	gvr := appsv1.SchemeGroupVersion.WithResource("deployments")
	a.getResourceFromDynamicClient(gvr, deployment.Namespace, deployment.Name, d)
	require.NotNil(a.t, d.Spec.Replicas)
	assert.Equal(a.t, int32(0), *d.Spec.Replicas, "namespace %s name %s", deployment.Namespace, deployment.Name)
	return a
}

func (a *IdleablePayloadAssertion) DeploymentScaledUp(deployment *appsv1.Deployment) *IdleablePayloadAssertion {
	d := &appsv1.Deployment{}
	gvr := appsv1.SchemeGroupVersion.WithResource("deployments")
	a.getResourceFromDynamicClient(gvr, deployment.Namespace, deployment.Name, d)
	require.NotNil(a.t, d.Spec.Replicas)
	assert.Equal(a.t, int32(3), *d.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) ScaleSubresourceScaledDown(obj *unstructured.Unstructured) *IdleablePayloadAssertion {
	actions := a.scalesClient.Actions()
	for _, action := range actions {
		if action.GetVerb() == "patch" {
			patchActionImpl := action.(k8sgotest.PatchActionImpl)
			if patchActionImpl.GetName() == obj.GetName() && patchActionImpl.GetNamespace() == obj.GetNamespace() {
				assert.JSONEq(a.t, `{"spec":{"replicas":0}}`, string(patchActionImpl.GetPatch()))
				return a
			}
		}
	}
	assert.Fail(a.t, fmt.Sprintf("object of the kind %s in namespace %s with the name %s wasn't scaled down, but it should be", obj.GetKind(), obj.GetNamespace(), obj.GetName()))
	return a
}

func (a *IdleablePayloadAssertion) ScaleSubresourceScaledUp(obj *unstructured.Unstructured) *IdleablePayloadAssertion {
	actions := a.scalesClient.Actions()
	for _, action := range actions {
		if action.GetVerb() == "patch" {
			patchActionImpl := action.(k8sgotest.PatchActionImpl)
			if patchActionImpl.GetName() == obj.GetName() && patchActionImpl.GetNamespace() == obj.GetNamespace() {
				assert.Fail(a.t, fmt.Sprintf("object of the kind %s in namespace %s with the name %s was scaled down, but it shouldn't be", obj.GetKind(), obj.GetNamespace(), obj.GetName()))
			}
		}
	}
	return a
}

func (a *IdleablePayloadAssertion) ReplicaSetScaledDown(replicaSet *appsv1.ReplicaSet) *IdleablePayloadAssertion {
	r := &appsv1.ReplicaSet{}
	gvr := appsv1.SchemeGroupVersion.WithResource("replicasets")
	a.getResourceFromDynamicClient(gvr, replicaSet.Namespace, replicaSet.Name, r)
	require.NotNil(a.t, r.Spec.Replicas)
	assert.Equal(a.t, int32(0), *r.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) getResourceFromDynamicClient(gvr schema.GroupVersionResource, namespace, name string, object runtime.Object) {
	unstructured, err := a.dynamicClient.
		Resource(gvr).
		Namespace(namespace).
		Get(context.TODO(), name, metav1.GetOptions{})
	require.NoError(a.t, err)

	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructured.Object, object)
	require.NoError(a.t, err)
}

func (a *IdleablePayloadAssertion) ReplicaSetScaledUp(replicaSet *appsv1.ReplicaSet) *IdleablePayloadAssertion {
	r := &appsv1.ReplicaSet{}
	gvr := appsv1.SchemeGroupVersion.WithResource("replicasets")
	a.getResourceFromDynamicClient(gvr, replicaSet.Namespace, replicaSet.Name, r)
	require.NotNil(a.t, r.Spec.Replicas)
	assert.Equal(a.t, int32(3), *r.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) DeploymentConfigScaledDown(deployment *openshiftappsv1.DeploymentConfig) *IdleablePayloadAssertion {
	d := &openshiftappsv1.DeploymentConfig{}
	gvr := openshiftappsv1.SchemeGroupVersion.WithResource("deploymentconfigs")
	a.getResourceFromDynamicClient(gvr, deployment.Namespace, deployment.Name, d)
	assert.Equal(a.t, int32(0), d.Spec.Replicas)
	assert.False(a.t, d.Spec.Paused) // DeploymentConfig should be unpaused when scaling down so that the replicas update can be rolled out
	return a
}

func (a *IdleablePayloadAssertion) DeploymentConfigScaledUp(deployment *openshiftappsv1.DeploymentConfig) *IdleablePayloadAssertion {
	d := &openshiftappsv1.DeploymentConfig{}
	gvr := openshiftappsv1.SchemeGroupVersion.WithResource("deploymentconfigs")
	a.getResourceFromDynamicClient(gvr, deployment.Namespace, deployment.Name, d)
	assert.Equal(a.t, int32(3), d.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) ReplicationControllerScaledDown(rc *corev1.ReplicationController) *IdleablePayloadAssertion {
	r := &corev1.ReplicationController{}
	gvr := corev1.SchemeGroupVersion.WithResource("replicationcontrollers")
	a.getResourceFromDynamicClient(gvr, rc.Namespace, rc.Name, r)
	require.NotNil(a.t, r.Spec.Replicas)
	assert.Equal(a.t, int32(0), *r.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) ReplicationControllerScaledUp(rc *corev1.ReplicationController) *IdleablePayloadAssertion {
	r := &corev1.ReplicationController{}
	gvr := corev1.SchemeGroupVersion.WithResource("replicationcontrollers")
	a.getResourceFromDynamicClient(gvr, rc.Namespace, rc.Name, r)
	require.NotNil(a.t, r.Spec.Replicas)
	assert.Equal(a.t, int32(3), *r.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) DaemonSetExists(daemonSet *appsv1.DaemonSet) *IdleablePayloadAssertion {
	gvr := appsv1.SchemeGroupVersion.WithResource("daemonsets")
	_, err := a.dynamicClient.
		Resource(gvr).
		Namespace(daemonSet.Namespace).
		Get(context.TODO(), daemonSet.Name, metav1.GetOptions{})
	require.NoError(a.t, err)
	return a
}

func (a *IdleablePayloadAssertion) DaemonSetDoesNotExist(daemonSet *appsv1.DaemonSet) *IdleablePayloadAssertion {
	gvr := appsv1.SchemeGroupVersion.WithResource("daemonsets")
	_, err := a.dynamicClient.
		Resource(gvr).
		Namespace(daemonSet.Namespace).
		Get(context.TODO(), daemonSet.Name, metav1.GetOptions{})
	require.Error(a.t, err, "daemonSet %s still exists", daemonSet.Name)
	assert.True(a.t, apierrors.IsNotFound(err))
	return a
}

func (a *IdleablePayloadAssertion) JobExists(job *batchv1.Job) *IdleablePayloadAssertion {
	gvr := batchv1.SchemeGroupVersion.WithResource("jobs")
	_, err := a.dynamicClient.
		Resource(gvr).
		Namespace(job.Namespace).
		Get(context.TODO(), job.Name, metav1.GetOptions{})
	require.NoError(a.t, err)
	return a
}

func (a *IdleablePayloadAssertion) JobDoesNotExist(job *batchv1.Job) *IdleablePayloadAssertion {
	gvr := batchv1.SchemeGroupVersion.WithResource("jobs")
	_, err := a.dynamicClient.
		Resource(gvr).
		Namespace(job.Namespace).
		Get(context.TODO(), job.Name, metav1.GetOptions{})
	require.Error(a.t, err, "job %s still exists", job.Name)
	assert.True(a.t, apierrors.IsNotFound(err))
	return a
}

func (a *IdleablePayloadAssertion) StatefulSetScaledDown(statefulSet *appsv1.StatefulSet) *IdleablePayloadAssertion {
	s := &appsv1.StatefulSet{}
	gvr := appsv1.SchemeGroupVersion.WithResource("statefulsets")
	a.getResourceFromDynamicClient(gvr, statefulSet.Namespace, statefulSet.Name, s)
	require.NotNil(a.t, s.Spec.Replicas)
	assert.Equal(a.t, int32(0), *s.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) StatefulSetScaledUp(statefulSet *appsv1.StatefulSet) *IdleablePayloadAssertion {
	s := &appsv1.StatefulSet{}
	gvr := appsv1.SchemeGroupVersion.WithResource("statefulsets")
	a.getResourceFromDynamicClient(gvr, statefulSet.Namespace, statefulSet.Name, s)
	require.NotNil(a.t, s.Spec.Replicas)
	assert.Equal(a.t, int32(3), *s.Spec.Replicas)
	return a
}

func (a *IdleablePayloadAssertion) VMRunning(vmStopCallCounter *int) *IdleablePayloadAssertion {
	assert.Empty(a.t, *vmStopCallCounter)
	return a
}

func (a *IdleablePayloadAssertion) VMStopped(vmStopCallCounter *int) *IdleablePayloadAssertion {
	assert.NotEmpty(a.t, *vmStopCallCounter)
	return a
}

var (
	aapGVR      = schema.GroupVersionResource{Group: "aap.ansible.com", Version: "v1alpha1", Resource: "ansibleautomationplatforms"}
	kubeFlowGVR = schema.GroupVersionResource{Group: "kubeflow.org", Version: "v1", Resource: "notebooks"}
)

func (a *IdleablePayloadAssertion) AAPIdled(aap *unstructured.Unstructured) *IdleablePayloadAssertion {
	actualAAP := &unstructured.Unstructured{}
	a.getResourceFromDynamicClient(aapGVR, aap.GetNamespace(), aap.GetName(), actualAAP)
	idled, _, err := unstructured.NestedBool(actualAAP.UnstructuredContent(), "spec", "idle_aap")
	require.NoError(a.t, err)
	assert.True(a.t, idled)
	return a
}

func (a *IdleablePayloadAssertion) AAPRunning(aap *unstructured.Unstructured) *IdleablePayloadAssertion {
	actualAAP := &unstructured.Unstructured{}
	a.getResourceFromDynamicClient(aapGVR, aap.GetNamespace(), aap.GetName(), actualAAP)
	idled, _, err := unstructured.NestedBool(actualAAP.UnstructuredContent(), "spec", "idle_aap")
	require.NoError(a.t, err)
	assert.False(a.t, idled, "AAP CR should not be idled")
	return a
}

func (a *IdleablePayloadAssertion) NotebookStopped(notebook *unstructured.Unstructured) *IdleablePayloadAssertion {
	// Check that the Notebook CR has the kubeflow-resource-stopped annotation
	notebookObj := &unstructured.Unstructured{}
	a.getResourceFromDynamicClient(kubeFlowGVR, notebook.GetNamespace(), notebook.GetName(), notebookObj)
	annotations := notebookObj.GetAnnotations()
	require.NotNil(a.t, annotations)
	assert.Contains(a.t, annotations, "kubeflow-resource-stopped", "Notebook should have kubeflow-resource-stopped annotation")
	return a
}

func (a *IdleablePayloadAssertion) NotebookRunning(notebook *unstructured.Unstructured) *IdleablePayloadAssertion {
	// Check that the Notebook CR does not have the kubeflow-resource-stopped annotation
	notebookObj := &unstructured.Unstructured{}
	a.getResourceFromDynamicClient(kubeFlowGVR, notebook.GetNamespace(), notebook.GetName(), notebookObj)
	annotations := notebookObj.GetAnnotations()
	assert.NotContains(a.t, annotations, "kubeflow-resource-stopped", "Notebook should not have kubeflow-resource-stopped annotation")
	return a
}
