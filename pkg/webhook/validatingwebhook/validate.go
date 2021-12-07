package validatingwebhook

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

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

func HandleValidate(w http.ResponseWriter, r *http.Request) {
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
		respBody = validate(body)
		w.WriteHeader(http.StatusOK)
	}
	if _, err := w.Write(respBody); err != nil {
		log.Error(err, "unable to write response")
	}
}

func validate(body []byte) []byte {
	admReview := v1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &admReview); err != nil {
		log.Error(err, "unable to deserialize the admission review object", "body", body)
		admReview.Response = responseWithError(err)
	}
	// let's unmarshal the object to be sure that it's a rolebinding
	var rb *rbac.RoleBinding
	if err := json.Unmarshal(admReview.Request.Object.Raw, &rb); err != nil {
		log.Error(err, "unable unmarshal rolebinding json object", "AdmissionReview", admReview)
		admReview.Response = responseWithError(errors.Wrapf(err, "unable unmarshal rolebinding json object - raw request object: %v", admReview.Request.Object.Raw))
	}
	requestingUser := admReview.Request.UserInfo
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
	fmt.Printf(">>>>>>>> Requesting User is %v \n", requestingUser)
	for _, sub := range subjects {
		if sub == allUsersSubject || sub == allServiceAccountsSubject {
			log.Error(fmt.Errorf("trying to give access which is restricted"), "unable unmarshal rolebinding json object", "AdmissionReview", admReview)
			admReview.Response = responseWithError(errors.Wrapf(fmt.Errorf("trying to give access which is restricted"), "unable unmarshal rolebinding json object - raw request object: %v", admReview.Request.Object.Raw))
		}
	}
	admReview.Response = createAdmissionReviewResponse(admReview)

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

	resp := &v1.AdmissionResponse{
		Allowed: true,
		UID:     admReview.Request.UID,
	}
	return resp
}
