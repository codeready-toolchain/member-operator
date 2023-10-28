package mutatingwebhook

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestEnsureLimits(t *testing.T) {

	t.Run("no requests set", func(t *testing.T) {
		// given
		vmAdmReviewRequest := vmAdmReviewRequestObject(t)
		actualPatchItems := []map[string]interface{}{}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assert.Empty(t, actualPatchItems) // no limits patch expected because no requests were set
	})

	t.Run("only memory request is set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setRequests(t, req))

		expectedLimits := resourceList("1Gi", "")
		expectedPatchItems := combinePatches(limitsPatch(expectedLimits)) // only memory limits patch expected
		actualPatchItems := []map[string]interface{}{}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})

	t.Run("memory and cpu requests are set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setRequests(t, req))
		expectedLimits := resourceList("1Gi", "1")
		expectedPatchItems := combinePatches(limitsPatch(expectedLimits)) // limits patch with memory and cpu expected
		actualPatchItems := []map[string]interface{}{}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})

	t.Run("memory and cpu requests are set but both limits are already set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		lim := resourceList("2Gi", "2")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setRequests(t, req), setLimits(t, lim))
		actualPatchItems := []map[string]interface{}{}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assert.Empty(t, actualPatchItems) // no limits patch expected because limits are already set
	})

	t.Run("memory and cpu requests are set but memory limit is already set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		lim := resourceList("2Gi", "")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setRequests(t, req), setLimits(t, lim))
		actualPatchItems := []map[string]interface{}{}
		expectedLimits := resourceList("2Gi", "1") // expect cpu limit to be set to the value of the cpu request
		expectedPatchItems := combinePatches(limitsPatch(expectedLimits))

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})

	t.Run("memory and cpu requests are set but cpu limit is already set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		lim := resourceList("", "2")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setRequests(t, req), setLimits(t, lim))
		actualPatchItems := []map[string]interface{}{}
		expectedLimits := resourceList("1Gi", "2") // expect memory limit to be set to the value of the memory request
		expectedPatchItems := combinePatches(limitsPatch(expectedLimits))

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})

	// t.Run("does not replace existing patches", func(t *testing.T) {

	// })
}

// func TestEnsureVolumeConfig(t *testing.T) {

// }

func TestAddSSHKeyToUserData(t *testing.T) {
	t.Run("no existing keys", func(t *testing.T) {
		// given
		sshKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCuyqYxl1up7uGK8KMFrTynx+FhOEm+zxqX3Yq1UgaABgQCuyqYxl1up7uGK8KMF human@machine"

		// when
		userDataStr, err := addSSHKeyToUserData(userDataSample, sshKey)

		// then
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(userDataStr, "#cloud-config\n"))
		require.Equal(t, "#cloud-config\nchpasswd:\n  expire: false\npassword: 5as2-8nbk-7a4c\nssh_authorized_keys:\n- |\n  ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCuyqYxl1up7uGK8KMFrTynx+FhOEm+zxqX3Yq1UgaABgQCuyqYxl1up7uGK8KMF human@machine\nuser: cloud-user\n", userDataStr)
	})

	t.Run("", func(t *testing.T) {

	})
	t.Run("", func(t *testing.T) {

	})
}

func addPatchToResponse(t *testing.T, resp *admissionv1.AdmissionResponse, expectedPatch map[string]interface{}) {
	respPatches := []map[string]interface{}{}

	if string(resp.Patch) != "" {
		err := json.Unmarshal(resp.Patch, &respPatches)
		require.NoError(t, err)
	}

	respPatches = append(respPatches, expectedPatch)

	updatedPatchContent, err := json.Marshal(respPatches)
	require.NoError(t, err)

	resp.Patch = updatedPatchContent
}

type admissionReviewOption func(t *testing.T, unstructuredAdmReview *unstructured.Unstructured)

func setRequests(t *testing.T, requests map[string]interface{}) admissionReviewOption {
	return func(t *testing.T, unstructuredAdmReview *unstructured.Unstructured) {
		err := unstructured.SetNestedMap(unstructuredAdmReview.Object, requests, "request", "object", "spec", "template", "spec", "domain", "resources", "requests")
		require.NoError(t, err)
	}
}

func setLimits(t *testing.T, limits map[string]interface{}) admissionReviewOption {
	return func(t *testing.T, unstructuredAdmReview *unstructured.Unstructured) {
		err := unstructured.SetNestedMap(unstructuredAdmReview.Object, limits, "request", "object", "spec", "template", "spec", "domain", "resources", "limits")
		require.NoError(t, err)
	}
}

func limitsPatch(expectedLimits map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"op":    "add",
		"path":  "/spec/template/spec/domain/resources/limits",
		"value": expectedLimits,
	}
}

func volumesPatch(expectedCloudInitDiskVolume map[string]interface{}) map[string]interface{} {
	volumes := []interface{}{
		rootDisk(),
		expectedCloudInitDiskVolume,
	}
	// volumes = append(volumes, )

	return map[string]interface{}{
		"op":    "replace",
		"path":  "/spec/template/spec/volumes",
		"value": volumes,
	}
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

func rootDisk() map[string]interface{} {
	return map[string]interface{}{
		"dataVolume": map[string]interface{}{
			"name": "rhel9-test",
		},
		"name": "rootdisk",
	}
}

func expectedCloudInitVolumeWithSSH() map[string]interface{} {
	return map[string]interface{}{
		"cloudInitNoCloud": map[string]interface{}{
			"userData": fmt.Sprintf("#cloud-config\nchpasswd:\n  expire: false\npassword: 5as2-8nbk-7a4c\nssh_authorized_keys:\n- |\n  ssh-rsa tmpkey human@machine\nuser: cloud-user\n"),
		},
		"name": "cloudinitdisk",
	}
}

func vmAdmReviewRequestObject(t *testing.T, options ...admissionReviewOption) *unstructured.Unstructured {
	return admReviewRequestObject(t, vmRawAdmissionReviewJSONTemplate, options...)
}

const userDataSample = "#cloud-config\nuser: cloud-user\npassword: 5as2-8nbk-7a4c\nchpasswd: { expire: False }"

var vmRawAdmissionReviewJSONTemplate = []byte(`{
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
}`)
