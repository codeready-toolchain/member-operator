package predicate

import (
	"github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"testing"
)

var predicate = OnlyUpdateWhenGenerationNotChanged{}

func TestPredicateUpdateShouldReturnFalseBecauseOfMissingData(t *testing.T) {
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
		ok := predicate.Update(event)

		// then
		assert.False(t, ok)
	}
}

func TestPredicateUpdateShouldReturnFalseAsGenerationChanged(t *testing.T) {
	// given
	updateEvent := event.UpdateEvent{
		MetaNew:   &metav1.ObjectMeta{Generation: int64(123456789)},
		MetaOld:   &metav1.ObjectMeta{Generation: int64(987654321)},
		ObjectNew: &v1alpha1.UserAccount{}, ObjectOld: &v1alpha1.UserAccount{}}

	// when
	ok := predicate.Update(updateEvent)

	// then
	assert.False(t, ok)
}

func TestPredicateUpdateShouldReturnTrueAsGenerationNotChanged(t *testing.T) {
	// given
	updateEvent := event.UpdateEvent{
		MetaNew:   &metav1.ObjectMeta{Generation: int64(123456789)},
		MetaOld:   &metav1.ObjectMeta{Generation: int64(123456789)},
		ObjectNew: &v1alpha1.UserAccount{}, ObjectOld: &v1alpha1.UserAccount{}}

	// when
	ok := predicate.Update(updateEvent)

	// then
	assert.True(t, ok)
}

func TestPredicateCreateShouldReturnFalse(t *testing.T) {
	// given
	createEvent := event.CreateEvent{
		Meta:   &metav1.ObjectMeta{Generation: int64(123456789)},
		Object: &v1alpha1.UserAccount{}}

	// when
	ok := predicate.Create(createEvent)

	// then
	assert.False(t, ok)
}

func TestPredicateDeleteShouldReturnFalse(t *testing.T) {
	// given
	deleteEvent := event.DeleteEvent{
		Meta:   &metav1.ObjectMeta{Generation: int64(123456789)},
		Object: &v1alpha1.UserAccount{}}

	// when
	ok := predicate.Delete(deleteEvent)

	// then
	assert.False(t, ok)
}

func TestPredicateGenericShouldReturnFalse(t *testing.T) {
	// given
	genericEvent := event.GenericEvent{
		Meta:   &metav1.ObjectMeta{Generation: int64(123456789)},
		Object: &v1alpha1.UserAccount{}}

	// when
	ok := predicate.Generic(genericEvent)

	// then
	assert.False(t, ok)
}
