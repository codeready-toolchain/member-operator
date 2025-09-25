package nstemplateset

import (
	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/utils"
	"k8s.io/utils/strings/slices"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// shouldCreate checks if the object has a feature toggle annotation. If it does then check if the corresponding
// feature is referenced in the NSTemplateSet feature annotation. Returns true if yes. It means this feature
// should be enabled and the object should be created. It also returns true if the object doesn't have a feature annotation at all
// which means it's a regular object, and it's not managed by any feature toggle and should be always created.
// Otherwise, returns false.
func shouldCreate(toCreate runtimeclient.Object, nsTmplSet *toolchainv1alpha1.NSTemplateSet) bool {
	feature, found := toCreate.GetAnnotations()[toolchainv1alpha1.FeatureToggleNameAnnotationKey]
	if !found {
		return true // This object is a regular object and not managed by a feature toggle. Always create it.
	}
	// This object represents a feature. Let's check if this feature is among winners in the NSTemplateSet.
	winners, found := nsTmplSet.GetAnnotations()[toolchainv1alpha1.FeatureToggleNameAnnotationKey]
	if !found {
		return false // No feature winners in the NSTemplateSet at all. Skip this object.
	}
	return slices.Contains(utils.SplitCommaSeparatedList(winners), feature)
}

// featuresChanged returns true if the features on the NSTemplateSet changed since the last time it was applied.
func featuresChanged(nsTmplSet *toolchainv1alpha1.NSTemplateSet) bool {
	changed, _ := featureAnnotationNeedsUpdate(nsTmplSet)
	return changed
}
