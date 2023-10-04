package mutatingwebhook

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()
)

type mutateHandler func(admReview v1.AdmissionReview) *v1.AdmissionResponse

func handleMutate(logger logr.Logger, w http.ResponseWriter, r *http.Request, mutator mutateHandler) {
	var respBody []byte
	body, err := io.ReadAll(r.Body)
	defer func() {
		if err := r.Body.Close(); err != nil {
			logger.Error(err, "unable to close the body")
		}
	}()
	if err != nil {
		logger.Error(err, "unable to read the body of the request")
		w.WriteHeader(http.StatusInternalServerError)
		respBody = []byte("unable to read the body of the request")
	} else {
		// mutate the request
		respBody = mutate(logger, body, mutator)
		w.WriteHeader(http.StatusOK)
	}
	if _, err := io.WriteString(w, string(respBody)); err != nil {
		logger.Error(err, "unable to write response")
	}
}

func mutate(logger logr.Logger, body []byte, mutator mutateHandler) []byte {
	admReview := v1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &admReview); err != nil {
		logger.Error(err, "unable to deserialize the admission review object", "body", string(body))
		admReview.Response = responseWithError(err)
	} else if admReview.Request == nil {
		err := fmt.Errorf("admission review request is nil")
		logger.Error(err, "cannot read the admission review request", "AdmissionReview", admReview)
		admReview.Response = responseWithError(err)
	} else {
		admReview.Response = mutator(admReview)
	}
	responseBody, err := json.Marshal(admReview)
	if err != nil {
		logger.Error(err, "unable to marshal the admission review with response", "admissionReview", admReview)
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
