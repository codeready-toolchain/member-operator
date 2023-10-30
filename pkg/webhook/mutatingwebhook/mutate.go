package mutatingwebhook

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()
)

type mutateHandler func(admReview admissionv1.AdmissionReview) *admissionv1.AdmissionResponse

// handleMutate is a common function that decodes an admission review request before handing it off to the
// mutator for processing and then writes the response
func handleMutate(logger logr.Logger, w http.ResponseWriter, r *http.Request, mutator mutateHandler) {
	admReviewBody, err := io.ReadAll(r.Body)
	defer func() {
		if err := r.Body.Close(); err != nil {
			logger.Error(err, "unable to close the body")
		}
	}()
	if err != nil {
		msg := "unable to read the body of the request"
		logger.Error(err, msg)
		writeResponse(logger, http.StatusInternalServerError, w, []byte(msg))
		return
	}

	// deserialize the request
	admReview := admissionv1.AdmissionReview{}
	if _, _, err := deserializer.Decode(admReviewBody, nil, &admReview); err != nil {
		logger.Error(err, "unable to deserialize the admission review object", "body", string(admReviewBody))
		writeResponse(logger, http.StatusBadRequest, w, []byte("unable to read the body of the request"))
		return
	} else if admReview.Request == nil {
		err = fmt.Errorf("admission review request is nil")
		logger.Error(err, "invalid admission review request", "AdmissionReview", admReview)
		writeResponse(logger, http.StatusBadRequest, w, []byte("unable to read the body of the request"))
		return
	}

	// mutate the request
	admReview.Response = mutator(admReview)

	respBody, err := json.Marshal(admReview)
	if err != nil {
		logger.Error(err, "unable to marshal the admission review with response", "admissionReview", admReview)
		writeResponse(logger, http.StatusInternalServerError, w, []byte("failed to marshal the adm review resposne"))
		return
	}
	writeResponse(logger, http.StatusOK, w, respBody)
}

func writeResponse(logger logr.Logger, responseCode int, w http.ResponseWriter, respBody []byte) {
	w.WriteHeader(responseCode)
	if _, err := io.WriteString(w, string(respBody)); err != nil {
		logger.Error(err, "unable to write adm review response")
	}
}

func responseWithError(uid types.UID, err error) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		Allowed: false,
		UID:     uid,
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}
