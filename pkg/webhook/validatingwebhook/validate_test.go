package validatingwebhook

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	userv1 "github.com/openshift/api/user/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/admission/v1"
)

func TestHandleValidateBlocked(t *testing.T) {
	cl := createFakeClient("johnsmith", true)
	validator := &Validator{
		Client: cl,
	}
	// given
	ts := httptest.NewServer(http.HandlerFunc(validator.HandleValidate))
	defer ts.Close()

	// when
	resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(sandboxUserJSON))

	// then
	assert.NoError(t, err)
	body, err := ioutil.ReadAll(resp.Body)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()
	assert.NoError(t, err)
	verifyRequestBlocked(t, body)
}

func TestValidate(t *testing.T) {
	t.Run("sandbox user trying to create rolebinding is denied", func(t *testing.T) {
		cl := createFakeClient("johnsmith", true)
		// when
		response := validate(sandboxUserJSON, cl)
		// then
		verifyRequestBlocked(t, response)
	})
}

func TestValidateAllow(t *testing.T) {

	t.Run("SA or kubeadmin trying to create rolebinding is allowed", func(t *testing.T) {
		cl := createFakeClient("system:kubeadmin", false)
		// when user is kubeadmin
		response := validate(allowedUserJSON, cl)

		// then
		verifyRequestAllowed(t, response, "a68769e5-d817-4617-bec5-90efa2bad6g7")
	})

	t.Run("non sandbox user trying to create rolebinding is allowed", func(t *testing.T) {
		cl := createFakeClient("nonsandbox", false)
		// when
		response := validate(nonSandboxUserJSON, cl)
		// then
		verifyRequestAllowed(t, response, "a68769e5-d817-4617-bec5-90efa2bad6f7")
	})

}

func verifyRequestBlocked(t *testing.T, response []byte) {
	reviewResponse := toReviewResponse(t, response)
	assert.False(t, reviewResponse.Allowed)
	assert.NotEmpty(t, reviewResponse.Result)
	assert.Contains(t, reviewResponse.Result.Message, "trying to give access which is restricted")
	assert.Equal(t, "a68769e5-d817-4617-bec5-90efa2bad6f6", string(reviewResponse.UID))
}

func verifyRequestAllowed(t *testing.T, response []byte, UID string) {
	reviewResponse := toReviewResponse(t, response)
	assert.True(t, reviewResponse.Allowed)
	assert.Empty(t, reviewResponse.Result)
	assert.Equal(t, UID, string(reviewResponse.UID))
}

func toReviewResponse(t *testing.T, content []byte) *v1.AdmissionResponse {
	r := v1.AdmissionReview{}
	err := json.Unmarshal(content, &r)
	require.NoError(t, err)
	return r.Response
}

func createFakeClient(username string, sandboxUser bool) runtimeclient.Client {
	s := scheme.Scheme
	err := userv1.Install(s)
	if err != nil {
		return nil
	}
	testUser := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: username,
		},
	}
	if sandboxUser {
		testUser.Labels = map[string]string{
			toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
		}
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(testUser).Build()
}

var sandboxUserJSON = []byte(`{
	"kind": "AdmissionReview",
	"apiVersion": "admission.k8s.io/v1",
	"request": {
		"uid": "a68769e5-d817-4617-bec5-90efa2bad6f6",
		"kind": {
			"group": "",
			"version": "v1",
			"kind": "RoleBinding"
		},
		"resource": {
			"group": "",
			"version": "v1",
			"resource": "rolebindings"
		},
		"requestKind": {
			"group": "",
			"version": "v1",
			"kind": "RoleBinding"
		},
		"requestResource": {
			"group": "",
			"version": "v1",
			"resource": "rolebindings"
		},
		"name": "busybox1",
		"namespace": "johnsmith-dev",
		"operation": "CREATE",
		"userInfo": {
			"username": "johnsmith",
			"groups": [
				"system:authenticated"
			],
			"extra": {
				"scopes.authorization.openshift.io": [
					"user:full"
				]
			}
		},
		"object": {
			"kind": "RoleBinding",
			"apiVersion": "v1",
			"metadata": {
				"name": "busybox1",
				"namespace": "johnsmith-dev"
			},
			"subjects": [{
				"kind": "Group",
				"name": "system:serviceaccounts",
				"apiGroup": "rbac.authorization.k8s.io"
			}],
			"roleRef": {
				"kind": "Role",
				"apiGroup": "rbac.authorization.k8s.io",
				"name": "rbac-edit"
			}
		},
		"oldObject": null,
		"dryRun": false,
		"options": {
			"kind": "CreateOptions",
			"apiVersion": "meta.k8s.io/v1"
		}
	}
}`)

