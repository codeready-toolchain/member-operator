package validatingwebhook

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"text/template"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/validatingwebhook/test"

	userv1 "github.com/openshift/api/user/v1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHandleValidateSSPAdmissionRequest(t *testing.T) {
	// given
	v := newSSPRequestValidator(t)
	ts := httptest.NewServer(http.HandlerFunc(v.HandleValidate))
	defer ts.Close()

	t.Run("sandbox user trying to create a SSP resource is denied", func(t *testing.T) {
		// given
		req := newCreateSSPAdmissionRequest(t, "johnsmith")

		// when
		response := v.validate(context.TODO(), req)

		// then
		test.VerifyRequestBlocked(t, response, "this is a Dev Sandbox enforced restriction. you are trying to create a SSP resource, which is not allowed", "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

}

func newSSPRequestValidator(t *testing.T) *SSPRequestValidator {
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
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(johnsmithUser).Build()
	return &SSPRequestValidator{
		Client: cl,
	}

}

func newCreateSSPAdmissionRequest(t *testing.T, username string) []byte {
	tmpl, err := template.New("admission request").Parse(createSSPJSONTmpl)
	require.NoError(t, err)
	req := &bytes.Buffer{}
	err = tmpl.Execute(req, username)
	require.NoError(t, err)
	return req.Bytes()
}

var createSSPJSONTmpl = `{
    "kind": "AdmissionReview",
    "apiVersion": "admission.k8s.io/v1",
    "request": {
        "uid": "b6ae2ab4-782b-11ee-b962-0242ac120002",
        "kind": {
            "group": "ssp.kubevirt.io",
            "version": "v1alpha1",
            "kind": "SSP"
        },
        "resource": {
            "group": "ssp.kubevirt.io",
            "version": "v1alpha1",
            "resource": "ssps"
        },
        "requestKind": {
            "group": "ssp.kubevirt.io",
            "version": "v1alpha1",
            "kind": "SSP"
        },
        "requestResource": {
            "group": "ssp.kubevirt.io",
            "version": "v1alpha1",
            "resource": "ssps"
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
            "apiVersion": "ssp.kubevirt.io",
            "kind": "SSP",
            "metadata": {
                "name": "johnsmith",
                "namespace": "johnsmith-dev"
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
