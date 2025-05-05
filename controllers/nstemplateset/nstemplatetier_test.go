package nstemplateset

import (
	"context"
	"sync"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/test"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	testcommon "github.com/codeready-toolchain/toolchain-common/pkg/test"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	templatev1 "github.com/openshift/api/template/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
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

func TestProcess(t *testing.T) {
	// given
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	codecFactory := serializer.NewCodecFactory(s)
	decoder := codecFactory.UniversalDeserializer()
	tmpl := templatev1.Template{}
	tmplContent := `
apiVersion: template.openshift.io/v1
kind: Template
metadata:
  name: test
objects:
- apiVersion: v1
  kind: ConfigMap
  metadata:
    name: ${SPACE_NAME}
    namespace: ${MEMBER_OPERATOR_NAMESPACE}
  data:
    test: test
parameters:
- name: SPACE_NAME
  required: true
- name: MEMBER_OPERATOR_NAMESPACE
  required: true
`
	_, _, err = decoder.Decode([]byte(tmplContent), nil, &tmpl)
	require.NoError(t, err)
	tierTemplate := &tierTemplate{
		template: tmpl,
	}

	restore := testcommon.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	// when
	obj, err := tierTemplate.process(s, map[string]string{
		SpaceName: "johnsmith",
	})

	// then
	require.NoError(t, err)
	require.Len(t, obj, 1)
	assert.Equal(t, "my-member-operator-namespace", obj[0].GetNamespace())
	assert.Equal(t, "johnsmith", obj[0].GetName())
}

func TestGetTierTemplate(t *testing.T) {

	// given
	// Setup Scheme for all resources (required before adding objects in the fake client)
	err := apis.AddToScheme(scheme.Scheme)
	require.NoError(t, err)

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
	ctx := context.TODO()

	t.Run("return code for basic tier", func(t *testing.T) {
		// given
		hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
		defer resetCache()
		// when
		tierTmpl, err := getTierTemplate(ctx, hostCluster, "basic-code-abcdef")

		// then
		require.NoError(t, err)
		assertThatTierTemplateIsSameAs(t, basicTierCode, tierTmpl)
	})

	t.Run("return dev for advanced tier", func(t *testing.T) {
		// given
		hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
		defer resetCache()
		// when
		tierTmpl, err := getTierTemplate(ctx, hostCluster, "advanced-dev-789012")

		// then
		require.NoError(t, err)
		assertThatTierTemplateIsSameAs(t, advancedTierDev, tierTmpl)
	})

	t.Run("return cluster type for basic tier", func(t *testing.T) {
		// given
		hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
		defer resetCache()
		// when
		tierTmpl, err := getTierTemplate(ctx, hostCluster, "basic-clusterresources-aa11bb22")

		// then
		require.NoError(t, err)
		assertThatTierTemplateIsSameAs(t, basicTierCluster, tierTmpl)
	})

	t.Run("test cache", func(t *testing.T) {
		// given
		hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
		defer resetCache()
		_, err := getTierTemplate(ctx, hostCluster, "basic-code-abcdef")
		require.NoError(t, err)

		t.Run("return cached TierTemplate even when for the second call doesn't exist", func(t *testing.T) {

			emptyHost := test.NewGetHostCluster(testcommon.NewFakeClient(t), true, apiv1.ConditionTrue)

			// when
			tierTmpl, err := getTierTemplate(ctx, emptyHost, "basic-code-abcdef")

			// then
			require.NoError(t, err)
			assertThatTierTemplateIsSameAs(t, basicTierCode, tierTmpl)
		})

		t.Run("return cached TierTemplate even when the host cluster was removed", func(t *testing.T) {
			// given
			noCluster := test.NewGetHostCluster(cl, false, apiv1.ConditionFalse)

			// when
			tierTmpl, err := getTierTemplate(ctx, noCluster, "basic-code-abcdef")

			// then
			require.NoError(t, err)
			assertThatTierTemplateIsSameAs(t, basicTierCode, tierTmpl)
		})

		t.Run("return cached TierTemplate even when the host cluster is not ready", func(t *testing.T) {
			// given
			noCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionFalse)

			// when
			tierTmpl, err := getTierTemplate(ctx, noCluster, "basic-code-abcdef")

			// then
			require.NoError(t, err)
			assertThatTierTemplateIsSameAs(t, basicTierCode, tierTmpl)
		})

		t.Run("return cached TierTemplate even when the host cluster is not ready", func(t *testing.T) {
			// given
			noCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionFalse)

			// when
			tierTmpl, err := getTierTemplate(ctx, noCluster, "basic-code-abcdef")

			// then
			require.NoError(t, err)
			assertThatTierTemplateIsSameAs(t, basicTierCode, tierTmpl)
		})
	})

	t.Run("failures", func(t *testing.T) {
		// given - no matter if one TierTemplate is cached
		hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
		defer resetCache()
		_, err := getTierTemplate(ctx, hostCluster, "basic-code-abcdef")
		require.NoError(t, err)

		t.Run("host cluster not available", func(t *testing.T) {
			// given
			hostCluster := test.NewGetHostCluster(cl, false, apiv1.ConditionFalse)
			resetCache()
			// when
			_, err := getTierTemplate(ctx, hostCluster, "advanced-dev-789012")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to connect to the host cluster: unknown cluster")
		})

		t.Run("host cluster not ready", func(t *testing.T) {
			// given
			hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionFalse)
			defer resetCache()
			// when
			_, err := getTierTemplate(ctx, hostCluster, "advanced-dev-789012")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "the host cluster is not ready")
		})

		t.Run("unknown templateRef", func(t *testing.T) {
			// given
			hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
			defer resetCache()
			// when
			_, err := getTierTemplate(ctx, hostCluster, "unknown")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to retrieve the TierTemplate 'unknown' from 'Host' cluster")
		})

		t.Run("tier in another namespace", func(t *testing.T) {
			// given
			hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
			defer resetCache()
			// when
			_, err := getTierTemplate(ctx, hostCluster, "other-other-other")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to retrieve the TierTemplate 'other-other-other' from 'Host' cluster")
		})

		t.Run("templateRef is not provided", func(t *testing.T) {
			// given
			hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
			defer resetCache()
			// when
			_, err := getTierTemplate(ctx, hostCluster, "")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "templateRef is not provided - it's not possible to fetch related TierTemplate/TierTemplateRevision resource")
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
				go func(tierTemplate *toolchainv1alpha1.TierTemplate) {
					// given
					defer waitForFinished.Done()
					latch.Wait()
					hostCluster := test.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
					if _, ok := tierTemplatesCache.get(tierTemplate.Name); ok {
						hostCluster = test.NewGetHostCluster(cl, true, apiv1.ConditionFalse)
					}

					// when
					retrievedTierTemplate, err := getTierTemplate(ctx, hostCluster, tierTemplate.Name)

					// then
					assert.NoError(t, err) // require must only be used in the goroutine running the test function (testifylint)
					assertThatTierTemplateIsSameAs(t, tierTemplate, retrievedTierTemplate)
				}(tierTemplate)
			}
		}

		// when
		latch.Done()

		// then
		waitForFinished.Wait()
	})
}

func resetCache() {
	tierTemplatesCache = newTierTemplateCache()
}

func assertThatTierTemplateIsSameAs(t *testing.T, expected *toolchainv1alpha1.TierTemplate, actual *tierTemplate) {
	assert.Equal(t, expected.Spec.Type, actual.typeName)
	assert.Equal(t, expected.Spec.Template, actual.template)
	assert.Equal(t, expected.Name, actual.templateRef)
	assert.Equal(t, expected.Spec.TierName, actual.tierName)
}
