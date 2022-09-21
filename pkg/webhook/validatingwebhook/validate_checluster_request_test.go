package validatingwebhook

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"text/template"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"

	userv1 "github.com/openshift/api/user/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHandleValidateCheClusterAdmissionRequest(t *testing.T) {
	// given
	v := newCheClusterValidator(t)
	ts := httptest.NewServer(http.HandlerFunc(v.HandleValidate))
	defer ts.Close()

	t.Run("sandbox user trying to create a CheCluster resource is denied", func(t *testing.T) {
		// given
		req := newCreateCheClusterAdmissionRequest(t, "johnsmith")

		// when
		response := v.validate(req)

		// then
		verifyRequestDenied(t, response, "this is a Dev Sandbox enforced restriction. you are trying to create a CheCluster resource, which is not allowed", "f0b30997-3ac0-49f2-baf4-6eafd123564c")
	})

	t.Run("crtadmin user trying to create a CheCluster resource is allowed", func(t *testing.T) {
		// given
		req := newCreateCheClusterAdmissionRequest(t, "johnsmith-crtadmin")

		// when
		response := v.validate(req)

		// then
		verifyRequestAllowed(t, response, "f0b30997-3ac0-49f2-baf4-6eafd123564c")
	})

}

func verifyRequestDenied(t *testing.T, response []byte, msg string, UID string) {
	reviewResponse := toReviewResponse(t, response)
	assert.False(t, reviewResponse.Allowed)
	assert.NotEmpty(t, reviewResponse.Result)
	assert.Contains(t, reviewResponse.Result.Message, msg)
	assert.Equal(t, UID, string(reviewResponse.UID))
}

func newCheClusterValidator(t *testing.T) *CheClusterRequestValidator {
	s := scheme.Scheme
	err := userv1.Install(s)
	require.NoError(t, err)
	johnsmithUser := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "johnsmith",
			Labels: map[string]string{
				toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
			},
		},
	}
	johnsmithAdmin := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "johnsmith-crtadmin",
			Labels: map[string]string{
				"provider": "sandbox-sre",
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(johnsmithUser, johnsmithAdmin).Build()
	return &CheClusterRequestValidator{
		Client: cl,
	}

}

func newCreateCheClusterAdmissionRequest(t *testing.T, username string) []byte {
	tmpl, err := template.New("admission request").Parse(createCheClusterJSONTmpl)
	require.NoError(t, err)
	req := &bytes.Buffer{}
	err = tmpl.Execute(req, username)
	require.NoError(t, err)
	return req.Bytes()
}

var createCheClusterJSONTmpl = `{
    "kind": "AdmissionReview",
    "apiVersion": "admission.k8s.io/v1",
    "request": {
        "uid": "f0b30997-3ac0-49f2-baf4-6eafd123564c",
        "kind": {
            "group": "org.eclipse.che",
            "version": "v2",
            "kind": "CheCluster"
        },
        "resource": {
            "group": "org.eclipse.che",
            "version": "v2",
            "resource": "checlusters"
        },
        "requestKind": {
            "group": "org.eclipse.che",
            "version": "v2",
            "kind": "CheCluster"
        },
        "requestResource": {
            "group": "org.eclipse.che",
            "version": "v2",
            "resource": "checlusters"
        },
        "name": "test",
        "namespace": "johnsmith-dev",
        "operation": "CREATE",
        "userInfo": {
            "username": "{{ . }}",
            "groups": [
                "system:authenticated"
            ]
        },
        "object": {
            "apiVersion": "org.eclipse.che/v2",
            "kind": "CheCluster",
            "metadata": {
                "name": "test",
                "namespace": "paul-dev"
            }
        },
        "oldObject": null,
        "dryRun": false,
        "options": {
            "kind": "CreateOptions",
            "apiVersion": "meta.k8s.io/v1",
            "fieldManager": "kubectl-client-side-apply",
            "fieldValidation": "Ignore"
        }
    }
}`
