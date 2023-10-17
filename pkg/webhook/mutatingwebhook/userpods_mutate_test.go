package mutatingwebhook

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var expectedMutatePodsRespSuccess = expectedSuccessResponse{
	patch:              `[{"op":"replace","path":"/spec/priorityClassName","value":"sandbox-users-pods"},{"op":"replace","path":"/spec/priority","value":-3}]`,
	auditAnnotationKey: "users_pods_mutating_webhook",
	auditAnnotationVal: "the sandbox-users-pods PriorityClass was set",
	uid:                "a68769e5-d817-4617-bec5-90efa2bad6f6",
}

func TestHandleMutateUserPodsSuccess(t *testing.T) {
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
	verifySuccessfulResponse(t, body, expectedMutatePodsRespSuccess)
}

func TestMutateUserPodsSuccess(t *testing.T) {
	// when
	response := mutate(podLogger, userPodsRawAdmissionReviewJSON, podMutator)

	// then
	verifySuccessfulResponse(t, response, expectedMutatePodsRespSuccess)
}

func TestMutateUserPodsFailsOnInvalidJson(t *testing.T) {
	// given
	rawJSON := []byte(`something wrong !`)
	var expectedResp = expectedFailedResponse{
		auditAnnotationKey: "users_pods_mutating_webhook",
		errMsg:             "cannot unmarshal string into Go value of type struct",
	}

	// when
	response := mutate(podLogger, rawJSON, podMutator)

	// then
	verifyFailedResponse(t, response, expectedResp)
}

func TestMutateUserPodsFailsOnInvalidPod(t *testing.T) {
	// when
	rawJSON := []byte(`{
		"request": {
			"object": 111
		}
	}`)

	// when
	response := mutate(podLogger, rawJSON, podMutator)

	// then
	var expectedResp = expectedFailedResponse{
		auditAnnotationKey: "users_pods_mutating_webhook",
		errMsg:             "cannot unmarshal number into Go value of type v1.Pod",
	}
	verifyFailedResponse(t, response, expectedResp)
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
