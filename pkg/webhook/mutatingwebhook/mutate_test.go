package mutatingwebhook

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/api/admission/v1"
)

func TestHandleMutateSuccess(t *testing.T) {
	// given
	ts := httptest.NewServer(http.HandlerFunc(HandleMutate))
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
	verifySuccessfulResponse(t, body)
}

func TestMutateSuccess(t *testing.T) {
	// when
	response := mutate(rawJSON)

	// then
	verifySuccessfulResponse(t, response)
}

func verifySuccessfulResponse(t *testing.T, response []byte) {
	reviewResponse := toReviewResponse(t, response)
	assert.Equal(t, `[{"op":"replace","path":"/spec/priorityClassName","value":"sandbox-users-pods"},{"op":"replace","path":"/spec/priority","value":-10}]`, string(reviewResponse.Patch))
	assert.Contains(t, "the sandbox-users-pods PriorityClass was set", reviewResponse.AuditAnnotations["users_pods_mutating_webhook"])
	assert.True(t, reviewResponse.Allowed)
	assert.Equal(t, v1.PatchTypeJSONPatch, *reviewResponse.PatchType)
	assert.Empty(t, reviewResponse.Result)
	assert.Equal(t, "a68769e5-d817-4617-bec5-90efa2bad6f6", string(reviewResponse.UID))
}

func TestMutateFailsOnInvalidJson(t *testing.T) {
	// given
	rawJSON := []byte(`something wrong !`)

	// when
	response := mutate(rawJSON)

	// then
	verifyFailedResponse(t, response, "cannot unmarshal string into Go value of type struct")
}

func TestMutateFailsOnInvalidPod(t *testing.T) {
	// when
	rawJSON := []byte(`{
		"request": {
			"object": 111
		}
	}`)

	// when
	response := mutate(rawJSON)

	// then
	verifyFailedResponse(t, response, "cannot unmarshal number into Go value of type v1.Pod")
}

func verifyFailedResponse(t *testing.T, response []byte, errMsg string) {
	reviewResponse := toReviewResponse(t, response)
	assert.Empty(t, string(reviewResponse.Patch))
	assert.Empty(t, reviewResponse.AuditAnnotations["users_pods_mutating_webhook"])
	assert.False(t, reviewResponse.Allowed)
	assert.Nil(t, reviewResponse.PatchType)
	assert.Empty(t, string(reviewResponse.UID))

	require.NotEmpty(t, reviewResponse.Result)
	assert.Contains(t, reviewResponse.Result.Message, errMsg)
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
      "kind": "Pod"
    },
    "resource": {
      "group": "",
      "version": "v1",
      "resource": "pods"
    },
    "requestKind": {
      "group": "",
      "version": "v1",
      "kind": "Pod"
    },
    "requestResource": {
      "group": "",
      "version": "v1",
      "resource": "pods"
    },
    "name": "busybox1",
    "namespace": "johnsmith-dev",
    "operation": "CREATE",
    "userInfo": {
      "username": "kube:admin",
      "groups": [
        "system:cluster-admins",
        "system:authenticated"
      ],
      "extra": {
        "scopes.authorization.openshift.io": [
          "user:full"
        ]
      }
    },
    "object": {
      "kind": "Pod",
      "apiVersion": "v1",
      "metadata": {
        "name": "busybox1",
        "namespace": "johnsmith-dev",
        "creationTimestamp": null,
        "labels": {
          "app": "busybox1"
        },
        "managedFields": []
      },
      "spec": {
        "volumes": [
          {
            "name": "default-token-8x9r5",
            "secret": {
              "secretName": "default-token-8x9r5"
            }
          }
        ],
        "containers": [
          {
            "name": "busybox",
            "image": "busybox",
            "command": [
              "sleep",
              "3600"
            ],
            "resources": {
              "limits": {
                "cpu": "150m",
                "memory": "750Mi"
              },
              "requests": {
                "cpu": "10m",
                "memory": "64Mi"
              }
            },
            "volumeMounts": [
              {
                "name": "default-token-8x9r5",
                "readOnly": true,
                "mountPath": "/var/run/secrets/kubernetes.io/serviceaccount"
              }
            ],
            "terminationMessagePath": "/dev/termination-log",
            "terminationMessagePolicy": "File",
            "imagePullPolicy": "IfNotPresent",
            "securityContext": {
              "capabilities": {
                "drop": [
                  "MKNOD"
                ]
              }
            }
          }
        ],
        "restartPolicy": "Always",
        "terminationGracePeriodSeconds": 30,
        "dnsPolicy": "ClusterFirst",
        "serviceAccountName": "default",
        "serviceAccount": "default",
        "securityContext": {
          "seLinuxOptions": {
            "level": "s0:c30,c0"
          }
        },
        "imagePullSecrets": [
          {
            "name": "default-dockercfg-k6xlc"
          }
        ],
        "schedulerName": "default-scheduler",
        "tolerations": [
          {
            "key": "node.kubernetes.io/not-ready",
            "operator": "Exists",
            "effect": "NoExecute",
            "tolerationSeconds": 300
          },
          {
            "key": "node.kubernetes.io/unreachable",
            "operator": "Exists",
            "effect": "NoExecute",
            "tolerationSeconds": 300
          },
          {
            "key": "node.kubernetes.io/memory-pressure",
            "operator": "Exists",
            "effect": "NoSchedule"
          }
        ],
        "priority": 0,
        "enableServiceLinks": true
      },
      "status": {}
    },
    "oldObject": null,
    "dryRun": false,
    "options": {
      "kind": "CreateOptions",
      "apiVersion": "meta.k8s.io/v1"
    }
  }
}`)
