package mutatingwebhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var testLogger = logf.Log.WithName("test_mutate")

type badReader struct{}

func (b badReader) Read(_ []byte) (n int, err error) {
	return 0, errors.New("bad reader")
}

// Test handleMutate function
func TestHandleMutate(t *testing.T) {

	t.Run("success", func(t *testing.T) {
		// given
		req, err := http.NewRequest("GET", "/mutate-whatever", bytes.NewBuffer(userPodsRawAdmissionReviewJSON))
		if err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()

		// when
		handleMutate(testLogger, rr, req, fakeMutator(t, true))

		// then
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("fail to write response", func(t *testing.T) {
		// given
		req, err := http.NewRequest("GET", "/mutate-whatever", badReader{})
		if err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()

		// when
		handleMutate(testLogger, rr, req, fakeMutator(t, false))

		// then
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, "unable to read the body of the request", rr.Body.String())
	})
}

func admissionReview(t *testing.T, admissionReviewJSONTemplate []byte, options ...admissionReviewOption) admissionv1.AdmissionReview {
	// start with the unstructured AdmReview object from the fixed template
	unstructuredAdmReview := &unstructured.Unstructured{}
	err := unstructuredAdmReview.UnmarshalJSON(admissionReviewJSONTemplate)
	require.NoError(t, err)

	// apply any options to the unstructured AdmReview object
	for _, opt := range options {
		opt(t, unstructuredAdmReview)
	}

	// get the request object from the admReview
	admReviewJSON, err := unstructuredAdmReview.MarshalJSON()
	require.NoError(t, err)

	// deserialize the request
	admReview := admissionv1.AdmissionReview{}
	_, _, err = deserializer.Decode(admReviewJSON, nil, &admReview)
	require.NoError(t, err)

	return admReview
}

func admReviewRequestObject(t *testing.T, admissionReviewJSONTemplate []byte, options ...admissionReviewOption) *unstructured.Unstructured {
	admReview := admissionReview(t, admissionReviewJSONTemplate, options...)
	unstructuredRequestObj := &unstructured.Unstructured{}
	err := unstructuredRequestObj.UnmarshalJSON(admReview.Request.Object.Raw)
	require.NoError(t, err)
	return unstructuredRequestObj
}

// func TestMutate(t *testing.T) {
// 	t.Run("success", func(t *testing.T) {
// 		// given
// 		vmAdmReview := vmAdmissionReview(t)
// 		// expectedResp := vmSuccessResponse(withVolumesPatch(t, cloudInitVolume()))

// 		patchType := admissionv1.PatchTypeJSONPatch
// 		expectedResp := admissionv1.AdmissionResponse{
// 			Allowed: true,
// 			AuditAnnotations: map[string]string{
// 				"virtual_machines_mutating_webhook": "the resource limits and ssh key were set",
// 			},
// 			UID:       "d68b4f8c-c62d-4e83-bd73-de991ab8a56a",
// 			Patch:     []byte{},
// 			PatchType: &patchType,
// 		}
// 		addPatchToResponse(t, &expectedResp, volumesPatch(t, expectedCloudInitVolumeWithSSH()))

// 		// when
// 		response := mutate(podLogger, vmAdmReview, vmMutator)

// 		// then
// 		verifySuccessfulResponse(t, response, expectedResp)
// 	})

// 	t.Run("fails with invalid JSON", func(t *testing.T) {
// 		// given
// 		rawJSON := []byte(`something wrong !`)
// 		var expectedResp = admissionv1.AdmissionResponse{
// 			Result: &metav1.Status{
// 				Message: "couldn't get version/kind; json parse error: json: cannot unmarshal string into Go value of type struct { APIVersion string \"json:\\\"apiVersion,omitempty\\\"\"; Kind string \"json:\\\"kind,omitempty\\\"\" }",
// 			},
// 			UID: "",
// 		}

// 		// when
// 		response := mutate(vmLogger, rawJSON, vmMutator)

// 		// then
// 		verifyFailedResponse(t, response, expectedResp)
// 	})

// 	t.Run("fails with invalid VM", func(t *testing.T) {
// 		// when
// 		rawJSON := []byte(`{
//             "request": {
//                 "object": 111
//             }
//         }`)
// 		var expectedResp = admissionv1.AdmissionResponse{
// 			Result: &metav1.Status{
// 				Message: "unable unmarshal VirtualMachine json object - raw request object: [49 49 49]: json: cannot unmarshal number into Go value of type map[string]interface {}",
// 			},
// 			UID: "",
// 		}

// 		// when
// 		response := mutate(vmLogger, rawJSON, vmMutator)

// 		// then
// 		verifyFailedResponse(t, response, expectedResp)
// 	})
// }

func assertResponseEqual(t *testing.T, mutateHandlerResponse []byte, expectedResp admissionv1.AdmissionResponse) {
	actualReviewResponse := toReviewResponse(t, mutateHandlerResponse)

	t.Log("actualReviewResponse " + string(actualReviewResponse.Patch))
	t.Log("expectedReviewResponse " + string(expectedResp.Patch))
	assert.Equal(t, expectedResp, actualReviewResponse)
}

func verifyFailedResponse(t *testing.T, response []byte, expectedResp admissionv1.AdmissionResponse) {
	actualReviewResponse := toReviewResponse(t, response)
	assert.Equal(t, expectedResp, actualReviewResponse)
}

func toReviewResponse(t *testing.T, admReviewContent []byte) admissionv1.AdmissionResponse {
	r := admissionv1.AdmissionReview{}
	err := json.Unmarshal(admReviewContent, &r)
	require.NoError(t, err)
	return *r.Response
}

// fakeMutator is a mutator that returns a blank AdmissionResponse
func fakeMutator(t *testing.T, success bool) mutateHandler {
	return func(admReview admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
		return &admissionv1.AdmissionResponse{}
	}
}

func assertPatchesEqual(t *testing.T, expected, actual []map[string]interface{}) {
	assert.Equal(t, len(expected), len(actual))
	expectedPatchContent, err := json.Marshal(expected)
	require.NoError(t, err)
	actualPatchContent, err := json.Marshal(actual)
	require.NoError(t, err)
	assert.Equal(t, string(expectedPatchContent), string(actualPatchContent))
}
