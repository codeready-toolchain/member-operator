package validatingwebhook

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"

	userv1 "github.com/openshift/api/user/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHandleValidateRolebBndingAdmissionRequestBlocked(t *testing.T) {
	v := newRoleBindingRequestValidator(t, "johnsmith", true)
	// given
	ts := httptest.NewServer(http.HandlerFunc(v.HandleValidate))
	defer ts.Close()

	// when
	resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(sandboxUserForAllServiceAccountsJSON))

	// then
	assert.NoError(t, err)
	body, err := ioutil.ReadAll(resp.Body)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()
	assert.NoError(t, err)
	verifyRequestBlocked(t, body, "please create a rolebinding for a specific user or service account to avoid this error", "a68769e5-d817-4617-bec5-90efa2bad6f6")
}

func TestValidateRolebBndingAdmissionRequest(t *testing.T) {
	t.Run("sandbox user trying to create rolebinding for all serviceaccounts is denied", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "johnsmith", true)
		// when
		response := v.validate(sandboxUserForAllServiceAccountsJSON)
		// then
		verifyRequestBlocked(t, response, "please create a rolebinding for a specific user or service account to avoid this error", "a68769e5-d817-4617-bec5-90efa2bad6f6")
	})

	t.Run("sandbox user trying to create rolebinding for all serviceaccounts: is denied", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "johnsmith", true)
		// when
		response := v.validate(sandboxUserForAllServiceAccountsJSON2)
		// then
		verifyRequestBlocked(t, response, "please create a rolebinding for a specific user or service account to avoid this error", "a68769e5-d817-4617-bec5-90efa2bad7g8")
	})

	t.Run("sandbox user trying to create rolebinding for all authenticated users is denied", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "johnsmith", true)
		// when
		response := v.validate(sandboxUserForAllUsersJSON)
		// then
		verifyRequestBlocked(t, response, "please create a rolebinding for a specific user or service account to avoid this error", "a68769e5-d817-4617-bec5-90efa2bad8k8")
	})

	t.Run("sandbox user trying to create rolebinding for all authenticated: is denied", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "johnsmith", true)
		// when
		response := v.validate(sandboxUserForAllUsersJSON2)
		// then
		verifyRequestBlocked(t, response, "please create a rolebinding for a specific user or service account to avoid this error", "a68769e5-d817-4617-bec5-90efa2bad9l9")
	})
}

func TestValidateRolebBndingAdmissionRequestAllowed(t *testing.T) {

	t.Run("SA or kubeadmin trying to create rolebinding is allowed", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "system:kubeadmin", false)
		// when user is kubeadmin
		response := v.validate(allowedUserJSON)

		// then
		verifyRequestAllowed(t, response, "a68769e5-d817-4617-bec5-90efa2bad6g7")
	})

	t.Run("non sandbox user trying to create rolebinding is allowed", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "nonsandbox", false)
		// when
		response := v.validate(nonSandboxUserJSON)
		// then
		verifyRequestAllowed(t, response, "a68769e5-d817-4617-bec5-90efa2bad6f7")
	})

	t.Run("unable to find the requesting user, allow request", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "random-user", true)
		// when
		response := v.validate(sandboxUserForAllServiceAccountsJSON)
		// then
		verifyRequestAllowed(t, response, "a68769e5-d817-4617-bec5-90efa2bad6f6")
	})

	t.Run("sandbox user creating a rolebinding for a specific user is allowed", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "laracroft", true)
		//when
		response := v.validate(allowedRbJSON)
		//then
		verifyRequestAllowed(t, response, "a68769e5-d817-4617-bec5-90efa2bad8g8")
	})

}

func TestValidateRolebBndingAdmissionRequestFailsOnInvalidJson(t *testing.T) {
	// given
	rawJSON := []byte(`something wrong !`)
	v := &RoleBindingRequestValidator{}

	// when
	response := v.validate(rawJSON)

	// then
	verifyRequestBlocked(t, response, "cannot unmarshal string into Go value of type struct", "")
}

func TestValidateRolebBndingAdmissionRequestFailsOnInvalidObjectJson(t *testing.T) {
	// given
	v := &RoleBindingRequestValidator{}

	// when
	response := v.validate(incorrectRequestObjectJSON)

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

func toReviewResponse(t *testing.T, content []byte) *admissionv1.AdmissionResponse {
	r := admissionv1.AdmissionReview{}
	err := json.Unmarshal(content, &r)
	require.NoError(t, err)
	return r.Response
}

func newRoleBindingRequestValidator(t *testing.T, username string, sandboxUser bool) *RoleBindingRequestValidator {
	err := userv1.Install(scheme.Scheme)
	require.NoError(t, err)
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
	cl := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(testUser).Build()
	return &RoleBindingRequestValidator{
		Client: cl,
	}
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
var sandboxUserForAllServiceAccountsJSON = []byte(`{
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

var sandboxUserForAllServiceAccountsJSON2 = []byte(`{
	"kind": "AdmissionReview",
	"apiVersion": "admission.k8s.io/v1",
	"request": {
		"uid": "a68769e5-d817-4617-bec5-90efa2bad7g8",
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
				"name": "system:serviceaccounts:",
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

var sandboxUserForAllUsersJSON = []byte(`{
	"kind": "AdmissionReview",
	"apiVersion": "admission.k8s.io/v1",
	"request": {
		"uid": "a68769e5-d817-4617-bec5-90efa2bad8k8",
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
				"name": "system:authenticated",
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

var sandboxUserForAllUsersJSON2 = []byte(`{
	"kind": "AdmissionReview",
	"apiVersion": "admission.k8s.io/v1",
	"request": {
		"uid": "a68769e5-d817-4617-bec5-90efa2bad9l9",
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
				"name": "system:authenticated:",
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
