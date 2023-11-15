package validatingwebhook

import (
	"bytes"
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

func TestHandleValidateK8sImagePullerAdmissionRequest(t *testing.T) {
	// given
	v := newK8sImagePullerValidator(t)
	ts := httptest.NewServer(http.HandlerFunc(v.HandleValidate))
	defer ts.Close()

	t.Run("sandbox user trying to create a KubernetesImagePuller resource is denied", func(t *testing.T) {
		// given
		req := newCreateK8sImagePullerAdmissionRequest(t, "johnsmith")

		// when
		response := v.validate(req)

		// then
		test.VerifyRequestBlocked(t, response, "this is a Dev Sandbox enforced restriction. you are trying to create a KubernetesImagePuller resource, which is not allowed", "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

	t.Run("crtadmin user trying to create a KubernetesImagePuller resource is allowed", func(t *testing.T) {
		// given
		req := newCreateK8sImagePullerAdmissionRequest(t, "johnsmith-crtadmin")

		// when
		response := v.validate(req)

		// then
		test.VerifyRequestAllowed(t, response, "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

}

func newK8sImagePullerValidator(t *testing.T) *K8sImagePullerRequestValidator {
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
	return &K8sImagePullerRequestValidator{
		Client: cl,
	}

}

func newCreateK8sImagePullerAdmissionRequest(t *testing.T, username string) []byte {
	tmpl, err := template.New("admission request").Parse(createK8sImagePullerJSONTmpl)
	require.NoError(t, err)
	req := &bytes.Buffer{}
	err = tmpl.Execute(req, username)
	require.NoError(t, err)
	return req.Bytes()
}

var createK8sImagePullerJSONTmpl = `{
    "kind": "AdmissionReview",
    "apiVersion": "admission.k8s.io/v1",
    "request": {
        "uid": "b6ae2ab4-782b-11ee-b962-0242ac120002",
        "kind": {
            "group": "che.eclipse.org",
            "version": "v1alpha1",
            "kind": "KubernetesImagePuller"
        },
        "resource": {
            "group": "che.eclipse.org",
            "version": "v1alpha1",
            "resource": "kubernetesimagepullers"
        },
        "requestKind": {
            "group": "che.eclipse.org",
            "version": "v1alpha1",
            "kind": "KubernetesImagePuller"
        },
        "requestResource": {
            "group": "che.eclipse.org",
            "version": "v1alpha1",
            "resource": "kubernetesimagepullers"
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
            "apiVersion": "che.eclipse.org/v1alpha1",
            "kind": "KubernetesImagePuller",
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
