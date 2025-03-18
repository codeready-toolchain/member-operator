package validatingwebhook

import (
	"html"
	"io"
	"net/http"

	"github.com/pkg/errors"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"
)

const manualRunStrategy = "Manual"

type VMRequestValidator struct {
	Client runtimeClient.Client
}

func (v VMRequestValidator) HandleValidate(w http.ResponseWriter, r *http.Request) {
	var respBody []byte
	body, err := io.ReadAll(r.Body)
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Error(err, "unable to close the body")
		}
	}()
	if err != nil {
		log.Error(err, "unable to read the body of the request")
		w.WriteHeader(http.StatusInternalServerError)
		respBody = []byte("unable to read the body of the request")
	} else {
		// validate the request
		respBody = v.validate(body)
		w.WriteHeader(http.StatusOK)
	}
	if _, err := io.Writer.Write(w, respBody); err != nil { //using 'io.Writer.Write' as per the static check SA6006: use io.Writer.Write instead of converting from []byte to string to use io.WriteString (staticcheck)
		log.Error(err, "unable to write response")
	}
}

func (v VMRequestValidator) validate(body []byte) []byte {
	log.Info("incoming request", "body", string(body))
	admReview := admissionv1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &admReview); err != nil {
		// sanitize the body
		escapedBody := html.EscapeString(string(body))
		log.Error(err, "unable to deserialize the admission review object", "body", escapedBody)
		return denyAdmissionRequest(admReview, errors.Wrapf(err, "unable to deserialize the admission review object - body: %v", escapedBody))
	}

	unstructuredRequestObj := &unstructured.Unstructured{}
	if err := unstructuredRequestObj.UnmarshalJSON(admReview.Request.Object.Raw); err != nil {
		log.Error(err, "unable to check runStrategy in VirtualMachine", "VirtualMachine", unstructuredRequestObj)
		return denyAdmissionRequest(admReview, errors.New("failed to validate VirtualMachine request"))
	}

	runStrategy, hasRunStrategy, err := unstructured.NestedString(unstructuredRequestObj.Object, "spec", "runStrategy")
	if err != nil {
		log.Error(err, "failed to unmarshal VirtualMachine json object", "AdmissionReview", admReview)
		return denyAdmissionRequest(admReview, errors.New("failed to validate VirtualMachine request"))
	}
	if hasRunStrategy && runStrategy != manualRunStrategy {
		log.Info("sandbox user is trying to configure a VM with RunStrategy set to a value other than Manual", "AdmissionReview", admReview) // other run strategies are not allowed because it conflicts with the Dev Sandbox Idler
		return denyAdmissionRequest(admReview, errors.New("this is a Dev Sandbox enforced restriction. Only 'Manual' RunStrategy is permitted"))
	}
	// the user is configuring a VM without the 'runStrategy' configured or with it configured to Manual, allowing the request.
	return allowAdmissionRequest(admReview)
}
