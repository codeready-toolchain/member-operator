package validatingwebhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"

	userv1 "github.com/openshift/api/user/v1"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/types"

	"github.com/pkg/errors"
	rbac "k8s.io/api/rbac/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()

	log = logf.Log.WithName("users_rolebindings_validating_webhook")
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
	admReview := v1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &admReview); err != nil {
		log.Error(err, "unable to deserialize the admission review object", "body", body)
		return responseWithError(admReview, err)
	}
	// let's unmarshal the object to be sure that it's a rolebinding
	var rb *rbac.RoleBinding
	if err := json.Unmarshal(admReview.Request.Object.Raw, &rb); err != nil {
		log.Error(err, "unable unmarshal rolebinding json object", "AdmissionReview", admReview)
		return responseWithError(admReview, errors.Wrapf(err, "unable unmarshal rolebinding json object - raw request object: %v", admReview.Request.Object.Raw))
	}
	requestingUsername := admReview.Request.UserInfo.Username
	subjects := rb.Subjects
	allServiceAccountsSubject := rbac.Subject{
		Kind:     "Group",
		Name:     "system:serviceaccounts",
		APIGroup: "rbac.authorization.k8s.io",
	}
	allUsersSubject := rbac.Subject{
		Kind:     "Group",
		Name:     "system:authenticated",
		APIGroup: "rbac.authorization.k8s.io",
	}
	requestingUser := &userv1.User{}
	err := client.Get(context.TODO(), types.NamespacedName{
		Name: admReview.Request.UserInfo.Username,
	}, requestingUser)

	if err != nil {
		log.Error(fmt.Errorf("Cannot find the user: %w", err), "unable to find the user requesting creation")
		return []byte(fmt.Sprintf("unable to find the user requesting creation: %s", err))
	}
	//check if the requesting user is a sandbox user
	if !strings.HasPrefix(requestingUsername, "system:") && requestingUser.GetLabels()[toolchainv1alpha1.ProviderLabelKey] == toolchainv1alpha1.ProviderLabelValue {
		for _, sub := range subjects {
			if sub == allUsersSubject || sub == allServiceAccountsSubject {
				log.Error(fmt.Errorf("trying to give access which is restricted"), "unable unmarshal rolebinding json object", "AdmissionReview", admReview)
				return responseWithError(admReview, errors.Wrapf(fmt.Errorf("trying to give access which is restricted"), "unable unmarshal rolebinding json object - raw request object: %v", admReview.Request.Object.Raw))
			}
		}
	} else {
		admReview.Response = createAdmissionReviewResponse(admReview)
	}

	responseBody, err := json.Marshal(admReview)
	if err != nil {
		log.Error(err, "unable to marshal the admission review with response", "admissionReview", admReview)
	}
	return responseBody
}

func responseWithError(admReview v1.AdmissionReview, err error) []byte {
	response := &v1.AdmissionResponse{
		UID:     admReview.Request.UID,
		Allowed: false,
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
	admReview.Response = response
	responseBody, err := json.Marshal(admReview)
	if err != nil {
		log.Error(err, "unable to marshal the admission review with response", "admissionReview", admReview)
	}
	return responseBody

}

func createAdmissionReviewResponse(admReview v1.AdmissionReview) *v1.AdmissionResponse {
	if admReview.Request == nil {
		err := fmt.Errorf("admission review request is nil")
		log.Error(err, "cannot read the admission review request", "AdmissionReview", admReview)
		return &v1.AdmissionResponse{
			UID:     admReview.Request.UID,
			Allowed: false,
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	resp := &v1.AdmissionResponse{
		Allowed: true,
		UID:     admReview.Request.UID,
	}
	return resp
}
