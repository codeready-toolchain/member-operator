package mutatingwebhook

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	membercfg "github.com/codeready-toolchain/toolchain-common/pkg/configuration/memberoperatorconfig"

	"github.com/pkg/errors"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
)

const (
	cloudConfigHeader = "#cloud-config\n"
)

var vmLogger = logf.Log.WithName("virtual_machines_mutating_webhook")

var sandboxToleration = map[string]interface{}{
	"effect":   "NoSchedule",
	"key":      "sandbox-cnv",
	"operator": "Exists",
}

func HandleMutateVirtualMachines(w http.ResponseWriter, r *http.Request) {
	handleMutate(vmLogger, w, r, vmMutator)
}

func vmMutator(admReview admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	unstructuredRequestObj := &unstructured.Unstructured{}
	if err := unstructuredRequestObj.UnmarshalJSON(admReview.Request.Object.Raw); err != nil {
		vmLogger.Error(err, "failed to unmarshal VirtualMachine json object", "AdmissionReview", admReview)
		return responseWithError(admReview.Request.UID, errors.Wrap(err, "failed to unmarshal VirtualMachine json object"))
	}

	patchType := admissionv1.PatchTypeJSONPatch
	resp := &admissionv1.AdmissionResponse{
		Allowed:   true,
		UID:       admReview.Request.UID,
		PatchType: &patchType,
	}
	resp.AuditAnnotations = map[string]string{
		"virtual_machines_mutating_webhook": "the resource limits and ssh key were set",
	}

	// instead of changing the object we need to tell K8s how to change the object via patch
	vmPatchItems := []map[string]interface{}{}

	// ensure limits are set in a best effort approach, if the limits are not set for any reason the request will still be allowed
	vmPatchItems = ensureLimits(unstructuredRequestObj, vmPatchItems)

	// ensure tolerations are set so vm can be scheduled to the metal node
	vmPatchItems = ensureTolerations(unstructuredRequestObj, vmPatchItems)

	// ensure cloud-init config is set, if the cloud-init config cannot be set for any reason the request will be blocked
	var cloudInitConfigErr error
	vmPatchItems, cloudInitConfigErr = ensureVolumeConfig(unstructuredRequestObj, vmPatchItems)
	if cloudInitConfigErr != nil {
		vmLogger.Error(cloudInitConfigErr, "failed to update volume configuration for VirtualMachine", "AdmissionReview", admReview, "Patch-Items", vmPatchItems)
		return responseWithError(admReview.Request.UID, errors.Wrapf(cloudInitConfigErr, "failed to update volume configuration for VirtualMachine"))
	}

	patchContent, err := json.Marshal(vmPatchItems)
	if err != nil {
		vmLogger.Error(err, "failed to marshal patch items for VirtualMachine", "AdmissionReview", admReview, "Patch-Items", vmPatchItems)
		return responseWithError(admReview.Request.UID, errors.Wrapf(err, "failed to marshal patch items for VirtualMachine"))
	}
	resp.Patch = patchContent

	return resp
}

