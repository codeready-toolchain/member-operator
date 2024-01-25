package validatingwebhook

import (
	"context"
	"html"
	"io"
	"net/http"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"

	userv1 "github.com/openshift/api/user/v1"
	"github.com/pkg/errors"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/types"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"
)

// The SSP resources is a CNV specific resource
type SSPRequestValidator struct {
	Client runtimeClient.Client
}

func (v SSPRequestValidator) HandleValidate(w http.ResponseWriter, r *http.Request) {
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
		respBody = v.validate(r.Context(), body)
		w.WriteHeader(http.StatusOK)
	}
	if _, err := io.WriteString(w, string(respBody)); err != nil {
		log.Error(err, "unable to write response")
	}
}

func (v SSPRequestValidator) validate(ctx context.Context, body []byte) []byte {
	log.Info("incoming request", "body", string(body))
	admReview := admissionv1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &admReview); err != nil {
		// sanitize the body
		escapedBody := html.EscapeString(string(body))
		log.Error(err, "unable to deserialize the admission review object", "body", escapedBody)
		return denyAdmissionRequest(admReview, errors.Wrapf(err, "unable to deserialize the admission review object - body: %v", escapedBody))
	}
	//check if the requesting user is a sandbox user
	requestingUser := &userv1.User{}
	err := v.Client.Get(ctx, types.NamespacedName{
		Name: admReview.Request.UserInfo.Username,
	}, requestingUser)

	if err != nil {
		log.Error(err, "unable to find the user requesting creation of the SSP resource", "username", admReview.Request.UserInfo.Username)
		return denyAdmissionRequest(admReview, errors.New("unable to find the user requesting the creation of the SSP resource"))
	}
	if requestingUser.GetLabels()[toolchainv1alpha1.ProviderLabelKey] == toolchainv1alpha1.ProviderLabelValue {
		log.Info("sandbox user is trying to create a SSP", "AdmissionReview", admReview)
		return denyAdmissionRequest(admReview, errors.New("this is a Dev Sandbox enforced restriction. you are trying to create a SSP resource, which is not allowed"))
	}
	// at this point, it is clear the user isn't a sandbox user, allow request
	return allowAdmissionRequest(admReview)
}
