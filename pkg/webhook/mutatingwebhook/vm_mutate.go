package mutatingwebhook

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/pkg/errors"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
)

const (
	cloudConfigHeader = "#cloud-config"
)

var vmLogger = logf.Log.WithName("virtual_machines_mutating_webhook")

func HandleMutateVirtualMachines(w http.ResponseWriter, r *http.Request) {
	handleMutate(vmLogger, w, r, vmMutator)
}

func vmMutator(admReview admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {

	unstructuredObj := &unstructured.Unstructured{}
	if err := unstructuredObj.UnmarshalJSON(admReview.Request.Object.Raw); err != nil {
		vmLogger.Error(err, "unable unmarshal VirtualMachine json object", "AdmissionReview", admReview)
		return responseWithError(errors.Wrapf(err, "unable unmarshal VirtualMachine json object - raw request object: %v", admReview.Request.Object.Raw))
	}

	patchType := admissionv1.PatchTypeJSONPatch
	resp := &admissionv1.AdmissionResponse{
		Allowed:   true,
		UID:       admReview.Request.UID,
		PatchType: &patchType,
	}
	resp.AuditAnnotations = map[string]string{
		"virtual_machines_mutating_webhook": "the resource limits were set",
	}

	// instead of changing the object we need to tell K8s how to change the object
	vmPatchItems := []map[string]interface{}{}

	// ensure limits are set
	vmPatchItems = ensureLimits(unstructuredObj, vmPatchItems)

	// ensure cloud-init config is set
	vmPatchItems = ensureCloudInitConfig(unstructuredObj, vmPatchItems)

	patchContent, err := json.Marshal(vmPatchItems)
	if err != nil {
		vmLogger.Error(err, "unable to marshal patch items for VirtualMachine", "AdmissionReview", admReview, "Patch-Items", vmPatchItems)
		return responseWithError(errors.Wrapf(err, "unable to marshal patch items for VirtualMachine - raw request object: %v", admReview.Request.Object.Raw))
	}
	resp.Patch = patchContent

	return resp
}

// ensureCloudInitConfig ensures the cloud-init config is set on the VirtualMachine
func ensureCloudInitConfig(unstructuredObj *unstructured.Unstructured, patchItems []map[string]interface{}) []map[string]interface{} {
	volumes, volumesFound, err := unstructured.NestedSlice(unstructuredObj.Object, "spec", "template", "spec", "volumes")
	if err != nil {
		vmLogger.Error(err, "unable to get volumes from VirtualMachine", "VirtualMachine", unstructuredObj)
		return patchItems
	}

	if !volumesFound {
		return patchItems
	}

	sshKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCuyqYxl1up7uGK8KMFrTynx+FhOEm+zxqX3Yq1UglUkqdferPWtEC47PcHUNFXhysGvTnvORVbR70EVZAtJTJEzSpHesNRwWu0ZBA+OKDHl3nI2o+F1xGAQN+8oGWcj8zbQxn6JDQW3uJBphSknJ7gbXNjg8OWepIjHXLDPxk/Y9rB5vRPzgbFnE1XNPWzVnjxMjHUU/15LWUgm7GWR+2ZRcsyu6Is2RS8ez+5y+NRoh8+9Uh4h80bvTuDFTesjh/5Gp8SlbuFuT103GVjQAe65hFJPHfqoiqByFvaOQpQc/Fsuq+N1T3xj/6Q2O/Jyt/6rfz+xZZuipGJJqq4Si8Vy+7U+e04KJCj0u9eZIBjQ0ucHOp6GDtYGHriAMxeYD6hqE01b5nBvl42I0d0wBnMJOOEJYuInOoiOCZ66LX96wXUm++8uGndqtBnsobN5pvjBHcHiSmCVSu52VeuMJA+AibCoqaZxd7ZZPLGGq7ZVoI7QhjAFtHO4rpIl9qleik= rajiv@rsenthil-mac"

	// iterate through volumes to find cloudinitdisk
	vmLogger.Info("volumes found", "count", len(volumes))
	cloudinitdiskVolumeFound := false
	for i, volume := range volumes {
		volumeDef := volume.(map[string]interface{})
		if volumeDef["name"] == "cloudinitdisk" {
			vmLogger.Info("cloudinitdisk found")
			cloudinitdiskVolumeFound = true
			// look for cloudInitNoCloud case
			// cloudInitNoCloud, cloudInitNoCloudFound, err := unstructured.NestedMap(volumeMap, "cloudInitNoCloud")
			// if err != nil {
			// 	vmLogger.Error(err, "unable to get cloudInitNoCloud from VirtualMachine")
			// 	return patchItems
			// }
			cloudInitNoCloud, cloudInitNoCloudFound := volumeDef["cloudInitNoCloud"].(map[string]interface{})
			if cloudInitNoCloudFound {
				vmLogger.Info("cloudInitNoCloud found")
				userData, ok := cloudInitNoCloud["userData"]
				if !ok {
					cloudInitNoCloud["userData"] = defaultUserData(sshKey)
					vmLogger.Info("set default userData in cloudInitNoCloud")
				} else {
					updatedUserData, err := addSSHKeyToUserData(userData.(string), sshKey)
					if err != nil {
						vmLogger.Error(err, "unable to add SSH key to cloudInitNoCloud user data")
					}
					cloudInitNoCloud["userData"] = updatedUserData
					volumeDef["cloudInitNoCloud"] = cloudInitNoCloud

					vmLogger.Info("updated userData in cloudInitNoCloud")

					volumes[i] = volumeDef // update the volume definition
					patchItems = append(patchItems,
						map[string]interface{}{
							"op":    "replace",
							"path":  "/spec/template/spec/volumes",
							"value": volumes,
						})
				}

				return patchItems
			}

			// look for cloudInitConfigDrive case
			cloudInitConfigDrive, cloudInitConfigDriveFound, err := unstructured.NestedMap(volumeDef, "cloudInitConfigDrive")
			if err != nil {
				vmLogger.Error(err, "unable to get cloudInitConfigDrive from VirtualMachine")
				return patchItems
			}
			if cloudInitConfigDriveFound {
				vmLogger.Info("cloudInitConfigDrive found")
				userData, ok := cloudInitConfigDrive["userData"]
				if !ok {
					cloudInitConfigDrive["userData"] = defaultUserData(sshKey)
					vmLogger.Info("set default userData in cloudInitConfigDrive")
				} else {
					updatedUserData, err := addSSHKeyToUserData(userData.(string), sshKey)
					if err != nil {
						vmLogger.Error(err, "unable to add SSH key to cloudInitConfigDrive user data")
					}
					cloudInitConfigDrive["userData"] = updatedUserData
					vmLogger.Info("updated userData in cloudInitConfigDrive")
				}
				return patchItems
			}
			return patchItems
		}
	}

	if !cloudinitdiskVolumeFound {
		// TODO add cloudInitNoCloud volume
	}
	return patchItems
}

func addSSHKeyToUserData(userDataString string, sshKey string) (string, error) {
	jsonBytes, err := yaml.YAMLToJSON([]byte(userDataString))
	if err != nil {
		return "", err
	}

	userData := map[string]interface{}{}
	if err := json.Unmarshal(jsonBytes, &userData); err != nil {
		return "", err
	}

	authorizedKeys, authorizedKeysFound := userData["ssh_authorized_keys"].([]string)
	if authorizedKeysFound {
		// append the key to the existing list
		userData["ssh_authorized_keys"] = append(authorizedKeys, sshKey)
	} else {
		// create a new list with the key
		userData["ssh_authorized_keys"] = []string{sshKey}
	}

	updatedJSON, err := json.Marshal(userData)
	if err != nil {
		return "", err
	}

	updatedYaml, err := yaml.JSONToYAML(updatedJSON)
	if err != nil {
		return "", err
	}
	return string(updatedYaml), nil
}

// ensureLimits ensures resource limits are set on the VirtualMachine if requests are set, this is a workaround for https://issues.redhat.com/browse/CNV-28746 (https://issues.redhat.com/browse/CNV-32069)
// The issue is that if the namespace has LimitRanges defined and the VirtualMachine resource does not have resource limits defined then it will use the LimitRanges which may be less than requested
// resources and the VirtualMachine will fail to start.
// This should be removed once https://issues.redhat.com/browse/CNV-32069 is complete.
func ensureLimits(unstructuredObj *unstructured.Unstructured, patchItems []map[string]interface{}) []map[string]interface{} {

	requests, reqFound, err := unstructured.NestedStringMap(unstructuredObj.Object, "spec", "template", "spec", "domain", "resources", "requests")
	if err != nil {
		vmLogger.Error(err, "unable to get requests from VirtualMachine", "VirtualMachine", unstructuredObj)
		return patchItems
	}

	if !reqFound {
		return patchItems
	}

	limits, limFound, err := unstructured.NestedStringMap(unstructuredObj.Object, "spec", "template", "spec", "domain", "resources", "limits")
	if err != nil {
		vmLogger.Error(err, "unable to get limits from VirtualMachine", "VirtualMachine", unstructuredObj)
		return patchItems
	}

	if limits == nil || !limFound {
		limits = map[string]string{}
	}

	// if the limit is not defined but the request is, then set the limit to the same value as the request
	anyChanges := false
	for _, r := range []string{"memory", "cpu"} {
		_, isLimitDefined := limits[r]
		_, isRequestDefined := requests[r]
		if !isLimitDefined && isRequestDefined {
			limits[r] = requests[r]
			anyChanges = true
		}
	}

	if anyChanges {
		patchItems = append(patchItems,
			map[string]interface{}{
				"op":    "add",
				"path":  "/spec/template/spec/domain/resources/limits",
				"value": limits,
			})
		vmLogger.Info("setting resource limits on the virtual machine", "vm-name", unstructuredObj.GetName(), "namespace", unstructuredObj.GetNamespace(), "limits", limits)
	}
	return patchItems
}

func defaultUserData(sshKey string) string {
	authorizedKeys := fmt.Sprintf("ssh_authorized_keys: [%s]\n", sshKey)
	return strings.Join(
		append([]string{cloudConfigHeader}, authorizedKeys), "\n")
}
