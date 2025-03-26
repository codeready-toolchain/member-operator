package idler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestPredicate(t *testing.T) {
	// given
	predicate := PodIdlerPredicate{}
	testPriorityClassData := map[string]struct {
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

	testRestartData := map[string]struct {
		newPodStatus corev1.PodStatus
		expected     bool
	}{
		"with container above threshold": {
			newPodStatus: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 50},
				{RestartCount: 48},
			}},
			expected: true,
		},
		"with containers under threshold": {
			newPodStatus: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 0},
				{RestartCount: 49},
			}},
			expected: false,
		},
		"without container statuses": {
			newPodStatus: corev1.PodStatus{},
			expected:     false,
		},
	}

	startTime := metav1.Now()
	testStartTimeData := map[string]struct {
		oldPod    *corev1.Pod
		startTime *metav1.Time
		expected  bool
	}{
		"without startTime": {
			oldPod:    &corev1.Pod{},
			startTime: nil,
			expected:  false,
		},
		"with added startTime": {
			oldPod:    &corev1.Pod{},
			startTime: &startTime,
			expected:  true,
		},
		"with startTime already present": {
			oldPod: &corev1.Pod{Status: corev1.PodStatus{
				StartTime: &startTime,
			}},
			startTime: &startTime,
			expected:  false,
		},
	}

	for classTestName, classTestData := range testPriorityClassData {
		t.Run(classTestName, func(t *testing.T) {
			for subTestName, restartData := range testRestartData {
				t.Run(subTestName, func(t *testing.T) {
					for subSubTestName, startTimeData := range testStartTimeData {
						t.Run(subSubTestName, func(t *testing.T) {
							// given
							pod := classTestData.pod.DeepCopy()
							pod.Status = restartData.newPodStatus
							pod.Status.StartTime = startTimeData.startTime
							expectedResult := classTestData.expectedResult && (restartData.expected || startTimeData.expected)

							// when & then
							assert.Equal(t, expectedResult, predicate.Update(event.TypedUpdateEvent[*corev1.Pod]{
								ObjectOld: startTimeData.oldPod,
								ObjectNew: pod,
							}))

							assert.False(t, predicate.Create(event.TypedCreateEvent[*corev1.Pod]{Object: classTestData.pod}))
							assert.False(t, predicate.Generic(event.TypedGenericEvent[*corev1.Pod]{Object: classTestData.pod}))
							assert.Equal(t, classTestData.expectedResult, predicate.Delete(
								event.TypedDeleteEvent[*corev1.Pod]{Object: classTestData.pod}))
						})
					}
				})
			}
		})
	}
}
