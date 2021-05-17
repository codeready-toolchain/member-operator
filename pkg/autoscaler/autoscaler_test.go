package autoscaler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestGetTemplateObjects(t *testing.T) {
	// given
	s := setScheme(t)

	// when
	toolchainObjects, err := getTemplateObjects(s, test.MemberOperatorNs, 3)

	// then
	require.NoError(t, err)
	require.Len(t, toolchainObjects, 2)
	contains(t, toolchainObjects, priorityClass())
	contains(t, toolchainObjects, deployment(test.MemberOperatorNs, 3))
}

func TestDeploy(t *testing.T) {
	// given
	s := setScheme(t)
	master := node(31000, "node-role.kubernetes.io/master")
	infra := node(20000, "node-role.kubernetes.io/infra", "node-role.kubernetes.io/worker")
	worker := node(10300, "node-role.kubernetes.io/worker")

	t.Run("when unable to obtain allocatable memory of a worker node", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t, infra, master)

		// when
		err := Deploy(fakeClient, s, test.MemberOperatorNs, 0.8)

		// then
		assert.EqualError(t, err, "unable to obtain allocatable memory of a worker node")
	})

	t.Run("when created", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t, master, infra, worker)

		// when
		err := Deploy(fakeClient, s, test.MemberOperatorNs, 0.8)

		// then
		require.NoError(t, err)
		verifyAutoscalerDeployment(t, fakeClient, 8)
	})

	t.Run("when updated", func(t *testing.T) {
		// given
		prioClass := &schedulingv1.PriorityClass{}
		unmarshalObj(t, priorityClass(), prioClass)
		prioClass.Labels = map[string]string{}
		prioClass.Value = 100

		deploymentObj := &appsv1.Deployment{}
		unmarshalObj(t, deployment(test.MemberOperatorNs, 40), deploymentObj)
		deploymentObj.Spec.Template.Spec.Containers[0].Image = "some-dummy"

		fakeClient := test.NewFakeClient(t, prioClass, deploymentObj, master, infra, worker)

		// when
		err := Deploy(fakeClient, s, test.MemberOperatorNs, 0.5)

		// then
		require.NoError(t, err)
		verifyAutoscalerDeployment(t, fakeClient, 5)
	})

	t.Run("when creation fails", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t, master, infra, worker)
		fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
			return fmt.Errorf("some error")
		}

		// when
		err := Deploy(fakeClient, s, test.MemberOperatorNs, 1)

		// then
		assert.EqualError(t, err, "cannot deploy autoscaling buffer template: unable to create resource of kind: PriorityClass, version: v1: some error")
	})
}

func verifyAutoscalerDeployment(t *testing.T, fakeClient *test.FakeClient, bufferSizeGi int64) {
	expPrioClass := &schedulingv1.PriorityClass{}
	unmarshalObj(t, priorityClass(), expPrioClass)
	actualPrioClass := &schedulingv1.PriorityClass{}
	AssertObject(t, fakeClient, "", "member-operator-autoscaling-buffer", actualPrioClass, func() {
		assert.Equal(t, expPrioClass.Labels, actualPrioClass.Labels)
		assert.Equal(t, expPrioClass.Value, actualPrioClass.Value)
		assert.False(t, actualPrioClass.GlobalDefault)
		assert.Equal(t, expPrioClass.Description, actualPrioClass.Description)
	})

	expDeployment := &appsv1.Deployment{}
	unmarshalObj(t, deployment(test.MemberOperatorNs, bufferSizeGi), expDeployment)
	actualDeployment := &appsv1.Deployment{}
	AssertMemberObject(t, fakeClient, "autoscaling-buffer", actualDeployment, func() {
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

func contains(t *testing.T, objects []applycl.ToolchainObject, expected string) {
	expectedObject := getUnstructuredObject(t, expected)
	for _, obj := range objects {
		if reflect.DeepEqual(obj.GetRuntimeObject(), runtime.Object(expectedObject)) {
			return
		}
	}
	assert.Fail(t, "webhook template doesn't contain expected object", "Expected object: %s", expected)
}

func unmarshalObj(t *testing.T, content string, target runtime.Object) {
	err := json.Unmarshal([]byte(content), target)
	require.NoError(t, err)
}

func getUnstructuredObject(t *testing.T, content string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	unmarshalObj(t, content, obj)
	return obj
}

func priorityClass() string {
	return `{"apiVersion":"scheduling.k8s.io/v1","kind":"PriorityClass","metadata":{"name":"member-operator-autoscaling-buffer","labels":{"toolchain.dev.openshift.com/provider":"codeready-toolchain"}},"value":-100,"globalDefault":false,"description":"This priority class is to be used by the autoscaling buffer pod only"}`
}

func deployment(namespace string, bufferSizeGi int64) string {
	return fmt.Sprintf(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"autoscaling-buffer","namespace":"%s","labels":{"app":"autoscaling-buffer","toolchain.dev.openshift.com/provider":"codeready-toolchain"}},"spec":{"replicas":1,"selector":{"matchLabels":{"app":"autoscaling-buffer"}},"template":{"metadata":{"labels":{"app":"autoscaling-buffer"}},"spec":{"priorityClassName":"member-operator-autoscaling-buffer","terminationGracePeriodSeconds":0,"containers":[{"name":"autoscaling-buffer","image":"gcr.io/google_containers/pause-amd64:3.0","imagePullPolicy":"IfNotPresent","resources":{"requests":{"memory":"%dGi"},"limits":{"memory":"%dGi"}}}]}}}}`, namespace, bufferSizeGi, bufferSizeGi)
}

func node(allocatableMi int64, labels ...string) *corev1.Node {
	name, _ := uuid.NewV4()
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name.String(),
			Labels: map[string]string{},
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{"memory": *resource.NewQuantity(allocatableMi*1024*1024, resource.BinarySI)},
		},
	}
	for _, l := range labels {
		n.Labels[l] = "true"
	}

	return n
}
