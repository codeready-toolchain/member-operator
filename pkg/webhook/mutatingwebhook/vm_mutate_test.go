package mutatingwebhook

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestMutateVMSuccess(t *testing.T) {

	t.Run("no requests set", func(t *testing.T) {
		// given
		vmAdmReview := vmAdmissionReview(t, nil, nil)
		expectedResp := vmSuccessResponse()

		// when
		response := mutate(podLogger, vmAdmReview, vmMutator)

		// then
		verifySuccessfulResponse(t, response, expectedResp)
	})

	t.Run("only memory request is set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "")
		vmAdmReview := vmAdmissionReview(t, req, nil)
		expectedLimits := resourceList("1Gi", "")
		expectedResp := vmSuccessResponse(withPatch(t, expectedLimits))

		// when
		response := mutate(podLogger, vmAdmReview, vmMutator)

		// then
		verifySuccessfulResponse(t, response, expectedResp)
	})

	t.Run("memory and cpu requests are set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		vmAdmReview := vmAdmissionReview(t, req, nil)
		expectedLimits := resourceList("1Gi", "1")
		expectedResp := vmSuccessResponse(withPatch(t, expectedLimits))

		// when
		response := mutate(podLogger, vmAdmReview, vmMutator)

		// then
		verifySuccessfulResponse(t, response, expectedResp)
	})

	t.Run("memory and cpu requests are set but both limits are already set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		lim := resourceList("2Gi", "2")
		vmAdmReview := vmAdmissionReview(t, req, lim)
		expectedResp := vmSuccessResponse() // no patch expected because limits are already set

		// when
		response := mutate(podLogger, vmAdmReview, vmMutator)

		// then
		verifySuccessfulResponse(t, response, expectedResp)
	})

	t.Run("memory and cpu requests are set but memory limit is already set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		lim := resourceList("2Gi", "")
		vmAdmReview := vmAdmissionReview(t, req, lim)
		expectedLimits := resourceList("2Gi", "1") // expect cpu limit to be set to the value of the cpu request
		expectedResp := vmSuccessResponse(withPatch(t, expectedLimits))

		// when
		response := mutate(podLogger, vmAdmReview, vmMutator)

		// then
		verifySuccessfulResponse(t, response, expectedResp)
	})

	t.Run("memory and cpu requests are set but cpu limit is already set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		lim := resourceList("", "2")
		vmAdmReview := vmAdmissionReview(t, req, lim)
		expectedLimits := resourceList("1Gi", "2") // expect memory limit to be set to the value of the memory request
		expectedResp := vmSuccessResponse(withPatch(t, expectedLimits))

		// when
		response := mutate(podLogger, vmAdmReview, vmMutator)

		// then
		verifySuccessfulResponse(t, response, expectedResp)
	})
}

func TestMutateVMsFailsOnInvalidJson(t *testing.T) {
	// given
	rawJSON := []byte(`something wrong !`)
	expectedResp := expectedFailedResponse{
		auditAnnotationKey: "virtual_machines_mutating_webhook",
		errMsg:             "cannot unmarshal string into Go value of type struct",
	}

	// when
	response := mutate(vmLogger, rawJSON, vmMutator)

	// then
	verifyFailedResponse(t, response, expectedResp)
}

func TestMutateVmmFailsOnInvalidVM(t *testing.T) {
	// when
	rawJSON := []byte(`{
		"request": {
			"object": 111
		}
	}`)
	expectedResp := expectedFailedResponse{
		auditAnnotationKey: "virtual_machines_mutating_webhook",
		errMsg:             "cannot unmarshal number into Go value of type map[string]interface {}",
	}

	// when
	response := mutate(vmLogger, rawJSON, vmMutator)

	// then
	verifyFailedResponse(t, response, expectedResp)
}

type vmSuccessResponseOption func(*expectedSuccessResponse)

func withPatch(t *testing.T, expectedLimits map[string]interface{}) vmSuccessResponseOption {
	return func(resp *expectedSuccessResponse) {
		expectedLimitsJSONBytes, err := json.Marshal(expectedLimits)
		require.NoError(t, err)
		expectedLimitsJSON := string(expectedLimitsJSONBytes)
		resp.patch = fmt.Sprintf(`[{"op":"add","path":"/spec/template/spec/domain/resources/limits","value":%s}]`, expectedLimitsJSON)
	}
}

func vmSuccessResponse(options ...vmSuccessResponseOption) expectedSuccessResponse {
	resp := &expectedSuccessResponse{
		patch:              "[]",
		auditAnnotationKey: "virtual_machines_mutating_webhook",
		auditAnnotationVal: "the resource limits were set",
		uid:                "d68b4f8c-c62d-4e83-bd73-de991ab8a56a",
	}

	for _, opt := range options {
		opt(resp)
	}

	return *resp
}

