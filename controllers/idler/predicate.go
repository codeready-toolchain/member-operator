package idler

import (
	"github.com/codeready-toolchain/member-operator/pkg/webhook/mutatingwebhook"
	corev1 "k8s.io/api/core/v1"
	runtimeevent "sigs.k8s.io/controller-runtime/pkg/event"
)

type PodIdlerPredicate struct {
}

// Update triggers reconcile if the pod runs in users namespace
// and if either the highest restart count is higher than the threshold
// or the startTime was newly set in the new version of the pod
func (p PodIdlerPredicate) Update(event runtimeevent.TypedUpdateEvent[*corev1.Pod]) bool {
	// all pods running in users' namespaces have the priorityClassName set, so trigger reconcile only
	// if the pod contains the same class name to ensure that the pod runs in a user's namespace
	// (we don't care about other pods)
	if event.ObjectNew.Spec.PriorityClassName != mutatingwebhook.PriorityClassName {
		return false
	}
	startTimeNewlySet := event.ObjectOld.Status.StartTime == nil && event.ObjectNew.Status.StartTime != nil
	return startTimeNewlySet || getHighestRestartCount(event.ObjectNew.Status) > aapRestartThreshold
}

// Create doesn't trigger reconcile
func (PodIdlerPredicate) Create(_ runtimeevent.TypedCreateEvent[*corev1.Pod]) bool {
	return false
}

// Delete triggers reconcile for users pods to make sure that the deleted pod is not tracked in the status anymore
func (PodIdlerPredicate) Delete(event runtimeevent.TypedDeleteEvent[*corev1.Pod]) bool {
	// all pods running in users' namespaces have the priorityClassName set, so trigger reconcile only
	// if the pod contains the same class name to ensure that the pod runs in a user's namespace
	// (we don't care about other pods)
	return event.Object.Spec.PriorityClassName == mutatingwebhook.PriorityClassName
}

// Generic doesn't trigger reconcile
func (p PodIdlerPredicate) Generic(_ runtimeevent.TypedGenericEvent[*corev1.Pod]) bool {
	return false
}
