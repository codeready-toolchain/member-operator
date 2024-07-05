package nstemplateset

import (
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"

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
			nsTmplSet := newNSTmplSet("na", "na", "na")
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

func TestReallySplit(t *testing.T) {
	assert.Empty(t, reallySplit("", ","))
	assert.Equal(t, strings.Split("1,2,3", ","), reallySplit("1,2,3", ","))
	assert.Equal(t, strings.Split("1", ","), reallySplit("1", ","))
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
