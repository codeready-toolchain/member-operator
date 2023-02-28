package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	rbac "k8s.io/api/rbac/v1"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	imgLoc = "quay.io/cool/member-operator-console-plugin:123"
	saname = "member-operator-console-plugin"
)

func TestGetTemplateObjects(t *testing.T) {
	// given
	s := setScheme(t)

	// when
	objs, err := getTemplateObjects(s, test.MemberOperatorNs, imgLoc)

	// then
	require.NoError(t, err)
	require.Len(t, objs, 5)
	contains(t, objs, service(test.MemberOperatorNs))
	contains(t, objs, deployment(test.MemberOperatorNs, saname, imgLoc))
	contains(t, objs, serviceAccount(test.MemberOperatorNs))
	contains(t, objs, role())
	contains(t, objs, roleBinding(test.MemberOperatorNs))
}

func TestDeploy(t *testing.T) {
	// given
	s := setScheme(t)
	t.Run("when created", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t)

		// when
		err := ConsolePlugin(fakeClient, s, test.MemberOperatorNs, imgLoc)

		// then
		require.NoError(t, err)
		verifyDeployment(t, fakeClient)
	})

	t.Run("when updated", func(t *testing.T) {
		// given
		serviceObj := &v1.Service{}
		unmarshalObj(t, service(test.MemberOperatorNs), serviceObj)
		serviceObj.Spec.Ports[0].Port = 8080
		serviceObj.Spec.Selector = nil

		deploymentObj := &appsv1.Deployment{}
		unmarshalObj(t, deployment(test.MemberOperatorNs, saname, "quay.io/some/cool:unknown"), deploymentObj)
		deploymentObj.Spec.Template.Spec.Containers[0].Command = []string{"./some-dummy"}
		deploymentObj.Spec.Template.Spec.Containers[0].VolumeDevices = nil

		fakeClient := test.NewFakeClient(t, serviceObj, deploymentObj)

		// when
		err := ConsolePlugin(fakeClient, s, test.MemberOperatorNs, imgLoc)

		// then
		require.NoError(t, err)
		verifyDeployment(t, fakeClient)
	})

	t.Run("when creation fails", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t)
		fakeClient.MockCreate = func(ctx context.Context, obj runtimeclient.Object, opts ...runtimeclient.CreateOption) error {
			return fmt.Errorf("some error")
		}

		// when
		err := ConsolePlugin(fakeClient, s, test.MemberOperatorNs, imgLoc)

		// then
		require.Error(t, err)
	})
}

