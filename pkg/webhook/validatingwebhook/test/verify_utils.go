package test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
)

func VerifyRequestBlocked(t *testing.T, response []byte, msg string, UID string) {
	reviewResponse := toReviewResponse(t, response)
	assert.False(t, reviewResponse.Allowed)
	assert.NotEmpty(t, reviewResponse.Result)
	assert.Contains(t, reviewResponse.Result.Message, msg)
	assert.Equal(t, UID, string(reviewResponse.UID))
}

func VerifyRequestAllowed(t *testing.T, response []byte, UID string) {
	reviewResponse := toReviewResponse(t, response)
	assert.True(t, reviewResponse.Allowed)
	assert.Empty(t, reviewResponse.Result)
	assert.Equal(t, UID, string(reviewResponse.UID))
}

func toReviewResponse(t *testing.T, content []byte) *admissionv1.AdmissionResponse {
	r := admissionv1.AdmissionReview{}
	err := json.Unmarshal(content, &r)
	require.NoError(t, err)
	return r.Response
}
