package idler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMapper(t *testing.T) {
	// given
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "jane-dev", Name: "my-pod"}}

	// when
	requests := MapPodToIdler(context.TODO(), pod)

	// then
	require.Len(t, requests, 1)
	assert.Equal(t, "jane-dev", requests[0].Name)
	assert.Empty(t, requests[0].Namespace)
}
