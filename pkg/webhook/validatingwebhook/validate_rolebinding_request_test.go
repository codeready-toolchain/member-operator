package validatingwebhook

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/validatingwebhook/test"

	userv1 "github.com/openshift/api/user/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHandleValidateRoleBindingAdmissionRequestBlocked(t *testing.T) {
	v := newRoleBindingRequestValidator(t, "johnsmith", true)
	// given
	ts := httptest.NewServer(http.HandlerFunc(v.HandleValidate))
	defer ts.Close()

	// when
	resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(sandboxUserForAllServiceAccountsJSON))

	// then
	assert.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()
	assert.NoError(t, err)
	test.VerifyRequestBlocked(t, body, "please create a rolebinding for a specific user or service account to avoid this error", "a68769e5-d817-4617-bec5-90efa2bad6f6")
}

func TestValidateRoleBindingAdmissionRequest(t *testing.T) {
	t.Run("sandbox user trying to create rolebinding for all serviceaccounts is denied", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "johnsmith", true)
		// when
		response := v.validate(sandboxUserForAllServiceAccountsJSON)
		// then
		test.VerifyRequestBlocked(t, response, "please create a rolebinding for a specific user or service account to avoid this error", "a68769e5-d817-4617-bec5-90efa2bad6f6")
	})

	t.Run("sandbox user trying to create rolebinding for all serviceaccounts: is denied", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "johnsmith", true)
		// when
		response := v.validate(sandboxUserForAllServiceAccountsJSON2)
		// then
		test.VerifyRequestBlocked(t, response, "please create a rolebinding for a specific user or service account to avoid this error", "a68769e5-d817-4617-bec5-90efa2bad7g8")
	})

	t.Run("sandbox user trying to create rolebinding for all authenticated users is denied", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "johnsmith", true)
		// when
		response := v.validate(sandboxUserForAllUsersJSON)
		// then
		test.VerifyRequestBlocked(t, response, "please create a rolebinding for a specific user or service account to avoid this error", "a68769e5-d817-4617-bec5-90efa2bad8k8")
	})

	t.Run("sandbox user trying to create rolebinding for all authenticated: is denied", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "johnsmith", true)
		// when
		response := v.validate(sandboxUserForAllUsersJSON2)
		// then
		test.VerifyRequestBlocked(t, response, "please create a rolebinding for a specific user or service account to avoid this error", "a68769e5-d817-4617-bec5-90efa2bad9l9")
	})
}

func TestValidateRoleBindingAdmissionRequestAllowed(t *testing.T) {

	t.Run("SA or kubeadmin trying to create rolebinding is allowed", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "system:kubeadmin", false)
		// when user is kubeadmin
		response := v.validate(allowedUserJSON)

		// then
		test.VerifyRequestAllowed(t, response, "a68769e5-d817-4617-bec5-90efa2bad6g7")
	})

	t.Run("non sandbox user trying to create rolebinding is allowed", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "nonsandbox", false)
		// when
		response := v.validate(nonSandboxUserJSON)
		// then
		test.VerifyRequestAllowed(t, response, "a68769e5-d817-4617-bec5-90efa2bad6f7")
	})

	t.Run("unable to find the requesting user, allow request", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "random-user", true)
		// when
		response := v.validate(sandboxUserForAllServiceAccountsJSON)
		// then
		test.VerifyRequestAllowed(t, response, "a68769e5-d817-4617-bec5-90efa2bad6f6")
	})

	t.Run("sandbox user creating a rolebinding for a specific user is allowed", func(t *testing.T) {
		v := newRoleBindingRequestValidator(t, "laracroft", true)
		//when
		response := v.validate(allowedRbJSON)
		//then
		test.VerifyRequestAllowed(t, response, "a68769e5-d817-4617-bec5-90efa2bad8g8")
	})

}

func TestValidateRolebBndingAdmissionRequestFailsOnInvalidJson(t *testing.T) {
	// given
	rawJSON := []byte(`something wrong !`)
	v := &RoleBindingRequestValidator{}

	// when
	response := v.validate(rawJSON)

	// then
	test.VerifyRequestBlocked(t, response, "cannot unmarshal string into Go value of type struct", "")
}

func TestValidateRolebBndingAdmissionRequestFailsOnInvalidObjectJson(t *testing.T) {
	// given
	v := &RoleBindingRequestValidator{}

	// when
	response := v.validate(test.IncorrectRequestObjectJSON)

	// then
	test.VerifyRequestBlocked(t, response, "unable to unmarshal object or object is not a rolebinding", "a68769e5-d817-4617-bec5-90efa2bad6f8")
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
