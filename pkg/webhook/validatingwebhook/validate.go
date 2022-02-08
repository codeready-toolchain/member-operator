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
		return denyAdmissionRequest(admReview, errors.Wrapf(err, "unable to deserialize the admission review object - body: %v", body))
	}
	// let's unmarshal the object to be sure that it's a rolebinding
	rb := rbac.RoleBinding{}
	if err := json.Unmarshal(admReview.Request.Object.Raw, &rb); err != nil || rb.Kind != "RoleBinding" {
		if err == nil {
			err = fmt.Errorf("request Object is not a rolebinding")
		}
		log.Error(err, "unable unmarshal rolebinding json object", "AdmissionReview", admReview)
		return denyAdmissionRequest(admReview, errors.Wrapf(err, "unable to unmarshal object or object is not a rolebinding - raw request object: %v", admReview.Request.Object.Raw))
	}
	requestingUsername := admReview.Request.UserInfo.Username
	// allow admission request if the user is a system user
	if strings.HasPrefix(requestingUsername, "system:") {
		return allowAdmissionRequest(admReview)
	}
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
	for _, sub := range subjects {
		if sub == allUsersSubject || sub == allServiceAccountsSubject {
			requestingUser := &userv1.User{}
			err := client.Get(context.TODO(), types.NamespacedName{
				Name: admReview.Request.UserInfo.Username,
			}, requestingUser)

			if err != nil {
				log.Error(fmt.Errorf("Cannot find the user: %w", err), "unable to find the user requesting creation")
				return denyAdmissionRequest(admReview, errors.Wrapf(err, "unable to find the user requesting creation: %s", requestingUsername))
			}
			//check if the requesting user is a sandbox user
			if requestingUser.GetLabels()[toolchainv1alpha1.ProviderLabelKey] == toolchainv1alpha1.ProviderLabelValue {
				log.Info("trying to give access which is restricted", "unable unmarshal rolebinding json object", "AdmissionReview", admReview)
				return denyAdmissionRequest(admReview, errors.Wrapf(fmt.Errorf("trying to give access which is restricted"), "Unauthorized request to create rolebinding json object - raw request object: %v", admReview.Request.Object.Raw))
			}
			//At this point, it is clear the user isn't a sandbox user,
			break
		}
	}
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
