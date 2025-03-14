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
func (p PodIdlerPredicate) Update(event runtimeevent.UpdateEvent) bool {
	newPod, ok := event.ObjectNew.(*corev1.Pod) // this can be replaced by the typed-predicate in the next version of k8s
	if !ok {
		return false
	}
	// all pods running in users' namespaces have the priorityClassName set, so trigger reconcile only
	// if the pod contains the same class name to ensure that the pod runs in a user's namespace
	// (we don't care about other pods)
	if newPod.Spec.PriorityClassName != mutatingwebhook.PriorityClassName {
		return false
	}
	if oldPod, ok := event.ObjectOld.(*corev1.Pod); ok {
		startTimeNewlySet := oldPod.Status.StartTime == nil && newPod.Status.StartTime != nil
		return startTimeNewlySet || getHighestRestartCount(newPod.Status) > aaPRestartThreshold
	}
	return false
}

// Create doesn't trigger reconcile
func (PodIdlerPredicate) Create(_ runtimeevent.CreateEvent) bool {
	return false
}

// Delete triggers reconcile to make sure that the pod is not tracked in the status
func (PodIdlerPredicate) Delete(_ runtimeevent.DeleteEvent) bool {
	return true
}

// Generic doesn't trigger reconcile
func (p PodIdlerPredicate) Generic(_ runtimeevent.GenericEvent) bool {
	return false
}
