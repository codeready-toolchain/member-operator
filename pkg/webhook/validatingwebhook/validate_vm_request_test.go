package validatingwebhook

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"text/template"

	"github.com/codeready-toolchain/member-operator/pkg/webhook/validatingwebhook/test"

	userv1 "github.com/openshift/api/user/v1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHandleValidateVMAdmissionRequestBlocked(t *testing.T) {
	v := newVMRequestValidator(t)
	// given
	ts := httptest.NewServer(http.HandlerFunc(v.HandleValidate))
	defer ts.Close()

	t.Run("sandbox user trying to update a VM resource with RunStrategy other than 'Manual' is denied", func(t *testing.T) {
		// when
		resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"UPDATE", "johnsmith"}, createVMWithRunStrategyJSONTmpl("Always"))))

		// then
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		defer func() {
			require.NoError(t, resp.Body.Close())
		}()
		require.NoError(t, err)
		test.VerifyRequestBlocked(t, body, "this is a Dev Sandbox enforced restriction. Only 'Manual' RunStrategy is permitted", "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

	t.Run("sandbox user trying to update a VM resource with RunStrategy 'Manual' is allowed", func(t *testing.T) {
		// when
		resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"UPDATE", "johnsmith"}, createVMWithRunStrategyJSONTmpl("Manual"))))

		// then
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		defer func() {
			require.NoError(t, resp.Body.Close())
		}()
		require.NoError(t, err)
		test.VerifyRequestAllowed(t, body, "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})

	t.Run("sandbox user trying to update a VM resource without RunStrategy is allowed", func(t *testing.T) { // note: RunStrategy is actually required by OpenShift Virtualization >=4.18 but the sandbox webhook doesn't need to require it
		// when
		resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"UPDATE", "johnsmith"}, createVMWithoutRunStrategyJSONTmpl)))

		// then
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		defer func() {
			require.NoError(t, resp.Body.Close())
		}()
		require.NoError(t, err)

		test.VerifyRequestAllowed(t, body, "b6ae2ab4-782b-11ee-b962-0242ac120002")
	})
}

func newVMRequestValidator(t *testing.T) *VMRequestValidator {
	s := scheme.Scheme
	err := userv1.Install(s)
	require.NoError(t, err)
	testUser := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "johnsmith",
		},
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

func createVMWithRunStrategyJSONTmpl(runStrategy string) string {
	return fmt.Sprintf(`{
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
                "runStrategy": "%s"
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
}`, runStrategy)
}

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
