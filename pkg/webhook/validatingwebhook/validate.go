package validatingwebhook

import (
	"encoding/json"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()

	log = logf.Log.WithName("validating_webhook")
)

func denyAdmissionRequest(admReview admissionv1.AdmissionReview, err error) []byte {
	response := &admissionv1.AdmissionResponse{
		Allowed: false,
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
	if admReview.Request != nil {
		response.UID = admReview.Request.UID
	}
	admReview.Response = response
	responseBody, err := json.Marshal(admReview)
	if err != nil {
		log.Error(err, "unable to marshal the admission review with response", "admissionReview", admReview)
		return []byte("unable to marshal the admission review with response")
	}
	return responseBody
}

func allowAdmissionRequest(admReview admissionv1.AdmissionReview) []byte {
	resp := &admissionv1.AdmissionResponse{
		Allowed: true,
		UID:     admReview.Request.UID,
	}
	admReview.Response = resp
	responseBody, err := json.Marshal(admReview)
	if err != nil {
		log.Error(err, "unable to marshal the admission review with response", "admissionReview", admReview)
		return []byte("unable to marshal the admission review with response")
	}
	return responseBody
}
