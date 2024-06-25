package nstemplateset

import (
	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// shouldCreate checks if the object has any feature toggle annotations. If it does then check if the corresponding
// annotations are present in the NSTemplateSet. Returns true if the annotations match. It means this feature
// should be enabled and the object should be created. It also returns true if the object doesn't have any feature annotation at all
// which means it's a regular object and should be always created.
// Otherwise, returns false.
func shouldCreate(toCreate runtimeclient.Object, nsTmplSet *toolchainv1alpha1.NSTemplateSet) bool {
	var featureToggleObject bool
	for objK, objV := range toCreate.GetAnnotations() {
		if objK == toolchainv1alpha1.FeatureToggleNameAnnotationKey {
			// This is a feature annotation in the object we are considering to create
			// Let's generate an annotation key for the NSTemplateSet for this feature
			expectedFeatureAnnotationKey := FeatureToggleAnnotationKey(objV)
			// Now let's check if there is any annotation with such key in the NSTemplateSet
			for nsK, _ := range nsTmplSet.Annotations {
				if nsK == expectedFeatureAnnotationKey {
					return true // Doesn't matter what value this annotation has. It only matters that the annotation is present.
				}
			}
			featureToggleObject = true // There can be multiple feature annotations in the object. So don't give up and check all annotations.
		}
	}
	// if there was at least one feature annotations in the object and none of them present in the NSTemplateSet
	// then we don't want to create this object. If there is no feature annotation at all then we do want to create the object.
	return !featureToggleObject
}
