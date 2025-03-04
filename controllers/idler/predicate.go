package idler

import (
	"github.com/codeready-toolchain/member-operator/pkg/webhook/mutatingwebhook"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	runtimeevent "sigs.k8s.io/controller-runtime/pkg/event"
)

type PodIdlerPredicate struct {
}

// Update triggers reconcile if the pod runs in users namespace
// and if the highest restart count is higher than the threshold
func (p PodIdlerPredicate) Update(event runtimeevent.UpdateEvent) bool {
	isUserPod, pod := isUserPod(event.ObjectNew)
	if !isUserPod {
		return false
	}

	return getHighestRestartCount(pod.Status) > RestartThreshold
}

// Create triggers reconcile only if the pod runs in users namespace
func (PodIdlerPredicate) Create(event runtimeevent.CreateEvent) bool {
	isUserPod, _ := isUserPod(event.Object)
	return isUserPod
}

// Delete doesn't trigger reconcile
func (PodIdlerPredicate) Delete(_ runtimeevent.DeleteEvent) bool {
	return false
}

// Generic doesn't trigger reconcile
func (p PodIdlerPredicate) Generic(_ runtimeevent.GenericEvent) bool {
	return false
}

func isUserPod(object client.Object) (bool, *corev1.Pod) {
	if pod, ok := object.(*corev1.Pod); ok { // this can be replaced by the typed-predicate in the next version of k8s
		// all pods running in users' namespaces have the priorityClassName set, so trigger reconcile only
		// if the pod contains the same class name to ensure that the pod runs in a user's namespace
		// (we don't care about other pods)
		return pod.Spec.PriorityClassName == mutatingwebhook.PriorityClassName, pod
	}
	return false, nil
}
