package validatingwebhook

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"

	userv1 "github.com/openshift/api/user/v1"
	"github.com/pkg/errors"
	admissionv1 "k8s.io/api/admission/v1"
	rbac "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"
)

type RoleBindingRequestValidator struct {
	Client runtimeClient.Client
}

func (v RoleBindingRequestValidator) HandleValidate(w http.ResponseWriter, r *http.Request) {
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
	if _, err := io.Writer.Write(w, respBody); err != nil { //using 'io.Writer.Write' as per the static check SA6006: use io.Writer.Write instead of converting from []byte to string to use io.WriteString (staticcheck)
		log.Error(err, "unable to write response")
	}
}

func (v RoleBindingRequestValidator) validate(ctx context.Context, body []byte) []byte {
	admReview := admissionv1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &admReview); err != nil {
		//sanitize the body
		escapedBody := html.EscapeString(string(body))
		log.Error(err, "unable to deserialize the admission review object", "body", escapedBody)
		return denyAdmissionRequest(admReview, errors.Wrapf(err, "unable to deserialize the admission review object - body: %v", escapedBody))
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
	subjectsList := getBlockSubjectList()
	for _, sub := range subjects {
		if containsSubject(subjectsList, sub) {
			requestingUser := &userv1.User{}
			err := v.Client.Get(ctx, types.NamespacedName{
				Name: admReview.Request.UserInfo.Username,
			}, requestingUser)

			if err != nil {
				log.Error(err, "unable to find the user requesting the rolebinding creation", "username", admReview.Request.UserInfo.Username)
				// We do not want to deny if it's an unknown user, continue to make another attempt to get user. at the end of loop request is allowed
				continue
			}
			//check if the requesting user is a sandbox user
			if requestingUser.GetLabels()[toolchainv1alpha1.ProviderLabelKey] == toolchainv1alpha1.ProviderLabelValue {
				log.Info("sandbox user is trying to create a rolebinding giving wider access", "AdmissionReview", admReview)
				return denyAdmissionRequest(admReview, errors.Wrapf(fmt.Errorf("please create a rolebinding for a specific user or service account to avoid this error"), "this is a Dev Sandbox enforced restriction. you are trying to create a rolebinding giving access to a larger audience, i.e : %v and requesting user: %+v", sub.Name, requestingUser.Labels))
			}
			//At this point, it is clear the user isn't a sandbox user, allow request
			break
		}
	}
	return allowAdmissionRequest(admReview)
}

func getBlockSubjectList() []rbac.Subject {

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
	allServiceAccountsSubjectColon := rbac.Subject{
		Kind:     "Group",
		Name:     "system:serviceaccounts:",
		APIGroup: "rbac.authorization.k8s.io",
	}
	allUsersSubjectColon := rbac.Subject{
		Kind:     "Group",
		Name:     "system:authenticated:",
		APIGroup: "rbac.authorization.k8s.io",
	}

	subjectList := []rbac.Subject{allServiceAccountsSubject, allUsersSubject, allServiceAccountsSubjectColon, allUsersSubjectColon}
	return subjectList
}

func containsSubject(subjectList []rbac.Subject, subject rbac.Subject) bool {
	for _, sub := range subjectList {
		if sub == subject {
			return true
		}
	}
	return false
}
