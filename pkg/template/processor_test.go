package template_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/member-operator/pkg/template"

	"github.com/go-logr/zapr"
	apitemplate "github.com/openshift/api/template/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"fmt"
	testtemplates "github.com/codeready-toolchain/member-operator/test/templates"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"context"
	authv1 "github.com/openshift/api/authorization/v1"
	projectv1 "github.com/openshift/api/project/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/satori/go.uuid"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
)

func TestProcess(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		// given
		scheme := runtime.NewScheme()
		utilruntime.Must(apitemplate.Install(scheme)) // see https://github.com/openshift/oc/blob/master/cmd/oc/oc.go#L77
		values := map[string]string{
			"PROJECT_NAME": "foo",
			"COMMIT":       "1a2b3c",
			"USER_NAME":    "developer",
		}

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, scheme, logger(t))

		// when
		objs, err := p.Process(templateContent(t), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)

		// project request
		projectKind := objs[0].Object.GetObjectKind()
		require.IsType(t, &unstructured.Unstructured{}, projectKind)
		projectRequest := projectKind.(*unstructured.Unstructured)
		prJson, err := projectRequest.MarshalJSON()
		require.NoError(t, err, "failed to marshal json for projectrequest")
		assert.Equal(t, expectedProjectRequest(), string(prJson))

		// role binding
		rbKind := objs[1].Object.GetObjectKind()
		require.IsType(t, &unstructured.Unstructured{}, rbKind)
		roleBinding := rbKind.(*unstructured.Unstructured)
		rbJson, err := roleBinding.MarshalJSON()
		require.NoError(t, err, "failed to marshal json for rolebinding")
		assert.Equal(t, expectedRoleBinding(), string(rbJson))
	})

	t.Run("random extra param - fail", func(t *testing.T) {

		// given
		scheme := runtime.NewScheme()
		utilruntime.Must(apitemplate.Install(scheme)) // see https://github.com/openshift/oc/blob/master/cmd/oc/oc.go#L77

		random := uuid.NewV4().String()
		values := map[string]string{
			"PROJECT_NAME": "foo",
			"COMMIT":       "1a2b3c",
			"USER_NAME":    "developer",
			random:         random,
		}
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, scheme, logger(t))

		// when
		objs, err := p.Process(templateContent(t), values)

		// then
		require.EqualError(t, err, fmt.Sprintf("unknown parameter name \"%s\"", random))
		require.Len(t, objs, 0)
	})
}

func TestProcessAndApply(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		s := scheme.Scheme
		err := apis.AddToScheme(s)
		require.NoError(t, err)
		utilruntime.Must(apitemplate.Install(s)) // see https://github.com/openshift/oc/blob/master/cmd/oc/oc.go#L77

		templateContent := templateContent(t)
		pn := uuid.NewV4().String()
		u := uuid.NewV4().String()
		c := uuid.NewV4().String()
		values := map[string]string{
			"PROJECT_NAME": pn,
			"COMMIT":       c,
			"USER_NAME":    u,
		}

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s, logger(t))

		err = p.ProcessAndApply(templateContent, values)
		require.NoError(t, err)
		// check that the project request was created
		verifyProjectRequest(t, cl, pn)
		verifyRoleBinding(t, cl, pn)
	})

	t.Run("delete role binding and apply template", func(t *testing.T) {
		// given
		s := scheme.Scheme
		err := apis.AddToScheme(s)
		require.NoError(t, err)
		utilruntime.Must(apitemplate.Install(s)) // see https://github.com/openshift/oc/blob/master/cmd/oc/oc.go#L77

		templateContent := templateContent(t)
		pn := uuid.NewV4().String()
		u := uuid.NewV4().String()
		c := uuid.NewV4().String()
		values := map[string]string{
			"PROJECT_NAME": pn,
			"COMMIT":       c,
			"USER_NAME":    u,
		}

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s, logger(t))

		err = p.ProcessAndApply(templateContent, values)
		require.NoError(t, err)

		verifyProjectRequest(t, cl, pn)
		verifyRoleBinding(t, cl, pn)

		// delete rolebinding to create scenario, of rolebinding failed to create in first run.
		rb, err := roleBinding(cl, pn)
		require.NoError(t, err)

		err = cl.Delete(context.TODO(), rb)
		require.NoError(t, err)

		// apply the same templates
		err = p.ProcessAndApply(templateContent, values)
		require.NoError(t, err)

		verifyProjectRequest(t, cl, pn)
		verifyRoleBinding(t, cl, pn)
	})
}


func templateContent(t *testing.T) []byte {
	templateContent, err := GetTemplateContent("basic-tier-template.yml")
	require.NoError(t, err)

	return templateContent
}

func logger(t *testing.T) logr.Logger {
	zapLog, err := zap.NewDevelopment()
	require.NoError(t, err)
	return zapr.NewLogger(zapLog)
}

func expectedRoleBinding() string {
	return `{"apiVersion":"authorization.openshift.io/v1","kind":"RoleBinding","metadata":{"labels":{"provider":"codeready-toolchain","version":"1a2b3c"},"name":"foo-admin","namespace":"foo"},"roleRef":{"name":"admin"},"subjects":[{"kind":"User","name":"developer"}]}
`
}

func expectedProjectRequest() string {
	return `{"apiVersion":"project.openshift.io/v1","kind":"ProjectRequest","metadata":{"annotations":{"openshift.io/description":"foo-user","openshift.io/display-name":"foo","openshift.io/requester":"developer"},"labels":{"provider":"codeready-toolchain","version":"1a2b3c"},"name":"foo"}}
`
}

func GetTemplateContent(tmplName string) ([]byte, error) {
	return testtemplates.Asset("test/templates/" + tmplName)
}

func verifyProjectRequest(t *testing.T, c client.Client, projectRequestName string) {
	// check that the project request was created
	pr, err := projectRequest(c, projectRequestName)

	require.NoError(t, err)
	assert.NotNil(t, pr)
}

func verifyRoleBinding(t *testing.T, c client.Client, ns string) {
	// check that the rolebinding is created in the namespace
	// (the fake client just records the request but does not perform any consistency check)
	rb, err := roleBinding(c, ns)

	require.NoError(t, err)
	assert.NotNil(t, rb)
}

func projectRequest(c client.Client, projectRequestName string) (*projectv1.ProjectRequest, error) {
	var pr projectv1.ProjectRequest
	err := c.Get(context.TODO(), types.NamespacedName{Name: projectRequestName, Namespace: ""}, &pr) // project request is cluster-scoped

	return &pr, err
}

func roleBinding(c client.Client, ns string) (*authv1.RoleBinding, error) {
	var rb authv1.RoleBinding
	err := c.Get(context.TODO(), types.NamespacedName{
		Namespace: ns,
		Name:      fmt.Sprintf("%s-admin", ns),
	}, &rb)

	return &rb, err
}
