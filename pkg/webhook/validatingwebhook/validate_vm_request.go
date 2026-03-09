package validatingwebhook

import (
	"html"
	"io"
	"net/http"
	"strings"

	"github.com/pkg/errors"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
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

	// runStrategy is mutated to manual by the virtual machines mutating webhook for CREATE so it only needs to be blocked for UPDATE
	if admReview.Request.Operation == admissionv1.Update {
		runStrategy, hasRunStrategy, err := unstructured.NestedString(unstructuredRequestObj.Object, "spec", "runStrategy")
		if err != nil {
			log.Error(err, "failed to unmarshal VirtualMachine json object", "AdmissionReview", admReview)
			return denyAdmissionRequest(admReview, errors.New("failed to validate VirtualMachine request"))
		}
		if hasRunStrategy && runStrategy != manualRunStrategy {
			log.Info("sandbox user is trying to configure a VM with RunStrategy set to a value other than Manual", "AdmissionReview", admReview) // other run strategies are not allowed because it conflicts with the Dev Sandbox Idler
			return denyAdmissionRequest(admReview, errors.New("this is a Dev Sandbox enforced restriction. Only 'Manual' RunStrategy is permitted"))
		}
	}

	// validate that a username is configured in the cloudInit volume (only for CREATE operations since cloudInit is used for "first-boot" initialization)
	if admReview.Request.Operation == admissionv1.Create {
		if err := validateCloudInitUsername(unstructuredRequestObj); err != nil {
			log.Error(err, "cloudInit username validation failed", "VirtualMachine", unstructuredRequestObj)
			return denyAdmissionRequest(admReview, errors.New("this is a Dev Sandbox enforced restriction. A user must be configured in either the cloudInitNoCloud or cloudInitConfigDrive volume."))
		}
	}

	// the user is configuring a VM without the 'runStrategy' configured or with it configured to Manual, allowing the request.
	return allowAdmissionRequest(admReview)
}

// validateCloudInitUsername validates that a username is configured in the cloudInit volume
func validateCloudInitUsername(unstructuredRequestObj *unstructured.Unstructured) error {
	volumes, volumesFound, err := unstructured.NestedSlice(unstructuredRequestObj.Object, "spec", "template", "spec", "volumes")
	if err != nil {
		return errors.Wrap(err, "failed to get volumes from VirtualMachine")
	}

	if !volumesFound {
		return errors.New("no volumes found in VirtualMachine")
	}

	// iterate through volumes to find cloudinitdisk
	for _, volume := range volumes {
		volumeDef, ok := volume.(map[string]interface{})
		if !ok {
			continue
		}

		if volumeDef["name"] == "cloudinitdisk" {
			// check supported cloudInit types
			supportedCloudInitConfigTypes := []string{"cloudInitNoCloud", "cloudInitConfigDrive"}

			for _, cloudInitType := range supportedCloudInitConfigTypes {
				cloudInitConfig, found := volumeDef[cloudInitType].(map[string]interface{})
				if !found {
					continue
				}

				// check if userData contains a username
				userData, ok := cloudInitConfig["userData"].(string)
				if !ok {
					// no userData found in this cloudInit config
					continue
				}

				// parse the userData YAML and check for username field
				if hasUsername(userData) {
					return nil // username found, validation passed
				}
			}
		}
	}

	return errors.New("no username configured in cloudInit volume")
}

// hasUsername checks if the userData contains a 'user' field
func hasUsername(userDataString string) bool {
	if !strings.HasPrefix(userDataString, "#cloud-config\n") {
		log.Info("userData does not contain the required '#cloud-config' header", "userData", userDataString)
		return false
	}
	userDataString = strings.TrimPrefix(userDataString, "#cloud-config\n")
	userDataString = strings.TrimSpace(userDataString)

	userData := map[string]interface{}{}
	if err := yaml.Unmarshal([]byte(userDataString), &userData); err != nil {
		log.Error(err, "failed to unmarshal userData YAML", "userData", userDataString)
		return false
	}

	// check if 'user' field exists and is not empty
	if user, found := userData["user"]; found {
		if userStr, ok := user.(string); ok && userStr != "" {
			return true
		}
	}

	return false
}
