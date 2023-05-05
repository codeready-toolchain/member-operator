package autoscaler

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

func TestGetTemplateObjects(t *testing.T) {
	// given
	s := setScheme(t)

	// when
	objs, err := getTemplateObjects(s, test.MemberOperatorNs, "8Gi", 3)

	// then
	require.NoError(t, err)
	require.Len(t, objs, 2)
	priorityClassEquals(t, priorityClass(), objs[0])
	deploymentEquals(t, deployment("8Gi", 3), objs[1])
}

func TestDeploy(t *testing.T) {
	// given
	s := setScheme(t)

	t.Run("when created", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t)

		// when
		err := Deploy(fakeClient, s, test.MemberOperatorNs, "500Mi", 10)

		// then
		require.NoError(t, err)
		verifyAutoscalerDeployment(t, fakeClient, "500Mi", 10)
	})

	t.Run("when updated", func(t *testing.T) {
		// given
		priorityClass := unmarshalPriorityClass(t, priorityClass())
		priorityClass.Labels = map[string]string{}
		priorityClass.Value = 100

		deployment := unmarshalDeployment(t, deployment("1Gi", 5))
		deployment.Spec.Template.Spec.Containers[0].Image = "some-dummy"

		fakeClient := test.NewFakeClient(t, priorityClass, deployment)

		// when
		err := Deploy(fakeClient, s, test.MemberOperatorNs, "7Gi", 1)

		// then
		require.NoError(t, err)
		verifyAutoscalerDeployment(t, fakeClient, "7Gi", 1)
	})

	t.Run("when creation fails", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t)
		fakeClient.MockCreate = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
			return fmt.Errorf("some error")
		}

		// when
		err := Deploy(fakeClient, s, test.MemberOperatorNs, "7Gi", 3)

		// then
		assert.EqualError(t, err, "cannot deploy autoscaling buffer template: unable to create resource of kind: PriorityClass, version: v1: some error")
	})
}

func TestDelete(t *testing.T) {
	// given
	s := setScheme(t)
	prioClass := unmarshalPriorityClass(t, priorityClass())
	dm := unmarshalDeployment(t, deployment("100Mi", 3))
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: test.MemberOperatorNs}}

	t.Run("when previously deployed", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t, namespace, prioClass, dm)
		AssertThatCluster(t, fakeClient).HasResource(prioClass.Name, &schedulingv1.PriorityClass{})
		AssertThatNamespace(t, test.MemberOperatorNs, fakeClient).HasResource(dm.Name, &appsv1.Deployment{})

		// when
		deleted, err := Delete(fakeClient, s, test.MemberOperatorNs)

		// then
		require.NoError(t, err)
		assert.True(t, deleted)
		AssertThatCluster(t, fakeClient).HasNoResource(prioClass.Name, &schedulingv1.PriorityClass{})
		AssertThatNamespace(t, test.MemberOperatorNs, fakeClient).HasNoResource(dm.Name, &appsv1.Deployment{})
	})

	t.Run("when previously not deployed", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t)

		// when
		deleted, err := Delete(fakeClient, s, test.MemberOperatorNs)

		// then
		require.NoError(t, err)
		assert.False(t, deleted)
	})

	t.Run("when loading previously deployed objects fails", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t)
		fakeClient.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			return fmt.Errorf("some error")
		}

		// when
		deleted, err := Delete(fakeClient, s, test.MemberOperatorNs)

		// then
		assert.EqualError(t, err, "cannot get autoscaling buffer object: some error")
		assert.False(t, deleted)
	})

	t.Run("when deleting previously deployed objects fails", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t, namespace, prioClass, dm)
		fakeClient.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("some error")
		}

		// when
		deleted, err := Delete(fakeClient, s, test.MemberOperatorNs)

		// then
		assert.EqualError(t, err, "cannot delete autoscaling buffer object: some error")
		assert.False(t, deleted)
	})
}

