package validatingwebhook

import (
	"bytes"
	"context"
	"io"
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

func TestHandleValidateSSPAdmissionRequestBlocked(t *testing.T) {
	v := newSSPRequestValidator(t, "johnsmith", true)
	// given
	ts := httptest.NewServer(http.HandlerFunc(v.HandleValidate))
	defer ts.Close()

	// when
	resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateSSPAdmissionRequest(t, SSPAdmReviewTmplParams{"CREATE", "johnsmith"})))

	// then
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()
	require.NoError(t, err)
	test.VerifyRequestBlocked(t, body, "this is a Dev Sandbox enforced restriction. you are trying to create a SSP resource, which is not allowed", "b6ae2ab4-782b-11ee-b962-0242ac120002")
}

func TestValidateSSPAdmissionRequest(t *testing.T) {
	t.Run("sandbox user trying to create a SSP resource is denied", func(t *testing.T) {
		// given
		v := newSSPRequestValidator(t, "johnsmith", true)
		req := newCreateSSPAdmissionRequest(t, SSPAdmReviewTmplParams{"CREATE", "johnsmith"})

		// when
		response := v.validate(context.TODO(), req)

		// then
		test.VerifyRequestBlocked(t, response, "this is a Dev Sandbox enforced restriction. you are trying to create a SSP resource, which is not allowed", "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

	t.Run("sandbox user trying to update a SSP resource is denied", func(t *testing.T) {
		// given
		v := newSSPRequestValidator(t, "johnsmith", true)
		req := newCreateSSPAdmissionRequest(t, SSPAdmReviewTmplParams{"UPDATE", "johnsmith"})

		// when
		response := v.validate(context.TODO(), req)

		// then
		test.VerifyRequestBlocked(t, response, "this is a Dev Sandbox enforced restriction. you are trying to create a SSP resource, which is not allowed", "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

	t.Run("non-sandbox user trying to create a SSP resource is allowed", func(t *testing.T) {
		// given
		v := newSSPRequestValidator(t, "other", false)
		req := newCreateSSPAdmissionRequest(t, SSPAdmReviewTmplParams{"CREATE", "other"})

		// when
		response := v.validate(context.TODO(), req)

		// then
		test.VerifyRequestAllowed(t, response, "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

	t.Run("non-sandbox user trying to update a SSP resource is allowed", func(t *testing.T) {
		// given
		v := newSSPRequestValidator(t, "other", false)
		req := newCreateSSPAdmissionRequest(t, SSPAdmReviewTmplParams{"UPDATE", "other"})

		// when
		response := v.validate(context.TODO(), req)

		// then
		test.VerifyRequestAllowed(t, response, "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})
}

func newSSPRequestValidator(t *testing.T, username string, isSandboxUser bool) *SSPRequestValidator {
	s := scheme.Scheme
	err := userv1.Install(s)
	require.NoError(t, err)
	testUser := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: username,
		},
	}

	if isSandboxUser {
		testUser.Labels = map[string]string{
			toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
		}
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(testUser).Build()
	return &SSPRequestValidator{
		Client: cl,
	}
}

func newCreateSSPAdmissionRequest(t *testing.T, params SSPAdmReviewTmplParams) []byte {
	tmpl, err := template.New("admission request").Parse(createSSPJSONTmpl)
	require.NoError(t, err)
	req := &bytes.Buffer{}
	err = tmpl.Execute(req, params)
	require.NoError(t, err)
	return req.Bytes()
}

type SSPAdmReviewTmplParams struct {
	ReqType  string
	Username string
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
        "namespace": "{{.Username}}-dev",
        "operation": "{{.ReqType}}",
        "userInfo": {
            "username": "{{.Username}}",
            "groups": [
                "system:authenticated"
            ]
        },
        "object": {
            "apiVersion": "ssp.kubevirt.io",
            "kind": "SSP",
            "metadata": {
                "name": "{{.Username}}",
                "namespace": "{{.Username}}-dev"
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
