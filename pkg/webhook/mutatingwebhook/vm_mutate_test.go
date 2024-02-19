package mutatingwebhook

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	membercfg "github.com/codeready-toolchain/toolchain-common/pkg/configuration/memberoperatorconfig"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	testconfig "github.com/codeready-toolchain/toolchain-common/pkg/test/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/kubectl/pkg/scheme"
)

type cloudInitConfigType string

var cloudInitNoCloud cloudInitConfigType = "cloudInitNoCloud"
var cloudInitConfigDrive cloudInitConfigType = "cloudInitConfigDrive"

func TestVMMutator(t *testing.T) {
	initMemberConfig(t)
	t.Run("success", func(t *testing.T) {
		// given
		admReview := admissionReview(t, vmRawAdmissionReviewJSONTemplate, setVolumes(rootDiskVolume(), cloudInitVolume(cloudInitNoCloud, userDataWithoutSSHKey)))
		expectedVolumes := cloudInitVolume(cloudInitNoCloud, userDataWithSSHKey) // expect SSH key will be added to userData
		expectedVolumesPatch := []map[string]interface{}{volumesPatch(expectedVolumes)}

		// when
		actualResponse := vmMutator(admReview)

		// then
		assert.Equal(t, expectedVMMutateRespSuccess(t, expectedVolumesPatch...), *actualResponse)
	})

	t.Run("fail", func(t *testing.T) {
		// given
		admReview := admissionReview(t, vmRawAdmissionReviewJSONTemplate) // no volumes so expect it to fail

		// when
		actualResponse := vmMutator(admReview)

		// then
		assert.Nil(t, actualResponse.Patch)
		assert.Contains(t, actualResponse.Result.Message, "failed to update volume configuration for VirtualMachine")
		assert.Equal(t, "d68b4f8c-c62d-4e83-bd73-de991ab8a56a", string(actualResponse.UID))
		assert.False(t, actualResponse.Allowed)
	})
}

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

	t.Run("only domain:resources:memory request is set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setDomainResourcesRequests(req))

		expectedLimits := resourceList("1Gi", "")
		expectedPatchItems := []map[string]interface{}{addLimitsToResources(expectedLimits)} // only memory limits patch expected
		actualPatchItems := []map[string]interface{}{}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})

	t.Run("domain:resources:memory and domain:resources:cpu requests are set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setDomainResourcesRequests(req))
		expectedLimits := resourceList("1Gi", "1")
		expectedPatchItems := []map[string]interface{}{addLimitsToResources(expectedLimits)} // limits patch with memory and cpu expected
		actualPatchItems := []map[string]interface{}{}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})

	t.Run("domain:resources:memory and domain:resources:cpu requests are set but both limits are already set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		lim := resourceList("2Gi", "2")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setDomainResourcesRequests(req), setDomainResourcesLimits(lim))
		actualPatchItems := []map[string]interface{}{}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assert.Empty(t, actualPatchItems) // no limits patch expected because limits are already set
	})

	t.Run("domain:resources:memory and domain:resources:cpu requests are set but memory limit is already set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		lim := resourceList("2Gi", "")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setDomainResourcesRequests(req), setDomainResourcesLimits(lim))
		actualPatchItems := []map[string]interface{}{}
		expectedLimits := resourceList("2Gi", "1") // expect cpu limit to be set to the value of the cpu request
		expectedPatchItems := []map[string]interface{}{addLimitsToResources(expectedLimits)}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})

	t.Run("domain:resources:memory and domain:resources:cpu requests are set but cpu limit is already set", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		lim := resourceList("", "2")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setDomainResourcesRequests(req), setDomainResourcesLimits(lim))
		actualPatchItems := []map[string]interface{}{}
		expectedLimits := resourceList("1Gi", "2") // expect memory limit to be set to the value of the memory request
		expectedPatchItems := []map[string]interface{}{addLimitsToResources(expectedLimits)}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})

	t.Run("only domain:memory:guest is set", func(t *testing.T) {
		// given
		dMem := domainMemory("3Gi", "")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setDomainMemory(dMem))
		actualPatchItems := []map[string]interface{}{}
		expectedLimits := resourceList("3Gi", "") // expect memory limit to be set to the value of the domain memory guest value
		expectedPatchItems := []map[string]interface{}{addResourcesToDomain(expectedLimits)}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})

	t.Run("only domain:memory:maxguest is set", func(t *testing.T) {
		// given
		dMem := domainMemory("", "4Gi")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setDomainMemory(dMem))
		actualPatchItems := []map[string]interface{}{}
		expectedLimits := resourceList("4Gi", "") // expect memory limit to be set to the value of the domain memory max guest value
		expectedPatchItems := []map[string]interface{}{addResourcesToDomain(expectedLimits)}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})

	t.Run("domain:memory:guest and domain:memory:maxguest are both set", func(t *testing.T) {
		// given
		dMem := domainMemory("3Gi", "4Gi")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setDomainMemory(dMem))
		actualPatchItems := []map[string]interface{}{}
		expectedLimits := resourceList("4Gi", "") // expect memory limit to be set to the value of the domain memory max guest value
		expectedPatchItems := []map[string]interface{}{addResourcesToDomain(expectedLimits)}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})

	t.Run("domain:memory:guest and domain:resources:memory are both set", func(t *testing.T) {
		// given
		dMem := domainMemory("2Gi", "")
		req := resourceList("1Gi", "1")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setDomainResourcesRequests(req), setDomainMemory(dMem))
		actualPatchItems := []map[string]interface{}{}
		expectedLimits := resourceList("1Gi", "1") // expect memory limit to be set to the value of the domain resources request
		expectedPatchItems := []map[string]interface{}{addLimitsToResources(expectedLimits)}

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})

	t.Run("does not replace existing patches", func(t *testing.T) {
		// given
		req := resourceList("1Gi", "1")
		lim := resourceList("", "2")
		vmAdmReviewRequest := vmAdmReviewRequestObject(t, setDomainResourcesRequests(req), setDomainResourcesLimits(lim))
		existingPatch := map[string]interface{}{
			"op":    "add",
			"path":  "/spec/template/spec/test",
			"value": "testval",
		}
		actualPatchItems := []map[string]interface{}{existingPatch}
		expectedLimits := resourceList("1Gi", "2")
		expectedPatchItems := []map[string]interface{}{existingPatch, addLimitsToResources(expectedLimits)} // expect both patches to be present

		// when
		actualPatchItems = ensureLimits(vmAdmReviewRequest, actualPatchItems)

		// then
		assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
	})
}

