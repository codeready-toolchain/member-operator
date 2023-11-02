package mutatingwebhook

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func expectedMutatePodsRespSuccess(t *testing.T) admissionv1.AdmissionResponse {
	patches := []map[string]interface{}{
		{
			"op":    "replace",
			"path":  "/spec/priorityClassName",
			"value": "sandbox-users-pods",
		},
		{
			"op":    "replace",
			"path":  "/spec/priority",
			"value": -3,
		},
	}

	patchContent, err := json.Marshal(patches)
	require.NoError(t, err)

	patchType := admissionv1.PatchTypeJSONPatch
	return admissionv1.AdmissionResponse{
		Allowed: true,
		AuditAnnotations: map[string]string{
			"users_pods_mutating_webhook": "the sandbox-users-pods PriorityClass was set",
		},
		UID:       "a68769e5-d817-4617-bec5-90efa2bad6f6",
		Patch:     patchContent,
		PatchType: &patchType,
	}
}

func TestHandleMutateUserPods(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		// given
		ts := httptest.NewServer(http.HandlerFunc(HandleMutateUserPods))
		defer ts.Close()

		// when
		resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(userPodsRawAdmissionReviewJSON))

		// then
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		defer func() {
			require.NoError(t, resp.Body.Close())
		}()
		assert.NoError(t, err)
		assertResponseEqual(t, body, expectedMutatePodsRespSuccess(t))
	})
}

func TestPodMutator(t *testing.T) {
	// given
	admReview := admissionReview(t, userPodsRawAdmissionReviewJSON)

	// when
	actualResponse := podMutator(admReview)

	// then
	assert.Equal(t, expectedMutatePodsRespSuccess(t), *actualResponse)
}

func TestMutateUserPodsFailsOnInvalidJson(t *testing.T) {
	// given
	badObj := map[string]interface{}{
		"spec": "bad data",
	}
	badJSON, err := json.Marshal(badObj)
	require.NoError(t, err)
	admReview := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			Object: runtime.RawExtension{
				Raw: badJSON,
			},
		},
	}

	// when
	actualResponse := podMutator(admReview)

	// then
	assert.Empty(t, actualResponse.UID)
	assert.Contains(t, actualResponse.Result.Message, "failed to unmarshal pod json object")
}

var userPodsRawAdmissionReviewJSON = []byte(`{
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
