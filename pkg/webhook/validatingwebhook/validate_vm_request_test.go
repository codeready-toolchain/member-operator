package validatingwebhook

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"text/template"

	"github.com/codeready-toolchain/member-operator/pkg/webhook/validatingwebhook/test"

	userv1 "github.com/openshift/api/user/v1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHandleValidateVMAdmissionRequest(t *testing.T) {
	v := newVMRequestValidator(t)
	// given
	ts := httptest.NewServer(http.HandlerFunc(v.HandleValidate))
	defer ts.Close()

	t.Run("RunStrategy validation", func(t *testing.T) {

		t.Run("sandbox user trying to UPDATE a VM resource with RunStrategy other than 'Manual' is denied", func(t *testing.T) {
			// when
			resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"UPDATE", "johnsmith"}, createVMAdmissionRequestJSON("Always", true))))

			// then
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			defer func() {
				require.NoError(t, resp.Body.Close())
			}()
			require.NoError(t, err)
			test.VerifyRequestBlocked(t, body, "this is a Dev Sandbox enforced restriction. Only 'Manual' RunStrategy is permitted", "b6ae2ab4-782b-11ee-b962-0242ac120002")
		})

		t.Run("sandbox user trying to UPDATE a VM resource with RunStrategy 'Manual' is allowed", func(t *testing.T) {
			// when
			resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"UPDATE", "johnsmith"}, createVMAdmissionRequestJSON("Manual", true))))

			// then
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			defer func() {
				require.NoError(t, resp.Body.Close())
			}()
			require.NoError(t, err)
			test.VerifyRequestAllowed(t, body, "b6ae2ab4-782b-11ee-b962-0242ac120002")
		})

		t.Run("sandbox user trying to UPDATE a VM resource without RunStrategy is allowed", func(t *testing.T) { // note: RunStrategy can be removed as it's not needed (it's automatically set upon creation)
			// when
			resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"UPDATE", "johnsmith"}, createVMAdmissionRequestJSON("", true))))

			// then
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			defer func() {
				require.NoError(t, resp.Body.Close())
			}()
			require.NoError(t, err)
			test.VerifyRequestAllowed(t, body, "b6ae2ab4-782b-11ee-b962-0242ac120002")
		})

		t.Run("sandbox user trying to CREATE a VM resource with RunStrategy other than 'Manual' is allowed", func(t *testing.T) {
			// RunStrategy is NOT validated on CREATE (the mutating webhook sets it)
			// when
			resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"CREATE", "johnsmith"}, createVMAdmissionRequestJSON("Always", true))))

			// then
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			defer func() {
				require.NoError(t, resp.Body.Close())
			}()
			require.NoError(t, err)
			test.VerifyRequestAllowed(t, body, "b6ae2ab4-782b-11ee-b962-0242ac120002")
		})
	})

	t.Run("cloudInit validation", func(t *testing.T) {

		t.Run("sandbox user trying to CREATE a VM resource with username in cloudInit is allowed", func(t *testing.T) {
			// when
			resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"CREATE", "johnsmith"}, createVMAdmissionRequestJSON("Manual", true))))

			// then
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			defer func() {
				require.NoError(t, resp.Body.Close())
			}()
			require.NoError(t, err)
			test.VerifyRequestAllowed(t, body, "b6ae2ab4-782b-11ee-b962-0242ac120002")
		})

		t.Run("sandbox user trying to CREATE a VM resource without username in cloudInit is denied", func(t *testing.T) {
			// when
			resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"CREATE", "johnsmith"}, createVMAdmissionRequestJSON("Manual", false))))

			// then
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			defer func() {
				require.NoError(t, resp.Body.Close())
			}()
			require.NoError(t, err)
			test.VerifyRequestBlocked(t, body, "this is a Dev Sandbox enforced restriction. A user must be configured in either the cloudInitNoCloud or cloudInitConfigDrive volume.", "b6ae2ab4-782b-11ee-b962-0242ac120002")
		})

		t.Run("sandbox user trying to UPDATE a VM resource with username in cloudInit is allowed", func(t *testing.T) {
			// cloudInit is NOT validated on UPDATE
			// when
			resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"UPDATE", "johnsmith"}, createVMAdmissionRequestJSON("Manual", true))))

			// then
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			defer func() {
				require.NoError(t, resp.Body.Close())
			}()
			require.NoError(t, err)
			test.VerifyRequestAllowed(t, body, "b6ae2ab4-782b-11ee-b962-0242ac120002")
		})

		t.Run("sandbox user trying to UPDATE a VM resource without username in cloudInit is allowed", func(t *testing.T) {
			// cloudInit is NOT validated on UPDATE
			// when
			resp, err := http.Post(ts.URL, "application/json", bytes.NewBuffer(newCreateVMAdmissionRequest(t, VMAdmReviewTmplParams{"UPDATE", "johnsmith"}, createVMAdmissionRequestJSON("Manual", false))))

			// then
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			defer func() {
				require.NoError(t, resp.Body.Close())
			}()
			require.NoError(t, err)
			test.VerifyRequestAllowed(t, body, "b6ae2ab4-782b-11ee-b962-0242ac120002")
		})
	})
}