func TestEnsureVolumeConfig(t *testing.T) {
	initMemberConfig(t)
	singleSSHKey := []string{"ssh-rsa tmpkey human@machine"}

	t.Run("success", func(t *testing.T) {
		// cloudinitdisk with cloudInitNoCloud config found
		t.Run("cloudinitdisk with cloudInitNoCloud config found", func(t *testing.T) {
			t.Run("userData found", func(t *testing.T) {
				// given
				vmAdmReviewRequest := vmAdmReviewRequestObject(t, setVolumes(rootDiskVolume(), cloudInitVolume(cloudInitNoCloud, userDataWithoutSSHKey))) // userData without SSH key
				actualPatchItems := []map[string]interface{}{}
				expectedVolumes := cloudInitVolume(cloudInitNoCloud, userDataWithSSHKey) // expect SSH key will be added to userData
				expectedPatchItems := []map[string]interface{}{volumesPatch(expectedVolumes)}

				// when
				actualPatchItems, err := ensureVolumeConfig(vmAdmReviewRequest, actualPatchItems)

				// then
				require.NoError(t, err)
				assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
			})

			t.Run("userData not found", func(t *testing.T) {
				// given
				vmAdmReviewRequest := vmAdmReviewRequestObject(t, setVolumes(rootDiskVolume(), cloudInitVolume(cloudInitNoCloud, ""))) // no userData
				actualPatchItems := []map[string]interface{}{}
				expectedVolumes := cloudInitVolume(cloudInitNoCloud, defaultUserData(singleSSHKey)) // expect default userData will be set
				expectedPatchItems := []map[string]interface{}{volumesPatch(expectedVolumes)}

				// when
				actualPatchItems, err := ensureVolumeConfig(vmAdmReviewRequest, actualPatchItems)

				// then
				require.NoError(t, err)
				assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
			})
		})

		// cloudinitdisk with cloudInitConfigDrive config found
		t.Run("cloudinitdisk with cloudInitConfigDrive config found", func(t *testing.T) {
			t.Run("userData found", func(t *testing.T) {
				// given
				vmAdmReviewRequest := vmAdmReviewRequestObject(t, setVolumes(rootDiskVolume(), cloudInitVolume(cloudInitConfigDrive, userDataWithoutSSHKey))) // userData without SSH key
				actualPatchItems := []map[string]interface{}{}
				expectedVolumes := cloudInitVolume(cloudInitConfigDrive, userDataWithSSHKey) // expect SSH key will be added to userData
				expectedPatchItems := []map[string]interface{}{volumesPatch(expectedVolumes)}

				// when
				actualPatchItems, err := ensureVolumeConfig(vmAdmReviewRequest, actualPatchItems)

				// then
				require.NoError(t, err)
				assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
			})

			t.Run("userData not found", func(t *testing.T) {
				// given
				vmAdmReviewRequest := vmAdmReviewRequestObject(t, setVolumes(rootDiskVolume(), cloudInitVolume(cloudInitConfigDrive, ""))) // no userData
				actualPatchItems := []map[string]interface{}{}
				expectedVolumes := cloudInitVolume(cloudInitConfigDrive, defaultUserData(singleSSHKey)) // expect default userData will be set
				expectedPatchItems := []map[string]interface{}{volumesPatch(expectedVolumes)}

				// when
				actualPatchItems, err := ensureVolumeConfig(vmAdmReviewRequest, actualPatchItems)

				// then
				require.NoError(t, err)
				assertPatchesEqual(t, expectedPatchItems, actualPatchItems)
			})
		})
	})

	t.Run("fail", func(t *testing.T) {

		t.Run("decode failure", func(t *testing.T) {
			// given
			unstructuredRequestObj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": "bad data",
				},
			}
			badObject := unstructuredRequestObj // object that cannot be decoded correctly
			actualPatchItems := []map[string]interface{}{}

			// when
			actualPatchItems, err := ensureVolumeConfig(badObject, actualPatchItems)

			// then
			require.EqualError(t, err, "failed to decode VirtualMachine: .spec.template accessor error: bad data is of the type string, expected map[string]interface{}")
			assert.Empty(t, actualPatchItems)
		})

		t.Run("volumes slice not found", func(t *testing.T) {
			// given
			vmAdmReviewRequest := vmAdmReviewRequestObject(t) // no volumes
			actualPatchItems := []map[string]interface{}{}

			// when
			actualPatchItems, err := ensureVolumeConfig(vmAdmReviewRequest, actualPatchItems)

			// then
			require.EqualError(t, err, "no volumes found")
			assert.Empty(t, actualPatchItems)
		})

		t.Run("cloudinitdisk volume not found", func(t *testing.T) {
			// given
			vmAdmReviewRequest := vmAdmReviewRequestObject(t, setVolumes(rootDiskVolume())) // missing cloudinitdisk volume
			actualPatchItems := []map[string]interface{}{}

			// when
			actualPatchItems, err := ensureVolumeConfig(vmAdmReviewRequest, actualPatchItems)

			// then
			require.EqualError(t, err, "no cloudInit volume found")
			assert.Empty(t, actualPatchItems)
		})
	})
}