// ensureVolumeConfig ensures the cloud-init config is set on the VirtualMachine
func ensureVolumeConfig(unstructuredRequestObj *unstructured.Unstructured, patchItems []map[string]interface{}) ([]map[string]interface{}, error) {
	volumes, volumesFound, err := unstructured.NestedSlice(unstructuredRequestObj.Object, "spec", "template", "spec", "volumes")
	if err != nil {
		vmLogger.Error(err, "failed to decode VirtualMachine resource", "VirtualMachine", unstructuredRequestObj)
		return patchItems, errors.Wrapf(err, "failed to decode VirtualMachine")
	}

	if !volumesFound {
		return patchItems, fmt.Errorf("no volumes found")
	}

	// get SSH keys from configuration
	memberConfig := membercfg.GetCachedConfiguration()
	sshKeys := strings.Split(memberConfig.Webhook().VMSSHKey(), ",")
	if len(sshKeys) == 0 || (len(sshKeys) == 1 && sshKeys[0] == "") {
		return patchItems, fmt.Errorf("invalid VM webhook configuration")
	}

	// iterate through volumes to find cloudinitdisk
	cloudinitdiskVolumeFound := false
	for i, volume := range volumes {
		volumeDef := volume.(map[string]interface{})
		if volumeDef["name"] == "cloudinitdisk" {
			cloudinitdiskVolumeFound = true

			supportedCloudInitConfigTypes := []string{"cloudInitNoCloud", "cloudInitConfigDrive"}

			// check if one of the supported volume types is already defined
			for _, supportedCloudInitConfigType := range supportedCloudInitConfigTypes {
				cloudInitConfig, cloudInitConfigTypeFound := volumeDef[supportedCloudInitConfigType].(map[string]interface{})
				if !cloudInitConfigTypeFound {
					continue
				}

				vmLogger.Info("Found cloud init config", "type", supportedCloudInitConfigType)
				userData, ok := cloudInitConfig["userData"]
				if ok {
					// userData is defined, append the ssh key
					updatedUserData, err := addSSHKeysToUserData(userData.(string), sshKeys)
					if err != nil {
						return patchItems, errors.Wrapf(err, "failed to add ssh key to userData")
					}
					cloudInitConfig["userData"] = updatedUserData
				} else {
					// no userData defined, set the default
					cloudInitConfig["userData"] = defaultUserData(sshKeys)
					vmLogger.Info("setting default userData")
				}

				volumeDef[supportedCloudInitConfigType] = cloudInitConfig
				volumes[i] = volumeDef // update the volume definition

				patchItems = append(patchItems,
					map[string]interface{}{
						"op":    "replace",
						"path":  "/spec/template/spec/volumes",
						"value": volumes,
					})

				vmLogger.Info("Added ssh key patch for userData")
				return patchItems, nil
			}
		}
	}

	if !cloudinitdiskVolumeFound {
		return patchItems, fmt.Errorf("no cloudInit volume found")
	}
	return patchItems, nil
}

// addSSHKeysToUserData parses the userData YAML and adds the provided ssh key to it or returns an error otherwise
func addSSHKeysToUserData(userDataString string, sshKeys []string) (string, error) {
	userData := map[string]interface{}{}

	if err := yaml.Unmarshal([]byte(userDataString), &userData); err != nil {
		return "", err
	}

	for _, sshKey := range sshKeys {
		_, authorizedKeysFound := userData["ssh_authorized_keys"]
		sshValue := sshKey
		// ensure the ssh key has a newline at the end so that it is properly unmarshalled later
		if !strings.HasSuffix(sshKey, "\n") {
			sshValue = sshKey + "\n"
		}

		if authorizedKeysFound {
			authKeys := userData["ssh_authorized_keys"].([]interface{})
			// append the key to the existing list
			userData["ssh_authorized_keys"] = append(authKeys, sshValue)
		} else {
			// create a new list with the key
			userData["ssh_authorized_keys"] = []interface{}{sshValue}
		}
	}

	updatedYaml, err := yaml.Marshal(userData)
	if err != nil {
		return "", err
	}

	// the cloud config header '#cloud-config' is lost during unmarshalling (all yaml comments are lost) so it needs to be prepended before returning
	return cloudConfigHeader + string(updatedYaml), nil
}

