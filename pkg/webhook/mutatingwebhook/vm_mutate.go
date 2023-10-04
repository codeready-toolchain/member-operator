package mutatingwebhook

import (
	"encoding/json"
	"net/http"

	"github.com/codeready-toolchain/member-operator/pkg/webhook/mutatingwebhook/types"

	"github.com/pkg/errors"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var vmLogger = logf.Log.WithName("virtual_machines_mutating_webhook")

func HandleMutateVirtualMachines(w http.ResponseWriter, r *http.Request) {
	handleMutate(vmLogger, w, r, vmMutator)
}

func vmMutator(admReview admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {

	// unmarshal the object to be sure that it's a VirtualMachine
	vm := &types.VirtualMachine{}
	if err := json.Unmarshal(admReview.Request.Object.Raw, vm); err != nil {
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
	vmPatchItems = ensureLimits(vm, vmPatchItems)

	patchContent, err := json.Marshal(vmPatchItems)
	if err != nil {
		vmLogger.Error(err, "unable to marshal patch items for VirtualMachine", "AdmissionReview", admReview, "Patch-Items", vmPatchItems)
		return responseWithError(errors.Wrapf(err, "unable to marshal patch items for VirtualMachine - raw request object: %v", admReview.Request.Object.Raw))
	}
	resp.Patch = patchContent

	vmLogger.Info("the resource limits were set on the VirtualMachine", "vm-name", vm.Name, "namespace", vm.Namespace)
	return resp
}

func ensureLimits(vm *types.VirtualMachine, patchItems []map[string]interface{}) []map[string]interface{} {
	if vm.Spec.Template.Spec.Domain.Resources.Requests == nil {
		return patchItems
	}
	requests := vm.Spec.Template.Spec.Domain.Resources.Requests
	limits := vm.Spec.Template.Spec.Domain.Resources.Limits
	if limits == nil {
		limits = corev1.ResourceList{}
	}

	anyChanges := false
	for _, r := range []corev1.ResourceName{"memory", "cpu"} {
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
		vmLogger.Info("setting resource limits on the virtual machine", "vm-name", vm.Name, "namespace", vm.Namespace, "limits", limits)
	}
	return patchItems
}
