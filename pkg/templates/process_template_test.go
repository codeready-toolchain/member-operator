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
	require.IsType(t, &unstructured.Unstructured{}, objs[0].Object.GetObjectKind())
	projectRequest := objs[0].Object.GetObjectKind().(*unstructured.Unstructured)
	// check that all parameters have been set in the template object
	assert.Equal(t, "foo", projectRequest.GetAnnotations()["openshift.io/description"])
	assert.Equal(t, "foo", projectRequest.GetAnnotations()["openshift.io/display-name"])
	assert.Equal(t, "operator-sa", projectRequest.GetAnnotations()["openshift.io/requester"])
	assert.Equal(t, "1a2b3c", projectRequest.GetLabels()["version"])
	assert.Equal(t, "foo", projectRequest.GetName())
	// TODO: assert other objects
}
