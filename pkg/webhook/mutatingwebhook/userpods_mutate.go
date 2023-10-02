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
	podM *podMutator

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
)

const (
	priority          = int32(-3)
	priorityClassName = "sandbox-users-pods"
)

func init() {
	podM = &podMutator{}
	podM.createAdmissionReviewResponseFunc = podM.createAdmissionReviewResponse

	podM.patchContent = podM.patchedContent(podPatchItems)
	podM.log = logf.Log.WithName("users_pods_mutating_webhook")
}

func HandleMutateUserPods(w http.ResponseWriter, r *http.Request) {
	podM.handleMutate(w, r)
}

type podMutator struct {
	patchContent []byte
	mutatorCommon
}

func (m *podMutator) createAdmissionReviewResponse(admReview v1.AdmissionReview) *v1.AdmissionResponse {
	// let's unmarshal the object to be sure that it's a pod
	var pod *corev1.Pod
	if err := json.Unmarshal(admReview.Request.Object.Raw, &pod); err != nil {
		m.log.Error(err, "unable unmarshal pod json object", "AdmissionReview", admReview)
		return responseWithError(errors.Wrapf(err, "unable unmarshal pod json object - raw request object: %v", admReview.Request.Object.Raw))
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

	// instead of changing the pod object we need to tell K8s how to change the object
	resp.Patch = m.patchContent

	m.log.Info("the sandbox-users-pods PriorityClass was set to the pod", "pod-name", pod.Name, "namespace", pod.Namespace)
	return resp
}

func (m *podMutator) patchedContent(patchItems []map[string]interface{}) []byte {
	if m.patchContent != nil {
		return m.patchContent
	}

	patchContent, err := json.Marshal(patchItems)
	if err != nil {
		m.log.Error(err, "unable to marshal patch items")
		os.Exit(1)
	}
	m.patchContent = patchContent
	return m.patchContent
}