func newVMRequestValidator(t *testing.T) *VMRequestValidator {
	s := scheme.Scheme
	err := userv1.Install(s)
	require.NoError(t, err)
	testUser := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "johnsmith",
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(testUser).Build()
	return &VMRequestValidator{
		Client: cl,
	}

}

func newCreateVMAdmissionRequest(t *testing.T, params VMAdmReviewTmplParams, tmplJSON string) []byte {
	tmpl, err := template.New("admission request").Parse(tmplJSON)
	require.NoError(t, err)
	req := &bytes.Buffer{}
	err = tmpl.Execute(req, params)
	require.NoError(t, err)
	return req.Bytes()
}

type VMAdmReviewTmplParams struct {
	ReqType  string
	Username string
}

// createVMAdmissionRequestJSON returns an AdmissionReview JSON template for a VirtualMachine.
// runStrategy is the RunStrategy to set in the VM spec; if empty, the field is omitted.
// cloudInitUsername is the username to include in the cloudInit userData; if empty, the user field is omitted.
func createVMAdmissionRequestJSON(runStrategy string, withCloudInitUsername bool) string {
	runStrategyField := ""
	if runStrategy != "" {
		runStrategyField = fmt.Sprintf(`"runStrategy": "%s",`, runStrategy)
	}
	userField := ""
	if withCloudInitUsername {
		userField = "user: cloud-user\\n"
	}
	return fmt.Sprintf(`{
    "kind": "AdmissionReview",
    "apiVersion": "admission.k8s.io/v1",
    "request": {
        "uid": "b6ae2ab4-782b-11ee-b962-0242ac120002",
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
        "name": "test",
        "namespace": "{{.Username}}-dev",
        "operation": "{{.ReqType}}",
        "userInfo": {
            "username": "{{.Username}}",
            "groups": [
                "system:authenticated"
            ]
        },
        "object": {
            "apiVersion": "kubevirt.io",
            "kind": "VirtualMachine",
            "metadata": {
                "name": "{{.Username}}",
                "namespace": "{{.Username}}-dev"
            },
            "spec": {
                %s
                "template": {
                    "spec": {
                        "volumes": [
                            {
                                "name": "cloudinitdisk",
                                "cloudInitNoCloud": {
                                    "userData": "#cloud-config\n%sssh_authorized_keys: [ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ]\n"
                                }
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
}`, runStrategyField, userField)
}

