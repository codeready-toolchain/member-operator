package predicate

import (
	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// UserNamespace a watcher predicate this only retains Pods whose namespace is not `openshift-*` or `kube-*`
type UserNamespace struct{}

var _ predicate.Predicate = UserNamespace{}

// Create returns true if the Create event should be processed
func (p UserNamespace) Create(e event.CreateEvent) bool {
	return checkNamespace(e.Meta)
}

// Delete returns true if the Delete event should be processed
func (p UserNamespace) Delete(e event.DeleteEvent) bool {
	return checkNamespace(e.Meta)
}

// Update returns true if the Update event should be processed
func (p UserNamespace) Update(e event.UpdateEvent) bool {
	return checkNamespace(e.MetaNew)
}

// Generic returns true if the Generic event should be processed
func (p UserNamespace) Generic(e event.GenericEvent) bool {
	return checkNamespace(e.Meta)
}

func checkNamespace(m metav1.Object) bool {
	_, found := m.GetLabels()[toolchainv1alpha1.ProviderLabelKey]
	return found
}