func TestAddSSHKeyToUserData(t *testing.T) {
	// given
	for tcName, tc := range map[string]struct {
		sshKeys                  []string
		expectedWhenFresh        string
		expectedWhenExistingKeys string
	}{
		"single ssh key": {
			sshKeys:                  []string{"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCuyqYxl1up7uGK8KMFrTynx+FhOEm+zxqX3Yq1UgaABgQCuyqYxl1up7uGK8KMF human@machine"},
			expectedWhenFresh:        "#cloud-config\nchpasswd:\n  expire: false\npassword: 5as2-8nbk-7a4c\nssh_authorized_keys:\n- |\n  ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCuyqYxl1up7uGK8KMFrTynx+FhOEm+zxqX3Yq1UgaABgQCuyqYxl1up7uGK8KMF human@machine\nuser: cloud-user\n",
			expectedWhenExistingKeys: "#cloud-config\nchpasswd:\n  expire: false\npassword: 5as2-8nbk-7a4c\nssh_authorized_keys:\n- |\n  ssh-rsa tmpkey human@machine\n- |\n  ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCuyqYxl1up7uGK8KMFrTynx+FhOEm+zxqX3Yq1UgaABgQCuyqYxl1up7uGK8KMF human@machine\nuser: cloud-user\n",
		},
		"multiple ssh keys": {
			sshKeys: []string{
				"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCuyqYxl1up7uGK8KMFrTynx+FhOEm+zxqX3Yq1UgaABgQCuyqYxl1up7uGK8KMF human@machine",
				"ssh-ed25519 QCuyqYxl1up7uGK8KMFrTynx+FhOEm+zxqX3Yq1UgaABgQCuyqYxl1up7uGK8KMF beaver@dam",
			},
			expectedWhenFresh:        "#cloud-config\nchpasswd:\n  expire: false\npassword: 5as2-8nbk-7a4c\nssh_authorized_keys:\n- |\n  ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCuyqYxl1up7uGK8KMFrTynx+FhOEm+zxqX3Yq1UgaABgQCuyqYxl1up7uGK8KMF human@machine\n- |\n  ssh-ed25519 QCuyqYxl1up7uGK8KMFrTynx+FhOEm+zxqX3Yq1UgaABgQCuyqYxl1up7uGK8KMF beaver@dam\nuser: cloud-user\n",
			expectedWhenExistingKeys: "#cloud-config\nchpasswd:\n  expire: false\npassword: 5as2-8nbk-7a4c\nssh_authorized_keys:\n- |\n  ssh-rsa tmpkey human@machine\n- |\n  ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCuyqYxl1up7uGK8KMFrTynx+FhOEm+zxqX3Yq1UgaABgQCuyqYxl1up7uGK8KMF human@machine\n- |\n  ssh-ed25519 QCuyqYxl1up7uGK8KMFrTynx+FhOEm+zxqX3Yq1UgaABgQCuyqYxl1up7uGK8KMF beaver@dam\nuser: cloud-user\n",
		},
	} {
		t.Run(tcName, func(t *testing.T) {
			t.Run("no existing keys", func(t *testing.T) {
				// when
				userDataStr, err := addSSHKeysToUserData(userDataWithoutSSHKey, tc.sshKeys)

				// then
				require.NoError(t, err)
				require.True(t, strings.HasPrefix(userDataStr, "#cloud-config\n"))
				require.Equal(t, tc.expectedWhenFresh, userDataStr)
			})

			t.Run("pre-existing key", func(t *testing.T) {
				// when
				userDataStr, err := addSSHKeysToUserData(userDataWithSSHKey, tc.sshKeys)

				// then
				require.NoError(t, err)
				require.True(t, strings.HasPrefix(userDataStr, "#cloud-config\n"))
				// both keys should exist
				require.Equal(t, tc.expectedWhenExistingKeys, userDataStr)
			})
		})
	}
}

