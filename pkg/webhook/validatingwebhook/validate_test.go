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
	verifyRequestBlocked(t, body, "please create a rolebinding for a specific user or service account to avoid this error", "a68769e5-d817-4617-bec5-90efa2bad6f6")
}

func TestValidate(t *testing.T) {
	t.Run("sandbox user trying to create rolebinding is denied", func(t *testing.T) {
		cl := createFakeClient("johnsmith", true)
		// when
		response := validate(sandboxUserJSON, cl)
		// then
		verifyRequestBlocked(t, response, "please create a rolebinding for a specific user or service account to avoid this error", "a68769e5-d817-4617-bec5-90efa2bad6f6")
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

	t.Run("unable to find the requesting user, allow request", func(t *testing.T) {
		cl := createFakeClient("random-user", true)
		// when
		response := validate(sandboxUserJSON, cl)
		// then
		verifyRequestAllowed(t, response, "a68769e5-d817-4617-bec5-90efa2bad6f6")
	})

	t.Run("sandbox user creating a rolebinding for a specific user is allowed", func(t *testing.T) {
		cl := createFakeClient("laracroft", true)
		//when
		response := validate(allowedRbJSON, cl)
		//then
		verifyRequestAllowed(t, response, "a68769e5-d817-4617-bec5-90efa2bad8g8")
	})

}

func TestValidateFailsOnInvalidJson(t *testing.T) {
	// given
	rawJSON := []byte(`something wrong !`)

	// when
	response := validate(rawJSON, nil)

	// then
	verifyRequestBlocked(t, response, "cannot unmarshal string into Go value of type struct", "")
}

func TestValidateFailsOnInvalidObjectJson(t *testing.T) {
	// when
	response := validate(incorrectRequestObjectJSON, nil)

	// then
	verifyRequestBlocked(t, response, "unable to unmarshal object or object is not a rolebinding", "a68769e5-d817-4617-bec5-90efa2bad6f8")
}

func verifyRequestBlocked(t *testing.T, response []byte, msg string, UID string) {
	reviewResponse := toReviewResponse(t, response)
	assert.False(t, reviewResponse.Allowed)
	assert.NotEmpty(t, reviewResponse.Result)
	assert.Contains(t, reviewResponse.Result.Message, msg)
	assert.Equal(t, UID, string(reviewResponse.UID))
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

var incorrectRequestObjectJSON = []byte(`{
	"kind": "AdmissionReview",
	"apiVersion": "admission.k8s.io/v1",
	"request": {
		"uid": "a68769e5-d817-4617-bec5-90efa2bad6f8",
		"name": "busybox1",
		"namespace": "johnsmith-dev",
		"object": {
			"kind": "asbasbf",
			"apiVersion": "v1",
			"metadata": {
				"name": "busybox1",
				"namespace": "johnsmith-dev"
			}
		}
	}
}`)
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

var allowedRbJSON = []byte(`{
	"kind": "AdmissionReview",
	"apiVersion": "admission.k8s.io/v1",
	"request": {
		"uid": "a68769e5-d817-4617-bec5-90efa2bad8g8",
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
		"namespace": "laracroft-dev",
		"operation": "CREATE",
		"userInfo": {
			"username": "laracroft",
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
				"namespace": "laracroft-dev"
			},
			"subjects": [{
				"kind": "User",
				"name": "crt-admin",
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