func vmAdmissionReview(t *testing.T, requests, limits map[string]interface{}) []byte {
	unstructuredAdmReview := &unstructured.Unstructured{}
	err := unstructuredAdmReview.UnmarshalJSON([]byte(vmRawAdmissionReviewJSONTemplate))
	require.NoError(t, err)

	// set requests
	if requests != nil {
		err := unstructured.SetNestedMap(unstructuredAdmReview.Object, requests, "request", "object", "spec", "template", "spec", "domain", "resources", "requests")
		require.NoError(t, err)
	}

	// set limits
	if limits != nil {
		err := unstructured.SetNestedMap(unstructuredAdmReview.Object, limits, "request", "object", "spec", "template", "spec", "domain", "resources", "limits")
		require.NoError(t, err)
	}

	admReviewJSON, err := unstructuredAdmReview.MarshalJSON()
	require.NoError(t, err)

	return admReviewJSON
}

func resourceList(mem, cpu string) map[string]interface{} {
	req := map[string]interface{}{}
	if mem != "" {
		req["memory"] = mem
	}
	if cpu != "" {
		req["cpu"] = cpu
	}
	return req
}

var vmRawAdmissionReviewJSONTemplate = `{
    "kind": "AdmissionReview",
    "apiVersion": "admission.k8s.io/v1",
    "request": {
        "uid": "d68b4f8c-c62d-4e83-bd73-de991ab8a56a",
        "kind": {
            "group": "kubevirt.io",
            "version": "v1",
            "kind": "VirtualMachine"
        },
        "resource": {
            "group": "kubevirt.io",
            "version": "v1",
            "resource": "virtualmachines"
        },
        "requestKind": {
            "group": "kubevirt.io",
            "version": "v1",
            "kind": "VirtualMachine"
        },
        "requestResource": {
            "group": "kubevirt.io",
            "version": "v1",
            "resource": "virtualmachines"
        },
        "name": "rhel9-test",
        "namespace": "userabc-dev",
        "operation": "CREATE",
        "userInfo": {
            "username": "system:admin",
            "groups": [
                "system:masters",
                "system:authenticated"
            ]
        },
        "object": {
            "apiVersion": "kubevirt.io/v1",
            "kind": "VirtualMachine",
            "metadata": {
                "labels": {
                    "app": "rhel9-test",
                    "vm.kubevirt.io/template": "rhel9-server-small",
                    "vm.kubevirt.io/template.namespace": "openshift",
                    "vm.kubevirt.io/template.revision": "1",
                    "vm.kubevirt.io/template.version": "v0.25.0"
                },
                "name": "rhel9-test",
                "namespace": "userabc-dev"
            },
            "spec": {
                "dataVolumeTemplates": [
                    {
                        "apiVersion": "cdi.kubevirt.io/v1beta1",
                        "kind": "DataVolume",
                        "metadata": {
                            "creationTimestamp": null,
                            "name": "rhel9-test"
                        },
                        "spec": {
                            "sourceRef": {
                                "kind": "DataSource",
                                "name": "rhel9",
                                "namespace": "openshift-virtualization-os-images"
                            },
                            "storage": {
                                "resources": {
                                    "requests": {
                                        "storage": "30Gi"
                                    }
                                }
                            }
                        }
                    }
                ],
                "running": true,
                "template": {
                    "metadata": {
                        "annotations": {
                            "vm.kubevirt.io/flavor": "small",
                            "vm.kubevirt.io/os": "rhel9",
                            "vm.kubevirt.io/workload": "server"
                        },
                        "creationTimestamp": null,
                        "labels": {
                            "kubevirt.io/domain": "rhel9-test",
                            "kubevirt.io/size": "small"
                        }
                    },
                    "spec": {
                        "domain": {
                            "cpu": {
                                "cores": 1,
                                "sockets": 1,
                                "threads": 1
                            },
                            "devices": {
                                "disks": [
                                    {
                                        "disk": {
                                            "bus": "virtio"
                                        },
                                        "name": "rootdisk"
                                    },
                                    {
                                        "disk": {
                                            "bus": "virtio"
                                        },
                                        "name": "cloudinitdisk"
                                    }
                                ],
                                "interfaces": [
                                    {
                                        "macAddress": "02:24:d5:00:00:00",
                                        "masquerade": {},
                                        "model": "virtio",
                                        "name": "default"
                                    }
                                ],
                                "networkInterfaceMultiqueue": true,
                                "rng": {}
                            },
                            "features": {
                                "acpi": {},
                                "smm": {
                                    "enabled": true
                                }
                            },
                            "firmware": {
                                "bootloader": {
                                    "efi": {}
                                }
                            },
                            "machine": {
                                "type": "pc-q35-rhel9.2.0"
                            },
                            "resources": {
                            }
                        },
                        "evictionStrategy": "LiveMigrate",
                        "networks": [
                            {
                                "name": "default",
                                "pod": {}
                            }
                        ],
                        "terminationGracePeriodSeconds": 180,
                        "volumes": [
                            {
                                "dataVolume": {
                                    "name": "rhel9-test"
                                },
                                "name": "rootdisk"
                            },
                            {
                                "cloudInitNoCloud": {
                                    "userData": "#cloud-config\nuser: cloud-user\npassword: 5as2-8nbk-7a4c\nchpasswd: { expire: False }"
                                },
                                "name": "cloudinitdisk"
                            }
                        ]
                    }
                }
            }
        },
        "oldObject": null,
        "dryRun": false,
        "options": {
            "kind": "CreateOptions",
            "apiVersion": "meta.k8s.io/v1",
            "fieldManager": "kubectl-client-side-apply",
            "fieldValidation": "Ignore"
        }
    }
}`