func verifyDeployment(t *testing.T, fakeClient *test.FakeClient) {
	expService := &v1.Service{}
	unmarshalObj(t, service(test.MemberOperatorNs), expService)
	actualService := &v1.Service{}
	AssertObject(t, fakeClient, test.MemberOperatorNs, "member-operator-console-plugin", actualService, func() {
		assert.Equal(t, expService.Labels, actualService.Labels)
		assert.Equal(t, expService.Spec, actualService.Spec)
	})

	expServiceAcc := &v1.ServiceAccount{}
	unmarshalObj(t, serviceAccount(test.MemberOperatorNs), expServiceAcc)
	actualServiceAcc := &v1.ServiceAccount{}
	AssertObject(t, fakeClient, test.MemberOperatorNs, "member-operator-console-plugin", actualServiceAcc, func() {
		assert.Equal(t, expServiceAcc.Namespace, actualServiceAcc.Namespace)
	})

	expRole := &rbac.Role{}
	unmarshalObj(t, role(), expRole)
	actualRole := &rbac.Role{}
	AssertObject(t, fakeClient, test.MemberOperatorNs, "member-operator-console-plugin", actualRole, func() {
		assert.Equal(t, expRole.Rules, actualRole.Rules)
	})

	expRb := &rbac.RoleBinding{}
	unmarshalObj(t, roleBinding(test.MemberOperatorNs), expRb)
	actualRb := &rbac.RoleBinding{}
	AssertObject(t, fakeClient, test.MemberOperatorNs, "member-operator-console-plugin", actualRb, func() {
		assert.Equal(t, expRb.Subjects, actualRb.Subjects)
		assert.Equal(t, expRb.RoleRef, actualRb.RoleRef)
	})

	expDeployment := &appsv1.Deployment{}
	unmarshalObj(t, deployment(test.MemberOperatorNs, saname, imgLoc), expDeployment)
	actualDeployment := &appsv1.Deployment{}
	AssertObject(t, fakeClient, test.MemberOperatorNs, "member-operator-console-plugin", actualDeployment, func() {
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

func contains(t *testing.T, objects []runtimeclient.Object, expected string) {
	strObjects := make([]string, len(objects))
	for i, obj := range objects {
		str, err := json.Marshal(obj)
		require.NoError(t, err)
		strObjects[i] = string(str)
	}

	assert.Contains(t, strObjects, expected, "console plugin template doesn't contain expected object")
}

func unmarshalObj(t *testing.T, content string, target runtime.Object) {
	err := json.Unmarshal([]byte(content), target)
	require.NoError(t, err)
}

func service(namespace string) string {
	return fmt.Sprintf(`{"apiVersion":"v1","kind":"Service","metadata":{"labels":{"provider":"codeready-toolchain","run":"member-operator-console-plugin"},"name":"member-operator-console-plugin","namespace":"%s"},"spec":{"ports":[{"name":"8080","port":80,"protocol":"TCP","targetPort":8080}],"selector":{"run":"member-operator-console-plugin"},"sessionAffinity":null,"type":"ClusterIP"}}`, namespace)
}

func deployment(namespace, sa string, image string) string {
	return fmt.Sprintf(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"labels":{"provider":"codeready-toolchain"},"name":"member-operator-console-plugin","namespace":"%s"},"spec":{"replicas":3,"selector":{"matchLabels":{"name":"member-operator-console-plugin"}},"template":{"metadata":{"labels":{"name":"member-operator-console-plugin","run":"member-operator-console-plugin"}},"spec":{"containers":[{"command":["member-operator-console-plugin"],"env":[{"name":"WATCH_NAMESPACE","value":"toolchain-member-operator"}],"image":"%s","imagePullPolicy":"IfNotPresent","livenessProbe":{"failureThreshold":3,"httpGet":{"path":"/api/v1/health","port":8080,"scheme":"HTTP"},"initialDelaySeconds":1,"periodSeconds":10,"successThreshold":1,"timeoutSeconds":1},"name":"member-operator-console-plugin","ports":[{"containerPort":8080}],"readinessProbe":{"failureThreshold":30,"httpGet":{"path":"/api/v1/health","port":8080,"scheme":"HTTP"},"initialDelaySeconds":1,"periodSeconds":1,"successThreshold":1,"timeoutSeconds":1},"resources":{"requests":{"cpu":"50m","memory":"10M"}},"startupProbe":{"failureThreshold":180,"httpGet":{"path":"/api/v1/health","port":8080,"scheme":"HTTP"},"initialDelaySeconds":1,"periodSeconds":1,"successThreshold":1,"timeoutSeconds":1}}],"serviceAccountName":"%s"}}}}`, namespace, image, sa)
}

func serviceAccount(namespace string) string {
	return fmt.Sprintf(`{"apiVersion":"v1","kind":"ServiceAccount","metadata":{"labels":{"provider":"codeready-toolchain"},"name":"member-operator-console-plugin","namespace":"%s"}}`, namespace)
}

func role() string {
	return `{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"Role","metadata":{"labels":{"provider":"codeready-toolchain"},"name":"member-operator-console-plugin","namespace":"toolchain-member-operator"},"rules":[{"apiGroups":["toolchain.dev.openshift.com"],"resources":["memberoperatorconfigs"],"verbs":["get","list","watch"]}]}`
}

func roleBinding(namespace string) string {
	return fmt.Sprintf(`{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"RoleBinding","metadata":{"labels":{"provider":"codeready-toolchain"},"name":"member-operator-console-plugin","namespace":"%s"},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"Role","name":"member-operator-console-plugin"},"subjects":[{"kind":"ServiceAccount","name":"member-operator-console-plugin"}]}`, namespace)
}