// ensureLimits ensures resource limits are set on the VirtualMachine if requests are set, this is a workaround for https://issues.redhat.com/browse/CNV-28746 (https://issues.redhat.com/browse/CNV-32069)
// The issue is that if the namespace has LimitRanges defined and the VirtualMachine resource does not have resource limits defined then it will use the LimitRanges which may be less than requested
// resources and the VirtualMachine will fail to start.
// This should be removed once https://issues.redhat.com/browse/CNV-32069 is complete.
func ensureLimits(unstructuredObj *unstructured.Unstructured, patchItems []map[string]interface{}) []map[string]interface{} {

	_, domainResourcesFound, err := unstructured.NestedMap(unstructuredObj.Object, "spec", "template", "spec", "domain", "resources")
	if err != nil {
		vmLogger.Error(err, "unable to get resources from VirtualMachine", "VirtualMachine", unstructuredObj)
		return patchItems
	}

	domainResourceReq, domainResourcesReqFound, err := unstructured.NestedStringMap(unstructuredObj.Object, "spec", "template", "spec", "domain", "resources", "requests")
	if err != nil {
		vmLogger.Error(err, "unable to get requests from VirtualMachine", "VirtualMachine", unstructuredObj)
		return patchItems
	}

	domainMemory, domainMemoryReqFound, err := unstructured.NestedStringMap(unstructuredObj.Object, "spec", "template", "spec", "domain", "memory")
	if err != nil {
		vmLogger.Error(err, "unable to get domain memory request from VirtualMachine", "VirtualMachine", unstructuredObj)
		return patchItems
	}

	if !domainResourcesReqFound && !domainMemoryReqFound {
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
		_, isRequestDefined := domainResourceReq[r]
		if !isLimitDefined && isRequestDefined {
			limits[r] = domainResourceReq[r]
			anyChanges = true
		}
	}

	// if domain memory is specified but memory limits are still not set, then set the limit to the same value as the requested guest OS memory
	if domainMemoryReqFound && limits["memory"] == "" {
		// use maxGuest value if it's defined, otherwise use guest value
		if domainMemory["maxGuest"] != "" {
			limits["memory"] = domainMemory["maxGuest"]
			anyChanges = true
		} else if domainMemory["guest"] != "" {
			limits["memory"] = domainMemory["guest"]
			anyChanges = true
		}
	}

	if anyChanges {
		if domainResourcesFound {
			vmLogger.Info("domain resources found", "vm-name", unstructuredObj.GetName(), "namespace", unstructuredObj.GetNamespace())
			patchItems = append(patchItems, addLimitsToResources(limits))
		} else {
			vmLogger.Info("domain resources not found", "vm-name", unstructuredObj.GetName(), "namespace", unstructuredObj.GetNamespace())
			patchItems = append(patchItems, addResourcesToDomain(limits))
		}
		vmLogger.Info("setting resource limits on the virtual machine", "vm-name", unstructuredObj.GetName(), "namespace", unstructuredObj.GetNamespace(), "limits", limits)
	}
	return patchItems
}

// ensureTolerations ensures tolerations are set on the VirtualMachine
func ensureTolerations(unstructuredRequestObj *unstructured.Unstructured, patchItems []map[string]interface{}) []map[string]interface{} {
	// get existing tolerations, if any
	tolerations, _, err := unstructured.NestedSlice(unstructuredRequestObj.Object, "spec", "template", "spec", "tolerations")
	if err != nil {
		vmLogger.Error(err, "unable to get tolerations from VirtualMachine", "VirtualMachine", unstructuredRequestObj)
		return patchItems
	}

	// no need to check original contents of tolerations, if there were existing tolerations the sandbox one will be appended; if there were no tolerations then the patch will add the sandbox toleration
	tolerations = append(tolerations, sandboxToleration)
	patchItems = append(patchItems, addTolerations(tolerations))

	return patchItems
}

func defaultUserData(sshKeys []string) string {
	authorizedKeys := fmt.Sprintf("ssh_authorized_keys: [%s]\n", sshKeys)
	return strings.Join(
		append([]string{cloudConfigHeader}, authorizedKeys), "\n")
}

func addLimitsToResources(limits map[string]string) map[string]interface{} {
	return map[string]interface{}{
		"op":    "add",
		"path":  "/spec/template/spec/domain/resources/limits",
		"value": limits,
	}
}

func addResourcesToDomain(limits map[string]string) map[string]interface{} {
	// wrap limits in resources
	resources := map[string]interface{}{
		"limits": limits,
	}

	return map[string]interface{}{
		"op":    "add",
		"path":  "/spec/template/spec/domain/resources",
		"value": resources,
	}
}

func addTolerations(tolerations []interface{}) map[string]interface{} {
	return map[string]interface{}{
		"op":    "add",
		"path":  "/spec/template/spec/tolerations",
		"value": tolerations,
	}
}
