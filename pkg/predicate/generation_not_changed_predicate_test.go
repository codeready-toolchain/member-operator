package predicate

import (
	"testing"

	"github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestGenerationNotChangedPredicate(t *testing.T) {

	// given
	p := OnlyUpdateWhenGenerationNotChanged{}

	t.Run("should return false because of missing data", func(t *testing.T) {
		// given
		updateEvents := []event.UpdateEvent{
			{},
			{MetaNew: &metav1.ObjectMeta{}, MetaOld: &metav1.ObjectMeta{},
				ObjectNew: &v1alpha1.UserAccount{}},
			{MetaNew: &metav1.ObjectMeta{}, MetaOld: &metav1.ObjectMeta{},
				ObjectOld: &v1alpha1.UserAccount{}},
			{MetaNew: &metav1.ObjectMeta{}, ObjectOld: &v1alpha1.UserAccount{},
				ObjectNew: &v1alpha1.UserAccount{}},
			{ObjectNew: &v1alpha1.UserAccount{}, MetaOld: &metav1.ObjectMeta{},
				ObjectOld: &v1alpha1.UserAccount{}}}

		for _, event := range updateEvents {
			// when
			ok := p.Update(event)

			// then
			assert.False(t, ok)
		}
	})

	t.Run("when creating", func(t *testing.T) {
		// given
		createEvent := event.CreateEvent{
			Meta:   &metav1.ObjectMeta{Generation: int64(123456789)},
			Object: &v1alpha1.UserAccount{}}

		// when
		ok := p.Create(createEvent)

		// then
		assert.False(t, ok)
	})

	t.Run("when updating", func(t *testing.T) {

		t.Run("generation changed", func(t *testing.T) {
			// given
			updateEvent := event.UpdateEvent{
				MetaNew:   &metav1.ObjectMeta{Generation: int64(123456789)},
				MetaOld:   &metav1.ObjectMeta{Generation: int64(987654321)},
				ObjectNew: &v1alpha1.UserAccount{}, ObjectOld: &v1alpha1.UserAccount{}}

			// when
			ok := p.Update(updateEvent)

			// then
			assert.False(t, ok)
		})

		t.Run("generation did not change", func(t *testing.T) {
			// given
			p := OnlyUpdateWhenGenerationNotChanged{}
			updateEvent := event.UpdateEvent{
				MetaNew:   &metav1.ObjectMeta{Generation: int64(123456789)},
				MetaOld:   &metav1.ObjectMeta{Generation: int64(123456789)},
				ObjectNew: &v1alpha1.UserAccount{}, ObjectOld: &v1alpha1.UserAccount{}}

			// when
			ok := p.Update(updateEvent)

			// then
			assert.True(t, ok)
		})
	})

	t.Run("when deleting", func(t *testing.T) {
		// given
		p := OnlyUpdateWhenGenerationNotChanged{}
		deleteEvent := event.DeleteEvent{
			Meta:   &metav1.ObjectMeta{Generation: int64(123456789)},
			Object: &v1alpha1.UserAccount{}}

		// when
		ok := p.Delete(deleteEvent)

		// then
		assert.False(t, ok)
	})

	t.Run("when generic event occurs", func(t *testing.T) {
		// given
		p := OnlyUpdateWhenGenerationNotChanged{}
		genericEvent := event.GenericEvent{
			Meta:   &metav1.ObjectMeta{Generation: int64(123456789)},
			Object: &v1alpha1.UserAccount{}}

		// when
		ok := p.Generic(genericEvent)

		// then
		assert.False(t, ok)
	})
}