func verifyAutoscalerDeployment(t *testing.T, fakeClient *test.FakeClient, memory string, replicas int) {
	expPrioClass := unmarshalPriorityClass(t, priorityClass())
	actualPrioClass := &schedulingv1.PriorityClass{}
	AssertObject(t, fakeClient, "", "member-operator-autoscaling-buffer", actualPrioClass, func() {
		assert.Equal(t, expPrioClass.Labels, actualPrioClass.Labels)
		assert.Equal(t, expPrioClass.Value, actualPrioClass.Value)
		assert.False(t, actualPrioClass.GlobalDefault)
		assert.Equal(t, expPrioClass.Description, actualPrioClass.Description)
	})

	expDeployment := unmarshalDeployment(t, deployment(memory, replicas))
	actualDeployment := &appsv1.Deployment{}
	AssertObject(t, fakeClient, test.MemberOperatorNs, "autoscaling-buffer", actualDeployment, func() {
		assert.Equal(t, expDeployment.Labels, actualDeployment.Labels)
		assert.Equal(t, expDeployment.Spec, actualDeployment.Spec)
	})
}

func setScheme(t *testing.T) *runtime.Scheme {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	return s
}

func priorityClassEquals(t *testing.T, expected string, actual runtime.Object) {
	expectedPriorityClass := unmarshalPriorityClass(t, expected)
	actualPriorityClass := objectToPriorityClass(t, actual)

	assert.Equal(t, expectedPriorityClass, actualPriorityClass)
}

func deploymentEquals(t *testing.T, expected string, actual runtime.Object) {
	expectedDeployment := unmarshalDeployment(t, expected)
	actualDeployment := objectToDeployment(t, actual)

	assert.Equal(t, expectedDeployment, actualDeployment)
}

func unmarshalDeployment(t *testing.T, content string) *appsv1.Deployment {
	obj := &appsv1.Deployment{}
	err := json.Unmarshal([]byte(content), obj)
	require.NoError(t, err)
	return obj
}

func unmarshalPriorityClass(t *testing.T, content string) *schedulingv1.PriorityClass {
	obj := &schedulingv1.PriorityClass{}
	err := json.Unmarshal([]byte(content), obj)
	require.NoError(t, err)
	return obj
}

func objectToDeployment(t *testing.T, obj runtime.Object) *appsv1.Deployment {
	content := marshalRuntimeObject(t, obj)
	return unmarshalDeployment(t, content)
}

func objectToPriorityClass(t *testing.T, obj runtime.Object) *schedulingv1.PriorityClass {
	content := marshalRuntimeObject(t, obj)
	return unmarshalPriorityClass(t, content)
}

func marshalRuntimeObject(t *testing.T, obj runtime.Object) string {
	result, err := json.Marshal(obj)
	require.NoError(t, err)
	return string(result)
}

func priorityClass() string {
	return `{"apiVersion":"scheduling.k8s.io/v1","kind":"PriorityClass","metadata":{"name":"member-operator-autoscaling-buffer","labels":{"toolchain.dev.openshift.com/provider":"codeready-toolchain"}},"value":-5,"globalDefault":false,"description":"This priority class is to be used by the autoscaling buffer pod only"}`
}

func deployment(memory string, replicas int) string {
	return fmt.Sprintf(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"autoscaling-buffer","namespace":"%s","labels":{"app":"autoscaling-buffer","toolchain.dev.openshift.com/provider":"codeready-toolchain"}},"spec":{"replicas":%d,"selector":{"matchLabels":{"app":"autoscaling-buffer"}},"template":{"metadata":{"labels":{"app":"autoscaling-buffer"}},"spec":{"priorityClassName":"member-operator-autoscaling-buffer","terminationGracePeriodSeconds":0,"containers":[{"name":"autoscaling-buffer","image":"gcr.io/google_containers/pause-amd64:3.2","imagePullPolicy":"IfNotPresent","resources":{"requests":{"memory":"%s"},"limits":{"memory":"%s"}}}]}}}}`, test.MemberOperatorNs, replicas, memory, memory)
}
