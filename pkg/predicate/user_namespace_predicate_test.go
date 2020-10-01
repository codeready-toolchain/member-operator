package predicate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestUserNamespacePredicate(t *testing.T) {

	// given
	p := UserNamespace{}

	t.Run("when creating", func(t *testing.T) {

		t.Run("kube namespace", func(t *testing.T) {
			// given
			e := event.CreateEvent{
				Meta:   &metav1.ObjectMeta{Name: "kube-public"},
				Object: &corev1.Namespace{},
			}
			// then
			result := p.Create(e)
			// then
			assert.False(t, result)
		})

		t.Run("openshift namespace", func(t *testing.T) {
			// given
			e := event.CreateEvent{
				Meta:   &metav1.ObjectMeta{Name: "openshift"},
				Object: &corev1.Namespace{},
			}
			// then
			result := p.Create(e)
			// then
			assert.False(t, result)
		})

		t.Run("user namespace", func(t *testing.T) {
			// given
			e := event.CreateEvent{
				Meta:   &metav1.ObjectMeta{Name: "user-dev"},
				Object: &corev1.Namespace{},
			}
			// then
			result := p.Create(e)
			// then
			assert.True(t, result)
		})
	})

	t.Run("when updating", func(t *testing.T) {

		t.Run("kube namespace", func(t *testing.T) {
			// given
			e := event.UpdateEvent{
				MetaNew:   &metav1.ObjectMeta{Name: "kube-public"},
				ObjectNew: &corev1.Namespace{},
			}
			// then
			result := p.Update(e)
			// then
			assert.False(t, result)
		})

		t.Run("openshift namespace", func(t *testing.T) {
			// given
			e := event.UpdateEvent{
				MetaNew:   &metav1.ObjectMeta{Name: "openshift"},
				ObjectNew: &corev1.Namespace{},
			}
			// then
			result := p.Update(e)
			// then
			assert.False(t, result)
		})

		t.Run("user namespace", func(t *testing.T) {
			// given
			e := event.UpdateEvent{
				MetaNew:   &metav1.ObjectMeta{Name: "user-dev"},
				ObjectNew: &corev1.Namespace{},
			}
			// then
			result := p.Update(e)
			// then
			assert.True(t, result)
		})
	})

	t.Run("when deleting", func(t *testing.T) {

		t.Run("kube namespace", func(t *testing.T) {
			// given
			e := event.DeleteEvent{
				Meta:   &metav1.ObjectMeta{Name: "kube-public"},
				Object: &corev1.Namespace{},
			}
			// then
			result := p.Delete(e)
			// then
			assert.False(t, result)
		})

		t.Run("openshift namespace", func(t *testing.T) {
			// given
			e := event.DeleteEvent{
				Meta:   &metav1.ObjectMeta{Name: "openshift"},
				Object: &corev1.Namespace{},
			}
			// then
			result := p.Delete(e)
			// then
			assert.False(t, result)
		})

		t.Run("user namespace", func(t *testing.T) {
			// given
			e := event.DeleteEvent{
				Meta:   &metav1.ObjectMeta{Name: "user-dev"},
				Object: &corev1.Namespace{},
			}
			// then
			result := p.Delete(e)
			// then
			assert.True(t, result)
		})
	})

	t.Run("when generic event occurs", func(t *testing.T) {

		t.Run("kube namespace", func(t *testing.T) {
			// given
			e := event.GenericEvent{
				Meta:   &metav1.ObjectMeta{Name: "kube-public"},
				Object: &corev1.Namespace{},
			}
			// then
			result := p.Generic(e)
			// then
			assert.False(t, result)
		})

		t.Run("openshift namespace", func(t *testing.T) {
			// given
			e := event.GenericEvent{
				Meta:   &metav1.ObjectMeta{Name: "openshift"},
				Object: &corev1.Namespace{},
			}
			// then
			result := p.Generic(e)
			// then
			assert.False(t, result)
		})

		t.Run("user namespace", func(t *testing.T) {
			// given
			e := event.GenericEvent{
				Meta:   &metav1.ObjectMeta{Name: "user-dev"},
				Object: &corev1.Namespace{},
			}
			// then
			result := p.Generic(e)
			// then
			assert.True(t, result)
		})
	})
}
