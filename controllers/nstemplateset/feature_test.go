package nstemplateset

import (
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func TestShouldCreate(t *testing.T) {
	t.Run("return namespace whose revision is not set", func(t *testing.T) {
		// given
		tests := []struct {
			name                  string
			objFeature            string
			nsTemplateSetFeatures string
			expectedToBeCreated   bool
		}{
			{
				name:                  "object with no feature annotation should be created when no features are enabled in NSTemplateSet",
				objFeature:            "",
				nsTemplateSetFeatures: "",
				expectedToBeCreated:   true,
			},
			{
				name:                  "object with no feature annotation should be created when some features are enabled in NSTemplateSet",
				objFeature:            "",
				nsTemplateSetFeatures: "feature-1,feature-2",
				expectedToBeCreated:   true,
			},
			{
				name:                  "object with a feature annotation should not be created when no features are enabled in NSTemplateSet",
				objFeature:            "feature-1",
				nsTemplateSetFeatures: "",
				expectedToBeCreated:   false,
			},
			{
				name:                  "object with a feature annotation should not be created when different features are enabled in NSTemplateSet",
				objFeature:            "feature-1",
				nsTemplateSetFeatures: "feature-2",
				expectedToBeCreated:   false,
			},
			{
				name:                  "object with a feature which is among enabled features should be created",
				objFeature:            "feature-2",
				nsTemplateSetFeatures: "feature-1,feature-2,feature-3",
				expectedToBeCreated:   true,
			},
		}
		for _, testRun := range tests {
			t.Run(testRun.name, func(t *testing.T) {
				// given
				obj := objectWithFeature(testRun.objFeature)
				nsTmplSet := newNSTmplSet("na", "na", "na")
				if testRun.nsTemplateSetFeatures != "" {
					nsTmplSet.Annotations = map[string]string{
						toolchainv1alpha1.FeatureToggleNameAnnotationKey: testRun.nsTemplateSetFeatures,
					}
				}

				// when
				should := shouldCreate(obj, nsTmplSet)

				// then
				assert.Equal(t, testRun.expectedToBeCreated, should)
			})
		}
	})
}

func objectWithFeature(feature string) runtimeclient.Object {
	obj := newRoleBinding("my-namespace", "rb-"+feature, "my-space")
	if feature != "" {
		obj.Annotations = map[string]string{
			toolchainv1alpha1.FeatureToggleNameAnnotationKey: feature,
		}
	}
	return obj
}