type admissionReviewOption func(t *testing.T, unstructuredAdmReview *unstructured.Unstructured)

func setDomainResourcesRequests(requests map[string]string) admissionReviewOption {
	return func(t *testing.T, unstructuredAdmReview *unstructured.Unstructured) {
		err := unstructured.SetNestedStringMap(unstructuredAdmReview.Object, requests, "request", "object", "spec", "template", "spec", "domain", "resources", "requests")
		require.NoError(t, err)
	}
}

func setDomainResourcesLimits(limits map[string]string) admissionReviewOption {
	return func(t *testing.T, unstructuredAdmReview *unstructured.Unstructured) {
		err := unstructured.SetNestedStringMap(unstructuredAdmReview.Object, limits, "request", "object", "spec", "template", "spec", "domain", "resources", "limits")
		require.NoError(t, err)
	}
}

func setDomainMemory(memory map[string]interface{}) admissionReviewOption {
	return func(t *testing.T, unstructuredAdmReview *unstructured.Unstructured) {
		err := unstructured.SetNestedMap(unstructuredAdmReview.Object, memory, "request", "object", "spec", "template", "spec", "domain", "memory")
		require.NoError(t, err)
	}
}

func setVolumes(volumes ...interface{}) admissionReviewOption {
	return func(t *testing.T, unstructuredAdmReview *unstructured.Unstructured) {
		err := unstructured.SetNestedSlice(unstructuredAdmReview.Object, volumes, "request", "object", "spec", "template", "spec", "volumes")
		require.NoError(t, err)
	}
}

