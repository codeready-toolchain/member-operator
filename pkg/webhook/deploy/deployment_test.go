package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	rbac "k8s.io/api/rbac/v1"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	imgLoc = "quay.io/cool/member-operator-webhook:123"
	saname = "member-operator-webhook-sa"
)

func TestGetTemplateObjects(t *testing.T) {
	// given
	s := setScheme(t)

	// when
	objs, err := getTemplateObjects(s, test.MemberOperatorNs, imgLoc, []byte("super-cool-ca"))

	// then
	require.NoError(t, err)
	require.Len(t, objs, 8)
	contains(t, objs, priorityClass())
	contains(t, objs, service(test.MemberOperatorNs))
	contains(t, objs, deployment(test.MemberOperatorNs, saname, imgLoc))
	contains(t, objs, mutatingWebhookConfig(test.MemberOperatorNs, "c3VwZXItY29vbC1jYQ=="))
	contains(t, objs, validatingWebhookConfig(test.MemberOperatorNs, "c3VwZXItY29vbC1jYQ=="))
	contains(t, objs, serviceAccount(test.MemberOperatorNs))
	contains(t, objs, clusterRole())
	contains(t, objs, clusterRoleBinding(test.MemberOperatorNs))
}

func TestDeployWebhook(t *testing.T) {
	// given
	s := setScheme(t)
	t.Run("when created", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t)

		// when
		err := Webhook(fakeClient, s, test.MemberOperatorNs, imgLoc)

		// then
		require.NoError(t, err)
		verifyWebhookDeployment(t, fakeClient)
	})

	t.Run("when updated", func(t *testing.T) {
		// given
		prioClass := &schedulingv1.PriorityClass{}
		unmarshalObj(t, priorityClass(), prioClass)
		prioClass.Labels = map[string]string{}
		prioClass.Value = 10

		serviceObj := &v1.Service{}
		unmarshalObj(t, service(test.MemberOperatorNs), serviceObj)
		serviceObj.Spec.Ports[0].Port = 8080
		serviceObj.Spec.Selector = nil

		deploymentObj := &appsv1.Deployment{}
		unmarshalObj(t, deployment(test.MemberOperatorNs, saname, "quay.io/some/cool:unknown"), deploymentObj)
		deploymentObj.Spec.Template.Spec.Containers[0].Command = []string{"./some-dummy"}
		deploymentObj.Spec.Template.Spec.Containers[0].VolumeDevices = nil

		mutWbhConf := &admv1.MutatingWebhookConfiguration{}
		unmarshalObj(t, mutatingWebhookConfig(test.MemberOperatorNs, base64.StdEncoding.EncodeToString([]byte("cool-ca"))), mutWbhConf)
		mutWbhConf.Webhooks[0].Rules = nil

		fakeClient := test.NewFakeClient(t, prioClass, serviceObj, deploymentObj, mutWbhConf)

		// when
		err := Webhook(fakeClient, s, test.MemberOperatorNs, imgLoc)

		// then
		require.NoError(t, err)
		verifyWebhookDeployment(t, fakeClient)
	})

	t.Run("when creation fails", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t)
		fakeClient.MockCreate = func(ctx context.Context, obj runtimeclient.Object, opts ...runtimeclient.CreateOption) error {
			return fmt.Errorf("some error")
		}

		// when
		err := Webhook(fakeClient, s, test.MemberOperatorNs, imgLoc)

		// then
		require.Error(t, err)
	})
}

