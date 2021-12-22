package validatingwebhook

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/admission/v1"
)

func TestHandleValidateBlocked(t *testing.T) {
	// given
	ts := httptest.NewServer(http.HandlerFunc(HandleValidate))
	defer ts.Close()

	// when
	resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(rawJSON))

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
	// when
	response := validate(rawJSON)

	// then
	verifyRequestBlocked(t, response)
}

func TestValidateAllow(t *testing.T) {
	// when user is kubeadmin
	response := validate(allowedJSON)

	// then
	verifyRequestAllowed(t, response)
}

func verifyRequestBlocked(t *testing.T, response []byte) {
	reviewResponse := toReviewResponse(t, response)
	assert.False(t, reviewResponse.Allowed)
	assert.NotEmpty(t, reviewResponse.Result)
	assert.Contains(t, reviewResponse.Result.Message, "trying to give access which is restricted")
	assert.Equal(t, "a68769e5-d817-4617-bec5-90efa2bad6f6", string(reviewResponse.UID))
}

func verifyRequestAllowed(t *testing.T, response []byte) {
	reviewResponse := toReviewResponse(t, response)
	assert.True(t, reviewResponse.Allowed)
	assert.Empty(t, reviewResponse.Result)
	assert.Equal(t, "a68769e5-d817-4617-bec5-90efa2bad6g7", string(reviewResponse.UID))
}

func toReviewResponse(t *testing.T, content []byte) *v1.AdmissionResponse {
	r := v1.AdmissionReview{}
	err := json.Unmarshal(content, &r)
	require.NoError(t, err)
	return r.Response
}

var rawJSON = []byte(`{
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

var allowedJSON = []byte(`{
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
