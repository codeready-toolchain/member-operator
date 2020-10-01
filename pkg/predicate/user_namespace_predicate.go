package predicate

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// UserNamespace a watcher predicate this only retains Pods whose namespace is not `openshift-*` or `kube-*`
type UserNamespace struct{}

var _ predicate.Predicate = UserNamespace{}

// Create returns true if the Create event should be processed
func (p UserNamespace) Create(e event.CreateEvent) bool {
	return checkNamespaceName(e.Meta)
}

// Delete returns true if the Delete event should be processed
func (p UserNamespace) Delete(e event.DeleteEvent) bool {
	return checkNamespaceName(e.Meta)
}

// Update returns true if the Update event should be processed
func (p UserNamespace) Update(e event.UpdateEvent) bool {
	return checkNamespaceName(e.MetaNew)
}

// Generic returns true if the Generic event should be processed
func (p UserNamespace) Generic(e event.GenericEvent) bool {
	return checkNamespaceName(e.Meta)
}

func checkNamespaceName(m metav1.Object) bool {
	return !strings.HasPrefix(m.GetName(), "openshift") && !strings.HasPrefix(m.GetName(), "kube")
}
