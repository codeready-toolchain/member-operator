package validatingwebhook

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"text/template"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/validatingwebhook/test"
	commontest "github.com/codeready-toolchain/toolchain-common/pkg/test"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHandleValidateSpaceBindingRequest(t *testing.T) {
	// given
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	requestUID := "f0b30997-3ac0-49f2-baf4-6eafd123564c"

	t.Run("update SpaceBindingRequest", func(t *testing.T) {
		// given
		johnSBR := newSBR("john-sbr", "jane-tenant", "john", "contributor")
		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(johnSBR).Build()
		v := newSpaceBindingRequestValidator(cl)
		ts := httptest.NewServer(http.HandlerFunc(v.HandleValidate))
		defer ts.Close()

		t.Run("MUR field update is denied in SpaceBindingRequest", func(t *testing.T) {
			// given
			req := newCreateSBRAdmissionRequest(t, sbrTemplate{
				RequestUID:       requestUID,
				Operation:        "UPDATE",
				Username:         "jane",
				MasterUserRecord: "newMur", // try to update the MUR field
				SpaceRole:        "admin",
			})

			// when
			response := v.validate(req)

			// then
			// it should not be allowed to update MUR field
			test.VerifyRequestBlocked(t, response, "SpaceBindingRequest.MasterUserRecord field cannot be changed. Consider deleting and creating a new SpaceBindingRequest resource", requestUID)
		})

		t.Run("SpaceRole field update is allowed in SpaceBindingRequest", func(t *testing.T) {
			// given
			req := newCreateSBRAdmissionRequest(t, sbrTemplate{
				RequestUID:       "f0b30997-3ac0-49f2-baf4-6eafd123564c",
				Operation:        "UPDATE",
				Username:         "jane",
				MasterUserRecord: "john",
				SpaceRole:        "maintainer", // update the SpaceRole field
			})

			// when
			response := v.validate(req)

			// then
			// it should be allowed to update SpaceRole field
			test.VerifyRequestAllowed(t, response, requestUID)
		})
	})

	t.Run("creating a new SpaceBindingRequest is always allowed", func(t *testing.T) {
		// given
		cl := fake.NewClientBuilder().WithScheme(s).Build()
		v := newSpaceBindingRequestValidator(cl)
		ts := httptest.NewServer(http.HandlerFunc(v.HandleValidate))
		defer ts.Close()

		// given
		req := newCreateSBRAdmissionRequest(t, sbrTemplate{
			RequestUID:       requestUID,
			Operation:        "CREATE",
			Username:         "jane",
			MasterUserRecord: "newMur",
			SpaceRole:        "admin",
		})

		// when
		response := v.validate(req)

		// then
		// it should not be allowed to create new SBR
		test.VerifyRequestAllowed(t, response, requestUID)
	})
}

func TestValidateSpaceBindingRequestFailsOnInvalidJson(t *testing.T) {
	// given
	v := &SpaceBindingRequestValidator{}

	t.Run("object is not spacebindingrequest", func(t *testing.T) {
		// when
		response := v.validate(test.IncorrectRequestObjectJSON)

		// then
		test.VerifyRequestBlocked(t, response, "unable to unmarshal object or object is not a spacebindingrequest", "a68769e5-d817-4617-bec5-90efa2bad6f8")
	})

	t.Run("json is invalid", func(t *testing.T) {
		// when
		rawJSON := []byte(`something wrong !`)
		response := v.validate(rawJSON)

		// then
		test.VerifyRequestBlocked(t, response, "cannot unmarshal string into Go value of type struct", "")
	})
}

func TestValidateSpaceBindingRequestFailsOnGettingSBR(t *testing.T) {
	// given
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	cl := commontest.NewFakeClient(t)
	cl.MockGet = mockGetSpaceBindingRequestFail(cl)
	v := newSpaceBindingRequestValidator(cl)
	ts := httptest.NewServer(http.HandlerFunc(v.HandleValidate))
	defer ts.Close()
	req := newCreateSBRAdmissionRequest(t, sbrTemplate{
		RequestUID:       "xvadsfasdf",
		Operation:        "UPDATE",
		Username:         "jane",
		MasterUserRecord: "newMur",
		SpaceRole:        "admin",
	})

	// when
	response := v.validate(req)

	// then
	test.VerifyRequestBlocked(t, response, "unable to validate the SpaceBindingRequest. SpaceBindingRequest.Name: john-sbr: mock error", "xvadsfasdf")
}

func newSpaceBindingRequestValidator(cl runtimeClient.Client) *SpaceBindingRequestValidator {
	return &SpaceBindingRequestValidator{
		Client: cl,
	}
}

func newSBR(name, namespace, mur, spaceRole string) *toolchainv1alpha1.SpaceBindingRequest {
	return &toolchainv1alpha1.SpaceBindingRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: toolchainv1alpha1.SpaceBindingRequestSpec{
			MasterUserRecord: mur,
			SpaceRole:        spaceRole,
		},
	}
}

func newCreateSBRAdmissionRequest(t *testing.T, sbrTemplate sbrTemplate) []byte {
	tmpl, err := template.New("admission request").Parse(createSpaceBindingRequestJSONTmpl)
	require.NoError(t, err)
	req := &bytes.Buffer{}
	err = tmpl.Execute(req, sbrTemplate)
	require.NoError(t, err)
	return req.Bytes()
}

func mockGetSpaceBindingRequestFail(cl runtimeClient.Client) func(ctx context.Context, key runtimeClient.ObjectKey, obj runtimeClient.Object, opts ...runtimeClient.GetOption) error {
	return func(ctx context.Context, key runtimeClient.ObjectKey, obj runtimeClient.Object, opts ...runtimeClient.GetOption) error {
		if _, ok := obj.(*toolchainv1alpha1.SpaceBindingRequest); ok {
			return fmt.Errorf("mock error")
		}
		return cl.Get(ctx, key, obj, opts...)
	}
}

type sbrTemplate struct {
	RequestUID       string
	Operation        string
	Username         string
	MasterUserRecord string
	SpaceRole        string
}

var createSpaceBindingRequestJSONTmpl = `{
    "kind": "AdmissionReview",
    "apiVersion": "admission.k8s.io/v1",
    "request": {
        "uid": "{{ .RequestUID }}",
        "kind": {
            "group": "toolchain.dev.openshift.com",
            "version": "v1alpha1",
            "kind": "SpaceBindingRequest"
        },
        "resource": {
            "group": "toolchain.dev.openshift.com",
            "version": "v1alpha1",
            "resource": "spacebindingrequests"
        },
        "requestKind": {
            "group": "toolchain.dev.openshift.com",
            "version": "v1alpha1",
            "kind": "SpaceBindingRequest"
        },
        "requestResource": {
            "group": "toolchain.dev.openshift.com",
            "version": "v1alpha1",
            "resource": "spacebindingrequests"
        },
        "name": "john-sbr",
        "namespace": "jane-tenant",
        "operation": " {{ .Operation }}",
        "userInfo": {
            "username": "{{ .Username }}",
            "groups": [
                "system:authenticated"
            ]
        },
        "object": {
            "apiVersion": "toolchain.dev.openshift.com/v1alpha1",
            "kind": "SpaceBindingRequest",
            "metadata": {
                "name": "john-sbr",
                "namespace": "jane-tenant"
            },
            "spec": {
                "masterUserRecord": "{{ .MasterUserRecord }}",
                "spaceRole": "{{ .SpaceRole }}"
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