func verifyWebhookDeployment(t *testing.T, fakeClient *test.FakeClient) {
	expPrioClass := &schedulingv1.PriorityClass{}
	unmarshalObj(t, priorityClass(), expPrioClass)
	actualPrioClass := &schedulingv1.PriorityClass{}
	AssertObject(t, fakeClient, "", "sandbox-users-pods", actualPrioClass, func() {
		assert.Equal(t, expPrioClass.Labels, actualPrioClass.Labels)
		assert.Equal(t, expPrioClass.Value, actualPrioClass.Value)
		assert.False(t, actualPrioClass.GlobalDefault)
		assert.Equal(t, expPrioClass.Description, actualPrioClass.Description)
	})

	expService := &v1.Service{}
	unmarshalObj(t, service(test.MemberOperatorNs), expService)
	actualService := &v1.Service{}
	AssertObject(t, fakeClient, test.MemberOperatorNs, "member-operator-webhook", actualService, func() {
		assert.Equal(t, expService.Labels, actualService.Labels)
		assert.Equal(t, expService.Spec, actualService.Spec)
	})

	expServiceAcc := &v1.ServiceAccount{}
	unmarshalObj(t, serviceAccount(test.MemberOperatorNs), expServiceAcc)
	actualServiceAcc := &v1.ServiceAccount{}
	AssertObject(t, fakeClient, test.MemberOperatorNs, "member-operator-webhook-sa", actualServiceAcc, func() {
		assert.Equal(t, expServiceAcc.Namespace, actualServiceAcc.Namespace)
	})

	expClusterRole := &rbac.ClusterRole{}
	unmarshalObj(t, clusterRole(), expClusterRole)
	actualClusterRole := &rbac.ClusterRole{}
	AssertObject(t, fakeClient, "", "webhook-role", actualClusterRole, func() {
		assert.Equal(t, expClusterRole.Rules, actualClusterRole.Rules)
	})

	expClusterRb := &rbac.ClusterRoleBinding{}
	unmarshalObj(t, clusterRoleBinding(test.MemberOperatorNs), expClusterRb)
	actualClusterRb := &rbac.ClusterRoleBinding{}
	AssertObject(t, fakeClient, "", "webhook-rolebinding", actualClusterRb, func() {
		assert.Equal(t, expClusterRb.Subjects, actualClusterRb.Subjects)
		assert.Equal(t, expClusterRb.RoleRef, actualClusterRb.RoleRef)
	})

	expDeployment := &appsv1.Deployment{}
	unmarshalObj(t, deployment(test.MemberOperatorNs, saname, imgLoc), expDeployment)
	actualDeployment := &appsv1.Deployment{}
	AssertObject(t, fakeClient, test.MemberOperatorNs, "member-operator-webhook", actualDeployment, func() {
		assert.Equal(t, expDeployment.Labels, actualDeployment.Labels)
		assert.Equal(t, expDeployment.Spec, actualDeployment.Spec)
	})

	secret := &v1.Secret{}
	err := fakeClient.Get(context.TODO(), test.NamespacedName(test.MemberOperatorNs, "webhook-certs"), secret)
	require.NoError(t, err)

	expMutWbhConf := &admv1.MutatingWebhookConfiguration{}
	unmarshalObj(t, mutatingWebhookConfig(test.MemberOperatorNs, base64.StdEncoding.EncodeToString(secret.Data["ca-cert.pem"])), expMutWbhConf)
	actualMutWbhConf := &admv1.MutatingWebhookConfiguration{}
	AssertObject(t, fakeClient, "", "member-operator-webhook", actualMutWbhConf, func() {
		assert.Equal(t, expMutWbhConf.Labels, actualMutWbhConf.Labels)
		assert.Equal(t, expMutWbhConf.Webhooks, actualMutWbhConf.Webhooks)
	})

	expValWbhConf := &admv1.ValidatingWebhookConfiguration{}
	unmarshalObj(t, validatingWebhookConfig(test.MemberOperatorNs, base64.StdEncoding.EncodeToString(secret.Data["ca-cert.pem"])), expValWbhConf)
	actualValWbhConf := &admv1.ValidatingWebhookConfiguration{}
	AssertObject(t, fakeClient, "", "member-operator-validating-webhook", actualValWbhConf, func() {
		assert.Equal(t, expValWbhConf.Labels, actualValWbhConf.Labels)
		assert.Equal(t, expValWbhConf.Webhooks, actualValWbhConf.Webhooks)
	})
}

