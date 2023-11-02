package mutatingwebhook

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/pkg/errors"
	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	podLogger = logf.Log.WithName("users_pods_mutating_webhook")

	podPatchItems = []map[string]interface{}{
		{
			"op":    "replace",
			"path":  "/spec/priorityClassName",
			"value": priorityClassName,
		},
		{
			"op":    "replace",
			"path":  "/spec/priority",
			"value": priority,
		},
	}

	patchedContent []byte
)

const (
	priority          = int32(-3)
	priorityClassName = "sandbox-users-pods"
)

func init() {
	var err error
	patchedContent, err = json.Marshal(podPatchItems)
	if err != nil {
		podLogger.Error(err, "unable to marshal patch items")
		os.Exit(1)
	}
}

func HandleMutateUserPods(w http.ResponseWriter, r *http.Request) {
	handleMutate(podLogger, w, r, podMutator)
}

func podMutator(admReview v1.AdmissionReview) *v1.AdmissionResponse {
	// let's unmarshal the object to be sure that it's a pod
	var pod *corev1.Pod
	if err := json.Unmarshal(admReview.Request.Object.Raw, &pod); err != nil {
		podLogger.Error(err, "failed to unmarshal pod json object", "AdmissionReview", admReview)
		return responseWithError(admReview.Request.UID, errors.Wrap(err, "failed to unmarshal pod json object"))
	}

	patchType := v1.PatchTypeJSONPatch
	resp := &v1.AdmissionResponse{
		Allowed:   true,
		UID:       admReview.Request.UID,
		PatchType: &patchType,
	}
	resp.AuditAnnotations = map[string]string{
		"users_pods_mutating_webhook": "the sandbox-users-pods PriorityClass was set",
	}

	// instead of changing the pod object we need to tell K8s how to change the object via patch
	resp.Patch = patchedContent

	podLogger.Info("the sandbox-users-pods PriorityClass was set to the pod", "pod-name", pod.Name, "namespace", pod.Namespace)
	return resp
}