var nonSandboxUserJSON = []byte(`{
	"kind": "AdmissionReview",
	"apiVersion": "admission.k8s.io/v1",
	"request": {
		"uid": "a68769e5-d817-4617-bec5-90efa2bad6f7",
		"kind": {
			"group": "",
			"version": "v1",
			"kind": "RoleBinding"
		},
		"resource": {
			"group": "",
			"version": "v1",
			"resource": "rolebindings"
		},
		"requestKind": {
			"group": "",
			"version": "v1",
			"kind": "RoleBinding"
		},
		"requestResource": {
			"group": "",
			"version": "v1",
			"resource": "rolebindings"
		},
		"name": "busybox1",
		"namespace": "nonsandbox-test",
		"operation": "CREATE",
		"userInfo": {
			"username": "nonsandbox",
			"groups": [
				"system:authenticated"
			],
			"extra": {
				"scopes.authorization.openshift.io": [
					"user:full"
				]
			}
		},
		"object": {
			"kind": "RoleBinding",
			"apiVersion": "v1",
			"metadata": {
				"name": "busybox1",
				"namespace": "nonsandbox-test"
			},
			"subjects": [{
				"kind": "Group",
				"name": "system:serviceaccounts",
				"apiGroup": "rbac.authorization.k8s.io"
			}],
			"roleRef": {
				"kind": "Role",
				"apiGroup": "rbac.authorization.k8s.io",
				"name": "rbac-edit"
			}
		},
		"oldObject": null,
		"dryRun": false,
		"options": {
			"kind": "CreateOptions",
			"apiVersion": "meta.k8s.io/v1"
		}
	}
}`)

var allowedUserJSON = []byte(`{
	"kind": "AdmissionReview",
	"apiVersion": "admission.k8s.io/v1",
	"request": {
		"uid": "a68769e5-d817-4617-bec5-90efa2bad6g7",
		"kind": {
			"group": "",
			"version": "v1",
			"kind": "RoleBinding"
		},
		"resource": {
			"group": "",
			"version": "v1",
			"resource": "rolebindings"
		},
		"requestKind": {
			"group": "",
			"version": "v1",
			"kind": "RoleBinding"
		},
		"requestResource": {
			"group": "",
			"version": "v1",
			"resource": "rolebindings"
		},
		"name": "busybox1",
		"namespace": "johnsmith-dev",
		"operation": "CREATE",
		"userInfo": {
			"username": "system:kubeadmin",
			"groups": [
				"system:authenticated"
			],
			"extra": {
				"scopes.authorization.openshift.io": [
					"user:full"
				]
			}
		},
		"object": {
			"kind": "RoleBinding",
			"apiVersion": "v1",
			"metadata": {
				"name": "busybox1",
				"namespace": "johnsmith-dev"
			},
			"subjects": [{
				"kind": "Group",
				"name": "system:serviceaccounts",
				"apiGroup": "rbac.authorization.k8s.io"
			}],
			"roleRef": {
				"kind": "Role",
				"apiGroup": "rbac.authorization.k8s.io",
				"name": "rbac-edit"
			}
		},
		"oldObject": null,
		"dryRun": false,
		"options": {
			"kind": "CreateOptions",
			"apiVersion": "meta.k8s.io/v1"
		}
	}
}`)
