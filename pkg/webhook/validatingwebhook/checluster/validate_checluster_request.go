package checluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"

	userv1 "github.com/openshift/api/user/v1"
	"github.com/pkg/errors"
	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()

	log = logf.Log.WithName("users_checluster_validating_webhook")
)

type Validator struct {
	Client runtimeClient.Client
}

func (v Validator) HandleValidate(w http.ResponseWriter, r *http.Request) {
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
		// validate the request
		respBody = validate(body, v.Client)
		w.WriteHeader(http.StatusOK)
	}
	if _, err := w.Write(respBody); err != nil {
		log.Error(err, "unable to write response")
	}
}

func validate(body []byte, client runtimeClient.Client) []byte {
	log.Info("incoming request", "body", string(body))
	admReview := v1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &admReview); err != nil {
		log.Error(err, "unable to deserialize the admission review object", "body", string(body))
		return denyAdmissionRequest(admReview, errors.Wrapf(err, "unable to deserialize the admission review object - body: %v", string(body)))
	}
	requestingUsername := admReview.Request.UserInfo.Username
	// allow admission request if the user is a system user
	if strings.HasPrefix(requestingUsername, "system:") {
		return allowAdmissionRequest(admReview)
	}
	//check if the requesting user is a sandbox user
	requestingUser := &userv1.User{}
	err := client.Get(context.TODO(), types.NamespacedName{
		Name: admReview.Request.UserInfo.Username,
	}, requestingUser)

	if err != nil {
		log.Error(err, "unable to find the user requesting creation of the CheCluster resource", "username", admReview.Request.UserInfo.Username)
		return denyAdmissionRequest(admReview, errors.New("unable to find the user requesting the  creation of the CheCluster resource"))
	}
	if requestingUser.GetLabels()[toolchainv1alpha1.ProviderLabelKey] == toolchainv1alpha1.ProviderLabelValue {
		log.Info("sandbox user is trying to create a CheCluster", "AdmissionReview", admReview)
		return denyAdmissionRequest(admReview, errors.New("this is a Dev Sandbox enforced restriction. you are trying to create a CheCluster resource, which is not allowed"))
	}
	// at this point, it is clear the user isn't a sandbox user, allow request
	return allowAdmissionRequest(admReview)
}

func denyAdmissionRequest(admReview v1.AdmissionReview, err error) []byte {
	response := &v1.AdmissionResponse{
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

func allowAdmissionRequest(admReview v1.AdmissionReview) []byte {
	resp := &v1.AdmissionResponse{
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
