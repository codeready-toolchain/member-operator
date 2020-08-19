package nstemplateset

import (
	"sync"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/test"
	testcommon "github.com/codeready-toolchain/toolchain-common/pkg/test"

	templatev1 "github.com/openshift/api/template/v1"
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

func newTierTemplate(tier, typeName, revision string) *toolchainv1alpha1.TierTemplate {
	return &toolchainv1alpha1.TierTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      test.NewTierTemplateName(tier, typeName, revision),
			Namespace: "toolchain-host-operator",
		},
		Spec: toolchainv1alpha1.TierTemplateSpec{
			TierName: tier,
			Type:     typeName,
			Revision: revision,
			Template: templatev1.Template{
				ObjectMeta: metav1.ObjectMeta{
					Name: typeName,
				},
			},
		},
	}
}

func TestGetTierTemplate(t *testing.T) {

	// given
	// Setup Scheme for all resources (required before adding objects in the fake client)
	err := apis.AddToScheme(scheme.Scheme)
	require.NoError(t, err)
	logf.SetLogger(zap.Logger())

	basicTierCode := newTierTemplate("basic", "code", "abcdef")
	basicTierDev := newTierTemplate("basic", "dev", "123456")
	basicTierStage := newTierTemplate("basic", "stage", "1a2b3c")
	basicTierCluster := newTierTemplate("basic", "clusterresources", "aa11bb22")

	advancedTierCode := newTierTemplate("advanced", "code", "ghijkl")
	advancedTierDev := newTierTemplate("advanced", "dev", "789012")
	advancedTierStage := newTierTemplate("advanced", "stage", "4d5e6f")

	other := newTierTemplate("other", "other", "other")
	other.Namespace = "other"

	cl := testcommon.NewFakeClient(t, basicTierCode, basicTierDev, basicTierStage, basicTierCluster, advancedTierCode, advancedTierDev, advancedTierStage, other)

	t.Run("return code for basic tier", func(t *testing.T) {
		// given
		hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
		defer resetCache()
		// when
		tierTmpl, err := getTierTemplate(hostCluster, "basic-code-abcdef")

		// then
		require.NoError(t, err)
		assertThatTierTemplateIsSameAs(t, basicTierCode, tierTmpl)
	})

	t.Run("return dev for advanced tier", func(t *testing.T) {
		// given
		hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
		defer resetCache()
		// when
		tierTmpl, err := getTierTemplate(hostCluster, "advanced-dev-789012")

		// then
		require.NoError(t, err)
		assertThatTierTemplateIsSameAs(t, advancedTierDev, tierTmpl)
	})

	t.Run("return cluster type for basic tier", func(t *testing.T) {
		// given
		hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
		defer resetCache()
		// when
		tierTmpl, err := getTierTemplate(hostCluster, "basic-clusterresources-aa11bb22")

		// then
		require.NoError(t, err)
		assertThatTierTemplateIsSameAs(t, basicTierCluster, tierTmpl)
	})

	t.Run("test cache", func(t *testing.T) {
		// given
		hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
		defer resetCache()
		_, err := getTierTemplate(hostCluster, "basic-code-abcdef")
		require.NoError(t, err)

		t.Run("return cached TierTemplate even when for the second call doesn't exist", func(t *testing.T) {

			emptyHost := test.NewGetHostCluster(testcommon.NewFakeClient(t), true, apiv1.ConditionTrue)

			// when
			tierTmpl, err := getTierTemplate(emptyHost, "basic-code-abcdef")

			// then
			require.NoError(t, err)
			assertThatTierTemplateIsSameAs(t, basicTierCode, tierTmpl)
		})

		t.Run("return cached TierTemplate even when the host cluster was removed", func(t *testing.T) {
			// given
			noCluster := test.NewGetHostCluster(cl, false, apiv1.ConditionFalse)

			// when
			tierTmpl, err := getTierTemplate(noCluster, "basic-code-abcdef")

			// then
			require.NoError(t, err)
			assertThatTierTemplateIsSameAs(t, basicTierCode, tierTmpl)
		})

		t.Run("return cached TierTemplate even when the host cluster is not ready", func(t *testing.T) {
			// given
			noCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionFalse)

			// when
			tierTmpl, err := getTierTemplate(noCluster, "basic-code-abcdef")

			// then
			require.NoError(t, err)
			assertThatTierTemplateIsSameAs(t, basicTierCode, tierTmpl)
		})

		t.Run("return cached TierTemplate even when the host cluster is not ready", func(t *testing.T) {
			// given
			noCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionFalse)

			// when
			tierTmpl, err := getTierTemplate(noCluster, "basic-code-abcdef")

			// then
			require.NoError(t, err)
			assertThatTierTemplateIsSameAs(t, basicTierCode, tierTmpl)
		})
	})

	t.Run("failures", func(t *testing.T) {
		// given - no matter if one TierTemplate is cached
		hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
		defer resetCache()
		_, err := getTierTemplate(hostCluster, "basic-code-abcdef")
		require.NoError(t, err)

		t.Run("host cluster not available", func(t *testing.T) {
			// given
			hostCluster := test.NewGetHostCluster(cl, false, apiv1.ConditionFalse)
			defer resetCache()
			// when
			_, err := getTierTemplate(hostCluster, "advanced-dev-789012")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to connect to the host cluster: unknown cluster")
		})

		t.Run("host cluster not ready", func(t *testing.T) {
			// given
			hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionFalse)
			defer resetCache()
			// when
			_, err := getTierTemplate(hostCluster, "advanced-dev-789012")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "the host cluster is not ready")
		})

		t.Run("unknown templateRef", func(t *testing.T) {
			// given
			hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
			defer resetCache()
			// when
			_, err := getTierTemplate(hostCluster, "unknown")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to retrieve the TierTemplate 'unknown' from 'Host' cluster")
		})

		t.Run("tier in another namespace", func(t *testing.T) {
			// given
			hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
			defer resetCache()
			// when
			_, err := getTierTemplate(hostCluster, "other-other-other")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to retrieve the TierTemplate 'other-other-other' from 'Host' cluster")
		})

		t.Run("templateRef is not provided", func(t *testing.T) {
			// given
			hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
			defer resetCache()
			// when
			_, err := getTierTemplate(hostCluster, "")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "templateRef is not provided - it's not possible to fetch related TierTemplate resource")
		})
	})

	t.Run("test multiple retrievals in parallel", func(t *testing.T) {
		// given
		defer resetCache()
		var latch sync.WaitGroup
		latch.Add(1)
		var waitForFinished sync.WaitGroup

		for _, tierTemplate := range []*toolchainv1alpha1.TierTemplate{basicTierCode, basicTierDev, basicTierStage, basicTierCluster, advancedTierCode, advancedTierDev, advancedTierStage} {
			for i := 0; i < 1000; i++ {
				waitForFinished.Add(1)
				go func() {
					// given
					defer waitForFinished.Done()
					latch.Wait()
					hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
					if _, ok := tierTemplatesCache.get(tierTemplate.Name); ok {
						hostCluster = test.NewGetHostCluster(cl, true, apiv1.ConditionFalse)
					}

					// when
					retrievedTierTemplate, err := getTierTemplate(hostCluster, tierTemplate.Name)

					// then
					assert.NoError(t, err)
					assertThatTierTemplateIsSameAs(t, tierTemplate, retrievedTierTemplate)
				}()
			}
		}

		// when
		latch.Done()

		// then
		waitForFinished.Wait()
	})
}

func resetCache() func() {
	reset := func() {
		tierTemplatesCache = newTierTemplateCache()
	}
	reset()
	return reset
}

func assertThatTierTemplateIsSameAs(t *testing.T, expected *toolchainv1alpha1.TierTemplate, actual *tierTemplate) {
	assert.Equal(t, expected.Spec.Type, actual.typeName)
	assert.Equal(t, expected.Spec.Template, actual.template)
	assert.Equal(t, expected.Name, actual.templateRef)
	assert.Equal(t, expected.Spec.TierName, actual.tierName)
}
