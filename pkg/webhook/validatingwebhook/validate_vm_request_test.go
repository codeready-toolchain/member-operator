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

func TestHandleValidateVMAdmissionRequestBlocked(t *testing.T) {
	v := newVMRequestValidator(t, "johnsmith", true)
	// given
	ts := httptest.NewServer(http.HandlerFunc(v.HandleValidate))
	defer ts.Close()

	// when
	resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"CREATE", "johnsmith"}, createVMWithRunStrategyJSONTmpl)))

	// then
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()
	require.NoError(t, err)
	test.VerifyRequestBlocked(t, body, "this is a Dev Sandbox enforced restriction. Configuring RunStrategy is not allowed", "b6ae2ab4-782b-11ee-b962-0242ac120002")
}

func TestValidateVMAdmissionRequest(t *testing.T) {
	t.Run("sandbox user trying to create a VM resource with RunStrategy is denied", func(t *testing.T) {
		// given
		v := newVMRequestValidator(t, "johnsmith", true)
		req := newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"CREATE", "johnsmith"}, createVMWithRunStrategyJSONTmpl)

		// when
		response := v.validate(context.TODO(), req)

		// then
		test.VerifyRequestBlocked(t, response, "this is a Dev Sandbox enforced restriction. Configuring RunStrategy is not allowed", "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

	t.Run("sandbox user trying to update a VM resource with RunStrategy is denied", func(t *testing.T) {
		// given
		v := newVMRequestValidator(t, "johnsmith", true)
		req := newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"UPDATE", "johnsmith"}, createVMWithRunStrategyJSONTmpl)

		// when
		response := v.validate(context.TODO(), req)

		// then
		test.VerifyRequestBlocked(t, response, "this is a Dev Sandbox enforced restriction. Configuring RunStrategy is not allowed", "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

	t.Run("non-sandbox user trying to create a VM resource with RunStrategy is allowed", func(t *testing.T) {
		// given
		v := newVMRequestValidator(t, "other", false)
		req := newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"CREATE", "other"}, createVMWithRunStrategyJSONTmpl)

		// when
		response := v.validate(context.TODO(), req)

		// then
		test.VerifyRequestAllowed(t, response, "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

	t.Run("non-sandbox user trying to update a VM resource with RunStrategy is allowed", func(t *testing.T) {
		// given
		v := newVMRequestValidator(t, "other", false)
		req := newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"UPDATE", "other"}, createVMWithRunStrategyJSONTmpl)

		// when
		response := v.validate(context.TODO(), req)

		// then
		test.VerifyRequestAllowed(t, response, "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

	t.Run("sandbox user trying to create a VM resource without RunStrategy is allowed", func(t *testing.T) {
		// given
		v := newVMRequestValidator(t, "johnsmith", true)
		req := newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"CREATE", "johnsmith"}, createVMWithoutRunStrategyJSONTmpl)

		// when
		response := v.validate(context.TODO(), req)

		// then
		test.VerifyRequestAllowed(t, response, "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

	t.Run("sandbox user trying to update a VM resource without RunStrategy is allowed", func(t *testing.T) {
		// given
		v := newVMRequestValidator(t, "johnsmith", true)
		req := newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"UPDATE", "johnsmith"}, createVMWithoutRunStrategyJSONTmpl)

		// when
		response := v.validate(context.TODO(), req)

		// then
		test.VerifyRequestAllowed(t, response, "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

}

func newVMRequestValidator(t *testing.T, username string, isSandboxUser bool) *VMRequestValidator {
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
	return &VMRequestValidator{
		Client: cl,
	}

}

func newCreateVMAdmissionRequest(t *testing.T, params VMAdmReviewTmplParams, tmplJSON string) []byte {
	tmpl, err := template.New("admission request").Parse(tmplJSON)
	require.NoError(t, err)
	req := &bytes.Buffer{}
	err = tmpl.Execute(req, params)
	require.NoError(t, err)
	return req.Bytes()
}

type VMAdmReviewTmplParams struct {
	ReqType  string
	Username string
}

var createVMWithRunStrategyJSONTmpl = `{
    "kind": "AdmissionReview",
    "apiVersion": "admission.k8s.io/v1",
    "request": {
        "uid": "b6ae2ab4-782b-11ee-b962-0242ac120002",
        "kind": {
            "group": "kubevirt.io",
            "version": "v1",
            "kind": "VirtualMachine"
        },
        "resource": {
            "group": "kubevirt.io",
            "version": "v1",
            "resource": "virtualmachines"
        },
        "requestKind": {
            "group": "kubevirt.io",
            "version": "v1",
            "kind": "VirtualMachine"
        },
        "requestResource": {
            "group": "kubevirt.io",
            "version": "v1",
            "resource": "virtualmachines"
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
            "apiVersion": "kubevirt.io",
            "kind": "VirtualMachine",
            "metadata": {
                "name": "{{.Username}}",
                "namespace": "{{.Username}}-dev"
            },
            "spec": {
                "runStrategy": "Always"
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

var createVMWithoutRunStrategyJSONTmpl = `{
    "kind": "AdmissionReview",
    "apiVersion": "admission.k8s.io/v1",
    "request": {
        "uid": "b6ae2ab4-782b-11ee-b962-0242ac120002",
        "kind": {
            "group": "kubevirt.io",
            "version": "v1",
            "kind": "VirtualMachine"
        },
        "resource": {
            "group": "kubevirt.io",
            "version": "v1",
            "resource": "virtualmachines"
        },
        "requestKind": {
            "group": "kubevirt.io",
            "version": "v1",
            "kind": "VirtualMachine"
        },
        "requestResource": {
            "group": "kubevirt.io",
            "version": "v1",
            "resource": "virtualmachines"
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
            "apiVersion": "kubevirt.io",
            "kind": "VirtualMachine",
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
