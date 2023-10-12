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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var testLogger = logf.Log.WithName("test_mutate")

type expectedSuccessResponse struct {
	patch              string
	auditAnnotationKey string
	auditAnnotationVal string
	uid                string
}

type expectedFailedResponse struct {
	auditAnnotationKey string
	errMsg             string
}

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
		expectedRespSuccess := expectedSuccessResponse{
			auditAnnotationKey: "users_pods_mutating_webhook",
			auditAnnotationVal: "the sandbox-users-pods PriorityClass was set",
			uid:                "a68769e5-d817-4617-bec5-90efa2bad6f6",
		}

		// when
		handleMutate(testLogger, rr, req, fakeMutator())

		// then
		assert.Equal(t, http.StatusOK, rr.Code)
		verifySuccessfulResponse(t, rr.Body.Bytes(), expectedRespSuccess)
	})

	t.Run("fail to write response", func(t *testing.T) {
		// given
		req, err := http.NewRequest("GET", "/mutate-whatever", badReader{})
		if err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()

		// when
		handleMutate(testLogger, rr, req, fakeMutator())

		// then
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, "unable to read the body of the request", rr.Body.String())
	})
}

func verifySuccessfulResponse(t *testing.T, response []byte, expectedResp expectedSuccessResponse) {
	reviewResponse := toReviewResponse(t, response)
	assert.Equal(t, expectedResp.patch, string(reviewResponse.Patch))
	assert.Contains(t, expectedResp.auditAnnotationVal, reviewResponse.AuditAnnotations[expectedResp.auditAnnotationKey])
	assert.True(t, reviewResponse.Allowed)
	assert.Equal(t, admissionv1.PatchTypeJSONPatch, *reviewResponse.PatchType)
	assert.Empty(t, reviewResponse.Result)
	assert.Equal(t, expectedResp.uid, string(reviewResponse.UID))
}

func verifyFailedResponse(t *testing.T, response []byte, expectedResp expectedFailedResponse) {
	reviewResponse := toReviewResponse(t, response)
	assert.Empty(t, string(reviewResponse.Patch))
	assert.Empty(t, reviewResponse.AuditAnnotations[expectedResp.auditAnnotationKey])
	assert.False(t, reviewResponse.Allowed)
	assert.Nil(t, reviewResponse.PatchType)
	assert.Empty(t, string(reviewResponse.UID))

	require.NotEmpty(t, reviewResponse.Result)
	assert.Contains(t, reviewResponse.Result.Message, expectedResp.errMsg)
}

func toReviewResponse(t *testing.T, content []byte) *admissionv1.AdmissionResponse {
	r := admissionv1.AdmissionReview{}
	err := json.Unmarshal(content, &r)
	require.NoError(t, err)
	return r.Response
}

func fakeMutator() mutateHandler {
	return func(admReview admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
		patchType := admissionv1.PatchTypeJSONPatch
		return &admissionv1.AdmissionResponse{
			Allowed:   true,
			UID:       admReview.Request.UID,
			PatchType: &patchType,
		}
	}
}
