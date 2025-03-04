package idler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestPredicate(t *testing.T) {
	// given
	testData := map[string]struct {
		pod            *corev1.Pod
		expectedResult bool
	}{
		"pod with sandbox class": {
			pod: &corev1.Pod{Spec: corev1.PodSpec{
				PriorityClassName: "sandbox-users-pods",
			}},
			expectedResult: true,
		},
		"pod without class": {
			pod:            &corev1.Pod{Spec: corev1.PodSpec{}},
			expectedResult: false,
		},
		"pod with other class": {
			pod: &corev1.Pod{Spec: corev1.PodSpec{
				PriorityClassName: "some-class",
			}},
			expectedResult: false,
		},
	}
	predicate := PodIdlerPredicate{}

	for testName, data := range testData {
		t.Run(testName, func(t *testing.T) {
			// when & then
			assert.Equal(t, data.expectedResult, predicate.Create(event.CreateEvent{Object: data.pod}))
			assert.False(t, predicate.Generic(event.GenericEvent{Object: data.pod}))
			assert.False(t, predicate.Delete(event.DeleteEvent{Object: data.pod}))

			updateTestData := map[string]struct {
				podStatus corev1.PodStatus
				expected  bool
			}{
				"with container above threshold": {
					podStatus: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
						{RestartCount: 51},
						{RestartCount: 49},
					}},
					expected: true,
				},
				"with containers under threshold": {
					podStatus: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
						{RestartCount: 0},
						{RestartCount: 49},
					}},
					expected: false,
				},
				"without container statuses": {
					podStatus: corev1.PodStatus{},
					expected:  false,
				},
			}
			t.Run("for update", func(t *testing.T) {
				for updateTestName, updateData := range updateTestData {
					t.Run(updateTestName, func(t *testing.T) {
						// given
						pod := data.pod.DeepCopy()
						pod.Status = updateData.podStatus
						expectedResult := data.expectedResult && updateData.expected

						// when & then
						assert.Equal(t, expectedResult, predicate.Update(event.UpdateEvent{ObjectNew: pod}))
					})
				}
			})
		})
	}
}