func setScheme(t *testing.T) *runtime.Scheme {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	return s
}

func contains(t *testing.T, objects []runtimeclient.Object, expected string) {
	expectedObject := getUnstructuredObject(t, expected)
	for _, obj := range objects {
		if reflect.DeepEqual(obj, runtime.Object(expectedObject)) {
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
	return `{"apiVersion":"scheduling.k8s.io/v1","kind":"PriorityClass","metadata":{"name":"sandbox-users-pods","labels":{"toolchain.dev.openshift.com/provider":"codeready-toolchain"}},"value":-3,"globalDefault":false,"description":"Priority class for pods in users' namespaces"}`
}

func service(namespace string) string {
	return fmt.Sprintf(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"member-operator-webhook","namespace":"%s","labels":{"app":"member-operator-webhook","toolchain.dev.openshift.com/provider":"codeready-toolchain"}},"spec":{"ports":[{"port":443,"targetPort":8443}],"selector":{"app":"member-operator-webhook"}}}`, namespace)
}

func deployment(namespace, sa string, image string) string {
	return fmt.Sprintf(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"member-operator-webhook","namespace":"%s","labels":{"app":"member-operator-webhook","toolchain.dev.openshift.com/provider":"codeready-toolchain"}},"spec":{"replicas":1,"selector":{"matchLabels":{"app":"member-operator-webhook"}},"template":{"metadata":{"name":"member-operator-webhook","labels":{"app":"member-operator-webhook"}},"spec":{"serviceAccountName": "%s","containers":[{"name":"mutator","image":"%s","command":["member-operator-webhook"],"imagePullPolicy":"IfNotPresent","resources":{"requests":{"cpu":"75m","memory":"128Mi"}},"volumeMounts":[{"name":"webhook-certs","mountPath":"/etc/webhook/certs","readOnly":true}]}],"volumes":[{"name":"webhook-certs","secret":{"secretName":"webhook-certs"}}]}}}}`, namespace, sa, image)
}

func mutatingWebhookConfig(namespace, caBundle string) string {
	return fmt.Sprintf(`{"apiVersion":"admissionregistration.k8s.io/v1","kind":"MutatingWebhookConfiguration","metadata":{"name":"member-operator-webhook","labels":{"app":"member-operator-webhook","toolchain.dev.openshift.com/provider":"codeready-toolchain"}},"webhooks":[{"name":"users.pods.webhook.sandbox","admissionReviewVersions":["v1"],"clientConfig":{"caBundle":"%[1]s","service":{"name":"member-operator-webhook","namespace":"%[2]s","path":"/mutate-users-pods","port":443}},"matchPolicy":"Equivalent","rules":[{"operations":["CREATE"],"apiGroups":[""],"apiVersions":["v1"],"resources":["pods"],"scope":"Namespaced"}],"sideEffects":"None","timeoutSeconds":5,"reinvocationPolicy":"Never","failurePolicy":"Ignore","namespaceSelector":{"matchLabels":{"toolchain.dev.openshift.com/provider":"codeready-toolchain"}}},{"name":"users.virtualmachines.webhook.sandbox","admissionReviewVersions":["v1"],"clientConfig":{"caBundle":"%[1]s","service":{"name":"member-operator-webhook","namespace":"%[2]s","path":"/mutate-virtual-machines","port":443}},"matchPolicy":"Equivalent","rules":[{"operations":["CREATE"],"apiGroups":["kubevirt.io"],"apiVersions":["v1"],"resources":["virtualmachines"],"scope":"Namespaced"}],"sideEffects":"None","timeoutSeconds":5,"reinvocationPolicy":"Never","failurePolicy":"Fail","namespaceSelector":{"matchLabels":{"toolchain.dev.openshift.com/provider":"codeready-toolchain"}}}]}`, caBundle, namespace)
}

