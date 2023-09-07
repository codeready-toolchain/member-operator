package validatingwebhook

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	errs "github.com/pkg/errors"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"
)

type SpaceBindingRequestValidator struct {
	Client runtimeClient.Client
}

func (v SpaceBindingRequestValidator) HandleValidate(w http.ResponseWriter, r *http.Request) {
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
	if _, err := io.WriteString(w, string(respBody)); err != nil {
		log.Error(err, "unable to write response")
	}
}

func (v SpaceBindingRequestValidator) validate(body []byte) []byte {
	admReview := admissionv1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &admReview); err != nil {
		//sanitize the body
		escapedBody := html.EscapeString(string(body))
		log.Error(err, "unable to deserialize the admission review object", "body", escapedBody)
		return denyAdmissionRequest(admReview, errs.Wrapf(err, "unable to deserialize the admission review object - body: %v", escapedBody))
	}
	// let's unmarshal the object to be sure that it's a spacebindingrequest
	newSBR := toolchainv1alpha1.SpaceBindingRequest{}
	if err := json.Unmarshal(admReview.Request.Object.Raw, &newSBR); err != nil || newSBR.Kind != "SpaceBindingRequest" {
		if err == nil {
			err = fmt.Errorf("request Object is not a SpaceBindingRequest")
		}
		log.Error(err, "unable unmarshal spacebindingrequest json object", "AdmissionReview", admReview)
		return denyAdmissionRequest(admReview, errs.Wrapf(err, "unable to unmarshal object or object is not a spacebindingrequest - raw request object: %v", admReview.Request.Object.Raw))
	}
	// fetch SBR and check that MUR is unchanged
	existingSBR := &toolchainv1alpha1.SpaceBindingRequest{}
	if err := v.Client.Get(context.TODO(), types.NamespacedName{
		Name:      newSBR.GetName(),
		Namespace: newSBR.GetNamespace(),
	}, existingSBR); err != nil {
		if errors.IsNotFound(err) {
			// this is a new SBR , not an existing one
			return allowAdmissionRequest(admReview)
		}
		// there was an issue while trying to GET SBR
		log.Error(err, "unable to check if spacebindingrequest already exists", "SpaceBindingRequest.Name", newSBR.GetName(), "SpaceBindingRequest.Namespace", newSBR.GetNamespace())
		return denyAdmissionRequest(admReview, errs.Wrapf(err, "unable to validate the SpaceBindingRequest. SpaceBindingRequest.Name: %v", newSBR.GetName()))
	}

	// check that MUR field is unchanged
	if existingSBR.Spec.MasterUserRecord != newSBR.Spec.MasterUserRecord {
		// MUR name field is immutable since the SpaceBinding name contains the MUR name,
		// changing it, then it wouldn't match anymore.
		return denyAdmissionRequest(admReview, errs.New("SpaceBindingRequest.MasterUserRecord field cannot be updated. Consider deleting and recreating the SpaceBindingRequest resource"))
	}

	return allowAdmissionRequest(admReview)
}