func volumesPatch(expectedCloudInitDiskVolume map[string]interface{}) map[string]interface{} {
	volumes := []interface{}{
		rootDiskVolume(),
		expectedCloudInitDiskVolume,
	}

	return map[string]interface{}{
		"op":    "replace",
		"path":  "/spec/template/spec/volumes",
		"value": volumes,
	}
}

func resourceList(mem, cpu string) map[string]string {
	req := map[string]string{}
	if mem != "" {
		req["memory"] = mem
	}
	if cpu != "" {
		req["cpu"] = cpu
	}
	return req
}

func domainMemory(guest, maxGuest string) map[string]interface{} {
	dMem := map[string]interface{}{}
	if guest != "" {
		dMem["guest"] = guest
	}
	if maxGuest != "" {
		dMem["maxGuest"] = maxGuest
	}
	return dMem
}

func rootDiskVolume() map[string]interface{} {
	return map[string]interface{}{
		"dataVolume": map[string]interface{}{
			"name": "rhel9-test",
		},
		"name": "rootdisk",
	}
}

func cloudInitVolume(cicType cloudInitConfigType, userData string) map[string]interface{} {
	configType := string(cicType)
	volume := map[string]interface{}{
		configType: map[string]interface{}{},
		"name":     "cloudinitdisk",
	}
	if userData != "" {
		volume[configType].(map[string]interface{})["userData"] = userData
	}

	return volume
}

func vmAdmReviewRequestObject(t *testing.T, options ...admissionReviewOption) *unstructured.Unstructured {
	return admReviewRequestObject(t, vmRawAdmissionReviewJSONTemplate, options...)
}

func expectedVMMutateRespSuccess(t *testing.T, expectedPatches ...map[string]interface{}) admissionv1.AdmissionResponse {
	patchContent, err := json.Marshal(expectedPatches)
	require.NoError(t, err)

	patchType := admissionv1.PatchTypeJSONPatch
	return admissionv1.AdmissionResponse{
		Allowed: true,
		AuditAnnotations: map[string]string{
			"virtual_machines_mutating_webhook": "the resource limits and ssh key were set",
		},
		UID:       "d68b4f8c-c62d-4e83-bd73-de991ab8a56a",
		Patch:     patchContent,
		PatchType: &patchType,
	}
}

func initMemberConfig(t *testing.T) {
	os.Setenv("WATCH_NAMESPACE", test.MemberOperatorNs)
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	configObj := commonconfig.NewMemberOperatorConfigWithReset(t, testconfig.Webhook().WebhookSecretRef("webhook-secret").VMSSHKey("vmSSHKeys"))
	webhookSecret := test.CreateSecret("webhook-secret", test.MemberOperatorNs, map[string][]byte{
		"vmSSHKeys": []byte("ssh-rsa tmpkey human@machine"),
	})
	fakeClient := test.NewFakeClient(t, configObj, webhookSecret)
	_, err = membercfg.GetConfiguration(fakeClient)
	require.NoError(t, err)
}

const userDataWithoutSSHKey = "#cloud-config\nchpasswd:\n  expire: false\npassword: 5as2-8nbk-7a4c\nuser: cloud-user\n"
const userDataWithSSHKey = "#cloud-config\nchpasswd:\n  expire: false\npassword: 5as2-8nbk-7a4c\nssh_authorized_keys:\n- |\n  ssh-rsa tmpkey human@machine\nuser: cloud-user\n"

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
                            }
                        },
                        "evictionStrategy": "LiveMigrate",
                        "networks": [
                            {
                                "name": "default",
                                "pod": {}
                            }
                        ],
                        "terminationGracePeriodSeconds": 180
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
