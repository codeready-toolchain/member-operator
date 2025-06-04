package nstemplateset

import (
	"context"
	"errors"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/test"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	testcommon "github.com/codeready-toolchain/toolchain-common/pkg/test"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	templatev1 "github.com/openshift/api/template/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

func TestProcessWithoutTTR(t *testing.T) {
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

func TestProcessWithTTR(t *testing.T) {
	// given
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	ttRev := createTierTemplateRevision("test-ttr")
	crq := newTestCRQ("600")
	ttRev.Spec.TemplateObjects = append(ttRev.Spec.TemplateObjects, runtime.RawExtension{Object: &crq})
	ttRev.Spec.Parameters = []toolchainv1alpha1.Parameter{
		{Name: "SPACE_NAME",
			Value: "test-space"},
		{Name: "DEPLOYMENT_QUOTA",
			Value: "600"},
	}

	tierTemplate := &tierTemplate{
		ttr: ttRev,
	}
	ttrObj, err := tierTemplate.process(s, map[string]string{
		SpaceName: "johnsmith",
	})

	// then
	require.NoError(t, err)
	require.Len(t, ttrObj, 1)
	require.Equal(t, "for-test-space-deployments", ttrObj[0].GetName())
	require.Equal(t, &expectedCRQ, ttrObj[0])

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

	t.Run("fetch ttr successfully and add it to the tiertemplate object", func(t *testing.T) {
		// given
		ttRev := createTierTemplateRevision("basic-clusterresources-aa11bb22")
		ttRev.Labels = map[string]string{
			toolchainv1alpha1.TierLabelKey:        "basic",
			toolchainv1alpha1.TemplateRefLabelKey: "basic-clusterresources-aa11bb22",
		}
		ctx := context.TODO()
		cl := testcommon.NewFakeClient(t, ttRev, basicTierCluster)
		hostCluster := test.NewHostClientGetter(cl, nil)
		//when
		ttrTmpl, err := getTierTemplate(ctx, hostCluster, "basic-clusterresources-aa11bb22")

		//then
		require.NoError(t, err)
		assert.Equal(t, ttrTmpl.ttr, ttRev)

	})
	t.Run("return code for basic tier", func(t *testing.T) {
		// given
		hostCluster := test.NewHostClientGetter(cl, nil)
		// when
		tierTmpl, err := getTierTemplate(ctx, hostCluster, "basic-code-abcdef")

		// then
		require.NoError(t, err)
		assertThatTierTemplateIsSameAs(t, basicTierCode, tierTmpl)
	})

	t.Run("return dev for advanced tier", func(t *testing.T) {
		// given
		hostCluster := test.NewHostClientGetter(cl, nil)
		// when
		tierTmpl, err := getTierTemplate(ctx, hostCluster, "advanced-dev-789012")

		// then
		require.NoError(t, err)
		assertThatTierTemplateIsSameAs(t, advancedTierDev, tierTmpl)
	})

	t.Run("return cluster type for basic tier", func(t *testing.T) {
		// given
		hostCluster := test.NewHostClientGetter(cl, nil)
		// when
		tierTmpl, err := getTierTemplate(ctx, hostCluster, "basic-clusterresources-aa11bb22")

		// then
		require.NoError(t, err)
		assertThatTierTemplateIsSameAs(t, basicTierCluster, tierTmpl)
	})

	t.Run("failures", func(t *testing.T) {

		t.Run("host cluster not available", func(t *testing.T) {
			// given
			hostCluster := test.NewHostClientGetter(cl, errors.New("some error"))
			// when
			_, err := getTierTemplate(ctx, hostCluster, "advanced-dev-789012")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to connect to the host cluster: some error")
		})

		t.Run("unknown templateRef", func(t *testing.T) {
			// given
			hostCluster := test.NewHostClientGetter(cl, nil)
			// when
			_, err := getTierTemplate(ctx, hostCluster, "unknown")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to retrieve the TierTemplate 'unknown' from 'Host' cluster")
		})

		t.Run("tier in another namespace", func(t *testing.T) {
			// given
			hostCluster := test.NewHostClientGetter(cl, nil)
			// when
			_, err := getTierTemplate(ctx, hostCluster, "other-other-other")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to retrieve the TierTemplate 'other-other-other' from 'Host' cluster")
		})

		t.Run("templateRef is not provided", func(t *testing.T) {
			// given
			hostCluster := test.NewHostClientGetter(cl, nil)
			// when
			_, err := getTierTemplate(ctx, hostCluster, "")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "templateRef is not provided - it's not possible to fetch related TierTemplate/TierTemplateRevision resource")
		})
	})
}

func assertThatTierTemplateIsSameAs(t *testing.T, expected *toolchainv1alpha1.TierTemplate, actual *tierTemplate) {
	assert.Equal(t, expected.Spec.Type, actual.typeName)
	assert.Equal(t, expected.Spec.Template, actual.template)
	assert.Equal(t, expected.Name, actual.templateRef)
	assert.Equal(t, expected.Spec.TierName, actual.tierName)
}

func newTestCRQ(podsCount string) unstructured.Unstructured {
	var crq = unstructured.Unstructured{Object: map[string]interface{}{
		"kind": "ClusterResourceQuota",
		"metadata": map[string]interface{}{
			"name": "for-{{.SPACE_NAME}}-deployments",
		},
		"spec": map[string]interface{}{
			"quota": map[string]interface{}{
				"hard": map[string]interface{}{
					"count/deploymentconfigs.apps": "{{.DEPLOYMENT_QUOTA}}",
					"count/deployments.apps":       "{{.DEPLOYMENT_QUOTA}}",
					"count/pods":                   podsCount,
				},
			},
			"selector": map[string]interface{}{
				"annotations": map[string]interface{}{},
				"labels": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"toolchain.dev.openshift.com/space": "'{{.SPACE_NAME}}'",
					},
				},
			},
		},
	}}
	return crq
}

var expectedCRQ = unstructured.Unstructured{
	Object: map[string]interface{}{
		"kind": "ClusterResourceQuota",
		"metadata": map[string]interface{}{
			"name": "for-test-space-deployments"},
		"spec": map[string]interface{}{
			"quota": map[string]interface{}{
				"hard": map[string]interface{}{
					"count/deploymentconfigs.apps": "600",
					"count/deployments.apps":       "600",
					"count/pods":                   "600"},
			},
			"selector": map[string]interface{}{
				"annotations": map[string]interface{}{},
				"labels": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"toolchain.dev.openshift.com/space": "'test-space'",
					},
				},
			},
		},
	},
}