func validatingWebhookConfig(namespace, caBundle string) string {
	return fmt.Sprintf(`{"apiVersion":"admissionregistration.k8s.io/v1","kind":"ValidatingWebhookConfiguration","metadata":{"labels":{"app":"member-operator-webhook","toolchain.dev.openshift.com/provider":"codeready-toolchain"},"name":"member-operator-validating-webhook"},"webhooks":[{"admissionReviewVersions":["v1"],"clientConfig":{"caBundle":"%[1]s","service":{"name":"member-operator-webhook","namespace":"%[2]s","path":"/validate-users-rolebindings","port":443}},"failurePolicy":"Ignore","matchPolicy":"Equivalent","name":"users.rolebindings.webhook.sandbox","namespaceSelector":{"matchLabels":{"toolchain.dev.openshift.com/provider":"codeready-toolchain"}},"reinvocationPolicy":"Never","rules":[{"apiGroups":["rbac.authorization.k8s.io","authorization.openshift.io"],"apiVersions":["v1"],"operations":["CREATE","UPDATE"],"resources":["rolebindings"],"scope":"Namespaced"}],"sideEffects":"None","timeoutSeconds":5},{"admissionReviewVersions":["v1"],"clientConfig":{"caBundle":"%[1]s","service":{"name":"member-operator-webhook","namespace":"%[2]s","path":"/validate-users-checlusters","port":443}},"failurePolicy":"Fail","matchPolicy":"Equivalent","name":"users.checlusters.webhook.sandbox","namespaceSelector":{"matchLabels":{"toolchain.dev.openshift.com/provider":"codeready-toolchain"}},"reinvocationPolicy":"Never","rules":[{"apiGroups":["org.eclipse.che"],"apiVersions":["v2"],"operations":["CREATE"],"resources":["checlusters"],"scope":"Namespaced"}],"sideEffects":"None","timeoutSeconds":5},{"admissionReviewVersions":["v1"],"clientConfig":{"caBundle":"%[1]s","service":{"name":"member-operator-webhook","namespace":"%[2]s","path":"/validate-spacebindingrequests","port":443}},"failurePolicy":"Fail","matchPolicy":"Equivalent","name":"users.spacebindingrequests.webhook.sandbox","namespaceSelector":{"matchLabels":{"toolchain.dev.openshift.com/provider":"codeready-toolchain"}},"reinvocationPolicy":"Never","rules":[{"apiGroups":["toolchain.dev.openshift.com"],"apiVersions":["v1alpha1"],"operations":["CREATE", "UPDATE"],"resources":["spacebindingrequests"],"scope":"Namespaced"}],"sideEffects":"None","timeoutSeconds":5}]}`, caBundle, namespace)
}

func serviceAccount(namespace string) string {
	return fmt.Sprintf(`{"apiVersion": "v1","kind": "ServiceAccount", "metadata":{"name": "member-operator-webhook-sa", "namespace": "%s"}}`, namespace)
}

func clusterRole() string {
	return `{"apiVersion": "rbac.authorization.k8s.io/v1","kind": "ClusterRole","metadata": {"creationTimestamp": null,"name": "webhook-role"}, "rules": [{"apiGroups": ["user.openshift.io"],"resources": ["identities","useridentitymappings","users"],"verbs": ["get","list","watch"]},{"apiGroups": ["toolchain.dev.openshift.com"],"resources": ["spacebindingrequests"],"verbs": ["get","list","watch"]},{"apiGroups": ["kubevirt.io"],"resources": ["virtualmachines"],"verbs": ["get","list","watch"]}]}`
}

func clusterRoleBinding(namespace string) string {
	return fmt.Sprintf(`{"apiVersion": "rbac.authorization.k8s.io/v1","kind": "ClusterRoleBinding", "metadata": {"name": "webhook-rolebinding"},"roleRef": {"apiGroup": "rbac.authorization.k8s.io","kind": "ClusterRole","name": "webhook-role"},"subjects": [{"kind": "ServiceAccount","name": "member-operator-webhook-sa","namespace": "%s"}]}`, namespace)
}
