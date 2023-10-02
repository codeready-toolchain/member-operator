package mutatingwebhook

import (
	"encoding/json"
	"net/http"

	"github.com/codeready-toolchain/member-operator/pkg/webhook/mutatingwebhook/types"

	"github.com/pkg/errors"
	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var vmM *vmMutator

func init() {
	vmM = &vmMutator{}
	vmM.createAdmissionReviewResponseFunc = vmM.createAdmissionReviewResponse
	vmM.log = logf.Log.WithName("virtual_machines_mutating_webhook")
}

func HandleMutateVirtualMachines(w http.ResponseWriter, r *http.Request) {
	vmM.handleMutate(w, r)
}

type vmMutator struct {
	mutatorCommon
}

func (m *vmMutator) createAdmissionReviewResponse(admReview v1.AdmissionReview) *v1.AdmissionResponse {
	// let's unmarshal the object to be sure that it's a pod
	vm := &types.VirtualMachine{}
	if err := json.Unmarshal(admReview.Request.Object.Raw, vm); err != nil {
		m.log.Error(err, "unable unmarshal VirtualMachine json object", "AdmissionReview", admReview)
		return responseWithError(errors.Wrapf(err, "unable unmarshal VirtualMachine json object - raw request object: %v", admReview.Request.Object.Raw))
	}

	patchType := v1.PatchTypeJSONPatch
	resp := &v1.AdmissionResponse{
		Allowed:   true,
		UID:       admReview.Request.UID,
		PatchType: &patchType,
	}
	resp.AuditAnnotations = map[string]string{
		"virtual_machines_mutating_webhook": "the resource limits were set",
	}

	// instead of changing the object we need to tell K8s how to change the object
	vmPatchItems := []map[string]interface{}{
		// {
		// 	"op":    "replace",
		// 	"path":  "/spec/template/spec/domain/resources/limits/memory",
		// 	"value": vm.Spec.Template.Spec.Domain.Resources.Requests.Memory().String(),
		// },
		// {
		// 	"op":    "replace",
		// 	"path":  "/spec/priority",
		// 	"value": priority,
		// },
	}

	// if memory request is defined and limit is missing then set the limit
	// if vm.Spec.Template.Spec.Domain.Resources.Requests.Memory().String() != "0" {
	// 	m.log.Info("setting memory request on the virtual machine", "vm-name", vm.Name, "namespace", vm.Namespace)
	// 	limits := corev1.ResourceList{
	// 		"memory": *vm.Spec.Template.Spec.Domain.Resources.Requests.Memory(),
	// 	}
	// 	vmPatchItems = append(vmPatchItems,
	// 		map[string]interface{}{
	// 			"op":    "add",
	// 			"path":  "/spec/template/spec/domain/resources/limits",
	// 			"value": limits,
	// 		})
	// }
	vmPatchItems = m.ensureLimits(vm, vmPatchItems)

	// if cpu request is defined and limit is missing then set the limit
	// if vm.Spec.Template.Spec.Domain.Resources.Requests.Cpu().String() != "0" {
	// 	m.log.Info("setting cpu request on the virtual machine", "vm-name", vm.Name, "namespace", vm.Namespace)
	// 	limits := corev1.ResourceList{
	// 		"cpu": *vm.Spec.Template.Spec.Domain.Resources.Requests.Cpu(),
	// 	}
	// 	vmPatchItems = append(vmPatchItems,
	// 		map[string]interface{}{
	// 			"op":    "add",
	// 			"path":  "/spec/template/spec/domain/resources/limits",
	// 			"value": limits,
	// 		})
	// }

	patchContent, err := json.Marshal(vmPatchItems)
	if err != nil {
		m.log.Error(err, "unable to marshal patch items for VirtualMachine", "AdmissionReview", admReview, "Patch-Items", vmPatchItems)
		return responseWithError(errors.Wrapf(err, "unable to marshal patch items for VirtualMachine - raw request object: %v", admReview.Request.Object.Raw))
	}
	resp.Patch = patchContent

	m.log.Info("the resource limits were set on the VirtualMachine", "vm-name", vm.Name, "namespace", vm.Namespace)
	return resp
}

func (m *vmMutator) ensureLimits(vm *types.VirtualMachine, patchItems []map[string]interface{}) []map[string]interface{} {
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
		m.log.Info("setting resource limits on the virtual machine", "vm-name", vm.Name, "namespace", vm.Namespace, "limits", limits)
	}
	return patchItems
}
