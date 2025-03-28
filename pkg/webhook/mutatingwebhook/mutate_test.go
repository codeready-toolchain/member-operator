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
		handleMutate(testLogger, rr, req, fakeMutator)

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
		handleMutate(testLogger, rr, req, fakeMutator)

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

func assertResponseEqual(t *testing.T, mutateHandlerResponse []byte, expectedResp admissionv1.AdmissionResponse) {
	actualReviewResponse := toReviewResponse(t, mutateHandlerResponse)

	t.Log("actualReviewResponse " + string(actualReviewResponse.Patch))
	t.Log("expectedReviewResponse " + string(expectedResp.Patch))
	assert.Equal(t, expectedResp, actualReviewResponse)
}

func toReviewResponse(t *testing.T, admReviewContent []byte) admissionv1.AdmissionResponse {
	r := admissionv1.AdmissionReview{}
	err := json.Unmarshal(admReviewContent, &r)
	require.NoError(t, err)
	return *r.Response
}

// fakeMutator is a mutator that returns a blank AdmissionResponse
func fakeMutator(_ admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{}
}

func assertPatchesEqual(t *testing.T, expected, actual []map[string]interface{}) {
	assert.Len(t, actual, len(expected))
	expectedPatchContent, err := json.Marshal(expected)
	require.NoError(t, err)
	actualPatchContent, err := json.Marshal(actual)
	require.NoError(t, err)
	assert.Equal(t, string(expectedPatchContent), string(actualPatchContent))
}
