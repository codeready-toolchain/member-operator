package nstemplateset

import (
	"testing"

	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func TestShouldCreate(t *testing.T) {
	// given
	tests := []struct {
		name                  string
		objFeature            *string
		nsTemplateSetFeatures *string
		expectedToBeCreated   bool
	}{
		{
			name:                  "object with an empty feature annotation should not be created when no features are enabled in NSTemplateSet",
			objFeature:            p(""),
			nsTemplateSetFeatures: nil,
			expectedToBeCreated:   false,
		},
		{
			name:                  "object with an empty feature annotation should not be created when NSTemplateSet has an empty feature annotation",
			objFeature:            p(""),
			nsTemplateSetFeatures: p(""),
			expectedToBeCreated:   false,
		},
		{
			name:                  "object with no feature annotation should be created when NSTemplateSet has an empty feature annotation",
			objFeature:            nil,
			nsTemplateSetFeatures: p(""),
			expectedToBeCreated:   true,
		},
		{
			name:                  "object with an empty feature annotation should not be created when NSTemplateSet has some features enabled",
			objFeature:            p(""),
			nsTemplateSetFeatures: p("feature-1"),
			expectedToBeCreated:   false,
		},
		{
			name:                  "object with a feature should not be created when NSTemplateSet has an empty feature annotation",
			objFeature:            p("feature-1"),
			nsTemplateSetFeatures: p(""),
			expectedToBeCreated:   false,
		},
		{
			name:                  "object with no feature annotation should be created when no features are enabled in NSTemplateSet",
			objFeature:            nil,
			nsTemplateSetFeatures: nil,
			expectedToBeCreated:   true,
		},
		{
			name:                  "object with no feature annotation should be created when some features are enabled in NSTemplateSet",
			objFeature:            nil,
			nsTemplateSetFeatures: p("feature-1,feature-2"),
			expectedToBeCreated:   true,
		},
		{
			name:                  "object with a feature annotation should not be created when no features are enabled in NSTemplateSet",
			objFeature:            p("feature-1"),
			nsTemplateSetFeatures: nil,
			expectedToBeCreated:   false,
		},
		{
			name:                  "object with a feature annotation should not be created when different features are enabled in NSTemplateSet",
			objFeature:            p("feature-1"),
			nsTemplateSetFeatures: p("feature-2"),
			expectedToBeCreated:   false,
		},
		{
			name:                  "object with a feature which is among enabled features should be created",
			objFeature:            p("feature-2"),
			nsTemplateSetFeatures: p("feature-1,feature-2,feature-3"),
			expectedToBeCreated:   true,
		},
	}
	for _, testRun := range tests {
		t.Run(testRun.name, func(t *testing.T) {
			// given
			obj := objectWithFeature(testRun.objFeature)
			nsTmplSet := newNSTmplSet("dummy-namespace", "dummy-name", "dummy-tier")
			if testRun.nsTemplateSetFeatures != nil {
				nsTmplSet.Annotations = map[string]string{
					toolchainv1alpha1.FeatureToggleNameAnnotationKey: *testRun.nsTemplateSetFeatures,
				}
			}

			// when
			should := shouldCreate(obj, nsTmplSet)

			// then
			assert.Equal(t, testRun.expectedToBeCreated, should)
		})
	}
}

func TestFeaturesChanged(t *testing.T) {
	for _, test := range []struct {
		name           string
		annoFeatures   string
		statusFeatures []string
		changed        bool
	}{
		{
			name:    "should report no change when no features in either annos or status",
			changed: false,
		},
		{
			name:         "should report change when no features in status",
			annoFeatures: "feature",
			changed:      true,
		},
		{
			name:           "should report change when no features in anno",
			statusFeatures: []string{"feature"},
			changed:        true,
		},
		{
			name:           "should report no change when features equal",
			statusFeatures: []string{"feature1", "feature2"},
			annoFeatures:   "feature1,feature2",
			changed:        false,
		},
		{
			name:           "should report change when features differ in number",
			statusFeatures: []string{"feature1", "feature2"},
			annoFeatures:   "feature1",
			changed:        true,
		},
		{
			name:           "should report change when features differ",
			statusFeatures: []string{"feature1", "feature2"},
			annoFeatures:   "feature1,feature3",
			changed:        true,
		},
		{
			name:           "should detect duplicates",
			statusFeatures: []string{"feature1", "feature2", "feature1"},
			annoFeatures:   "feature2,feature1",
			changed:        false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			nsts := newNSTmplSet("default", "ns", "base", withNSTemplateSetFeatureAnnotation(test.annoFeatures), withStatusFeatureToggles(test.statusFeatures))

			// when
			changed := featuresChanged(nsts)

			// then
			assert.Equal(t, test.changed, changed)
		})
	}
}

func p(s string) *string {
	return &s
}

func objectWithFeature(feature *string) runtimeclient.Object {
	obj := newRoleBinding("my-namespace", "rb", "my-space")
	if feature != nil {
		obj.Annotations = map[string]string{
			toolchainv1alpha1.FeatureToggleNameAnnotationKey: *feature,
		}
	}
	return obj
}
