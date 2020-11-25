package mutatingwebhook

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/pkg/errors"
	"k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()

	log = logf.Log.WithName("users_pods_mutating_webhook")

	patchContent = patchedContent()
)

const (
	priority          = int32(-10)
	priorityClassName = "sandbox-users-pods"
)

func patchedContent() []byte {
	patchItems := []map[string]interface{}{
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

	patchContent, err := json.Marshal(patchItems)
	if err != nil {
		log.Error(err, "unable marshal patch items")
		os.Exit(1)
	}
	return patchContent
}

func HandleMutate(w http.ResponseWriter, r *http.Request) {
	var respBody []byte
	body, err := ioutil.ReadAll(r.Body)
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Error(err, "unable to close the body")
		}
	}()
	if err != nil {
		log.Error(err, "unable to read the body of the request")
		w.WriteHeader(http.StatusInternalServerError)
		respBody = []byte(fmt.Sprintf("unable to read the body of the request: %s", err))
	} else {
		// mutate the request
		respBody = mutate(body)
		w.WriteHeader(http.StatusOK)
	}
	if _, err := w.Write(respBody); err != nil {
		log.Error(err, "unable to write response")
	}
}

func mutate(body []byte) []byte {
	log.Info("received", "body", string(body))

	admReview := v1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &admReview); err != nil {
		log.Error(err, "unable to deserialize the admission review object", "body", body)
		admReview.Response = responseWithError(err)
	} else {
		admReview.Response = createAdmissionReviewResponse(admReview)
	}
	responseBody, err := json.Marshal(admReview)
	if err != nil {
		log.Error(err, "unable to marshal the admission review with response", "admissionReview", admReview)
	}
	return responseBody
}

func responseWithError(err error) *v1.AdmissionResponse {
	return &v1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}

func createAdmissionReviewResponse(admReview v1.AdmissionReview) *v1.AdmissionResponse {
	if admReview.Request == nil {
		err := fmt.Errorf("admission review request is nil")
		log.Error(err, "cannot read the admission review request", "AdmissionReview", admReview)
		return responseWithError(err)
	}

	// let's unmarshal the object to be sure that it's a pod
	var pod *corev1.Pod
	if err := json.Unmarshal(admReview.Request.Object.Raw, &pod); err != nil {
		log.Error(err, "unable unmarshal pod json object", "AdmissionReview", admReview)
		return responseWithError(errors.Wrapf(err, "unable unmarshal pod json object"))
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
	resp.Patch = patchContent

	log.Info("the sandbox-users-pods PriorityClass was set to the pod", "pod-name", pod.Name, "namespace", pod.Namespace)
	return resp
}