func TestValidateCloudInitUsername(t *testing.T) {

	t.Run("unstructured.NestedSlice error", func(t *testing.T) {
		// given - volumes is set to a string instead of a slice, causing NestedSlice to return an error
		obj := &unstructured.Unstructured{Object: map[string]interface{}{}}
		require.NoError(t, unstructured.SetNestedField(obj.Object, "not-a-slice", "spec", "template", "spec", "volumes"))

		// when
		err := validateCloudInitUsername(obj)

		// then
		require.EqualError(t, err, "failed to get volumes from VirtualMachine: .spec.template.spec.volumes accessor error: not-a-slice is of the type string, expected []interface{}")
	})
	t.Run("no volumes in spec", func(t *testing.T) {
		// given
		obj := &unstructured.Unstructured{Object: map[string]interface{}{}}

		// when
		err := validateCloudInitUsername(obj)

		// then
		require.EqualError(t, err, "no volumes found in VirtualMachine")
	})

	t.Run("volumes present but no cloudinitdisk volume", func(t *testing.T) {
		// given
		obj := vmWithVolumes(t, map[string]interface{}{
			"name":                  "datadisk",
			"persistentVolumeClaim": map[string]interface{}{"claimName": "my-pvc"},
		})

		// when
		err := validateCloudInitUsername(obj)

		// then
		require.EqualError(t, err, "no username configured in cloudInit volume")
	})

	t.Run("cloudinitdisk with cloudInitNoCloud and username is allowed", func(t *testing.T) {
		// given
		obj := vmWithVolumes(t, cloudInitDiskVolume("cloudInitNoCloud", "#cloud-config\nuser: johnsmith\nssh_authorized_keys: []\n"))

		// when
		err := validateCloudInitUsername(obj)

		// then
		require.NoError(t, err)
	})

	t.Run("cloudinitdisk with cloudInitNoCloud and no username is denied", func(t *testing.T) {
		// given
		obj := vmWithVolumes(t, cloudInitDiskVolume("cloudInitNoCloud", "#cloud-config\nssh_authorized_keys: []\n"))

		// when
		err := validateCloudInitUsername(obj)

		// then
		require.EqualError(t, err, "no username configured in cloudInit volume")
	})

	t.Run("cloudinitdisk with cloudInitConfigDrive and username is allowed", func(t *testing.T) {
		// given
		obj := vmWithVolumes(t, cloudInitDiskVolume("cloudInitConfigDrive", "#cloud-config\nuser: johnsmith\n"))

		// when
		err := validateCloudInitUsername(obj)

		// then
		require.NoError(t, err)
	})

	t.Run("cloudinitdisk with cloudInitConfigDrive and no username is denied", func(t *testing.T) {
		// given
		obj := vmWithVolumes(t, cloudInitDiskVolume("cloudInitConfigDrive", "#cloud-config\nssh_authorized_keys: []\n"))

		// when
		err := validateCloudInitUsername(obj)

		// then
		require.EqualError(t, err, "no username configured in cloudInit volume")
	})

	t.Run("cloudinitdisk with no userData is denied", func(t *testing.T) {
		// given
		obj := vmWithVolumes(t, map[string]interface{}{
			"name":             "cloudinitdisk",
			"cloudInitNoCloud": map[string]interface{}{},
		})

		// when
		err := validateCloudInitUsername(obj)

		// then
		require.EqualError(t, err, "no username configured in cloudInit volume")
	})

	t.Run("cloudinitdisk with empty username is denied", func(t *testing.T) {
		// given
		obj := vmWithVolumes(t, cloudInitDiskVolume("cloudInitNoCloud", "#cloud-config\nuser: \n"))

		// when
		err := validateCloudInitUsername(obj)

		// then
		require.EqualError(t, err, "no username configured in cloudInit volume")
	})

	t.Run("cloudinitdisk with malformed userData YAML is denied", func(t *testing.T) {
		// given - invalid YAML causes yaml.Unmarshal to fail; hasUsername returns false
		obj := vmWithVolumes(t, cloudInitDiskVolume("cloudInitNoCloud", "key: [unclosed bracket\n"))

		// when
		err := validateCloudInitUsername(obj)

		// then
		require.EqualError(t, err, "no username configured in cloudInit volume")
	})

	t.Run("cloudinitdisk with unsupported cloudInit type is denied", func(t *testing.T) {
		// given
		obj := vmWithVolumes(t, cloudInitDiskVolume("cloudInitCustom", "#cloud-config\nuser: johnsmith\n"))

		// when
		err := validateCloudInitUsername(obj)

		// then
		require.EqualError(t, err, "no username configured in cloudInit volume")
	})

	t.Run("cloudinitdisk with username but no cloud-config header is denied", func(t *testing.T) {
		// given - the #cloud-config header is required
		obj := vmWithVolumes(t, cloudInitDiskVolume("cloudInitNoCloud", "user: johnsmith\n"))

		// when
		err := validateCloudInitUsername(obj)

		// then
		require.EqualError(t, err, "no username configured in cloudInit volume")
	})
}

// vmWithVolumes returns an Unstructured VM with the given volume definitions under spec.template.spec.volumes.
func vmWithVolumes(t *testing.T, volumes ...map[string]interface{}) *unstructured.Unstructured {
	vols := make([]interface{}, len(volumes))
	for i, v := range volumes {
		vols[i] = v
	}
	obj := &unstructured.Unstructured{Object: map[string]interface{}{}}
	require.NoError(t, unstructured.SetNestedSlice(obj.Object, vols, "spec", "template", "spec", "volumes"))
	return obj
}

// cloudInitDiskVolume returns a volume map for a cloudinitdisk with the given cloudInit type and userData.
func cloudInitDiskVolume(cloudInitType, userData string) map[string]interface{} {
	return map[string]interface{}{
		"name": "cloudinitdisk",
		cloudInitType: map[string]interface{}{
			"userData": userData,
		},
	}
}
