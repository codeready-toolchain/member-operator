package templates_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/member-operator/pkg/templates"

	"github.com/go-logr/zapr"
	"github.com/openshift/api/template"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

func TestProcess(t *testing.T) {
	// given
	zapLog, err := zap.NewDevelopment()
	require.NoError(t, err)
	log := zapr.NewLogger(zapLog)
	scheme := runtime.NewScheme()
	utilruntime.Must(template.Install(scheme)) // see https://github.com/openshift/oc/blob/master/cmd/oc/oc.go#L77
	values := map[string]string{
		"PROJECT_NAME":            "foo",
		"PROJECT_REQUESTING_USER": "operator-sa",
		"COMMIT":                  "1a2b3c",
		"ADMIN_USER_NAME":         "developer",
	}
	// when
	objs, err := templates.Process(scheme, log, "basic", values)
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
}

func expectedRoleBinding() string  {
	return `{"apiVersion":"authorization.openshift.io/v1","kind":"RoleBinding","metadata":{"labels":{"provider":"codeready-toolchain","version":"1a2b3c"},"name":"user-admin","namespace":"foo"},"roleRef":{"name":"admin"},"subjects":[{"kind":"User","name":"developer"}]}
`}

func expectedProjectRequest() string  {
	return `{"apiVersion":"project.openshift.io/v1","kind":"ProjectRequest","metadata":{"annotations":{"openshift.io/description":"foo-user","openshift.io/display-name":"foo","openshift.io/requester":"operator-sa"},"labels":{"provider":"codeready-toolchain","version":"1a2b3c"},"name":"foo"}}
`
}
