package nstemplateset

import (
	"context"
	"errors"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/test"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"
	testcommon "github.com/codeready-toolchain/toolchain-common/pkg/test"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	templatev1 "github.com/openshift/api/template/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Template constants to avoid duplication
const (
	configMapTemplate = `{
		"apiVersion": "v1",
		"kind": "ConfigMap",
		"metadata": {
			"name": "config-{{.SPACE_NAME}}",
			"namespace": "{{.NAMESPACE}}"
		},
		"data": {
			"config": "{{.CONFIG_VALUE}}"
		}
	}`
	// #nosec G101  // This is a test template, not a real secret and does not have any sensitive data or static values passed
	secTemplate = `{
		"apiVersion": "v1",
		"kind": "Secret",
		"metadata": {
			"name": "secret-{{.SPACE_NAME}}",
			"namespace": "{{.NAMESPACE}}"
		},
		"data": {
			"quota": "{{.QUOTA_LIMIT}}",
			"runtime-param": "{{.RUNTIME_PARAM}}"
		}
	}`

	namespaceTemplate = `{
		"apiVersion": "v1",
		"kind": "Namespace",
		"metadata": {
			"name": "ns-{{.SPACE_NAME}}"
		}
	}`

	invalidTemplate = `{
		"apiVersion": "v1",
		"kind": "ConfigMap",
		"metadata": {
			"name": "config-{{.SPACE_NAME",
			"namespace": "default"
		}
	}`

	invalidFunctionTemplate = `{
		"apiVersion": "v1",
		"kind": "ConfigMap",
		"metadata": {
			"name": "config-{{.SPACE_NAME | invalid_function}}",
			"namespace": "default"
		}
	}`

	invalidJSONTemplate = `{
		"apiVersion": "v1",
		"kind": "ConfigMap",
		"metadata": {
			"name": "config-{{.SPACE_NAME}}",
			"namespace": "default"
		},
		"data": {
			"invalid": "json"
		}
	` // Missing closing brace
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
	s := runtime.NewScheme()
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

func TestProcessWithTTRTable(t *testing.T) {
	// given
	standardRuntimeParams := map[string]string{
		"SPACE_NAME": "johnsmith",
	}
	s := runtime.NewScheme()
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	// Table-driven tests for common scenarios
	testCases := []templateProcessTestCase{
		{
			name:      "successful processing with valid templates",
			templates: []string{configMapTemplate},
			staticParams: []toolchainv1alpha1.Parameter{
				{Name: "CONFIG_VALUE", Value: "test-value"},
				{Name: "NAMESPACE", Value: "test-namespace"},
			},
			runtimeParams: standardRuntimeParams,
			expectedCount: 1,
			validate: func(t *testing.T, objects []runtimeclient.Object) {
				cm := objects[0]
				assert.Equal(t, "ConfigMap", cm.GetObjectKind().GroupVersionKind().Kind)
				assert.Equal(t, "config-johnsmith", cm.GetName())
				assert.Equal(t, "test-namespace", cm.GetNamespace())
			},
		},
		{
			name:      "parameter substitution with static and runtime params",
			templates: []string{secTemplate},
			staticParams: []toolchainv1alpha1.Parameter{
				{Name: "QUOTA_LIMIT", Value: "1000"},
				{Name: "NAMESPACE", Value: "test-ns"},
			},
			runtimeParams: map[string]string{
				"SPACE_NAME":    "testuser",
				"RUNTIME_PARAM": "runtime-value",
			},
			expectedCount: 1,
			validate: func(t *testing.T, objects []runtimeclient.Object) {
				secret := objects[0]
				assert.Equal(t, "Secret", secret.GetObjectKind().GroupVersionKind().Kind)
				assert.Equal(t, "secret-testuser", secret.GetName())
				assert.Equal(t, "test-ns", secret.GetNamespace())
			},
		},
		{
			name:          "empty template objects",
			templates:     []string{},
			staticParams:  []toolchainv1alpha1.Parameter{},
			runtimeParams: map[string]string{},
			expectedCount: 0,
		},
		{
			name:          "invalid template syntax",
			templates:     []string{invalidTemplate},
			staticParams:  []toolchainv1alpha1.Parameter{},
			runtimeParams: standardRuntimeParams,
			expectedError: "template:",
		},
		{
			name:          "template execution error - invalid function",
			templates:     []string{invalidFunctionTemplate},
			staticParams:  []toolchainv1alpha1.Parameter{},
			runtimeParams: standardRuntimeParams,
			expectedError: "invalid_function",
		},
		{
			name:          "invalid JSON in template",
			templates:     []string{invalidJSONTemplate},
			staticParams:  []toolchainv1alpha1.Parameter{},
			runtimeParams: standardRuntimeParams,
			expectedError: "unexpected end of JSON input",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			ttr := createTestTTR("test-ttr", tc.templates, tc.staticParams)
			tierTemplate := createTestTierTemplate(ttr)

			// when
			objects, err := tierTemplate.process(s, tc.runtimeParams, tc.filters...)

			// then
			if tc.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
				return
			}

			require.NoError(t, err)
			require.Len(t, objects, tc.expectedCount)
			if tc.validate != nil {
				tc.validate(t, objects)
			}
		})
	}

	t.Run("processing with filters", func(t *testing.T) {
		// given - create templates with both namespace and configmap
		templates := []string{namespaceTemplate, configMapTemplate}
		ttr := createTestTTR("test-ttr", templates, []toolchainv1alpha1.Parameter{
			{Name: "NAMESPACE", Value: "default"},
		})
		tierTemplate := createTestTierTemplate(ttr)
		runtimeParams := map[string]string{"SPACE_NAME": "testuser"}

		t.Run("filter namespaces only", func(t *testing.T) {
			// when
			objects, err := tierTemplate.process(s, runtimeParams, template.RetainNamespaces)

			// then
			require.NoError(t, err)
			require.Len(t, objects, 1)
			assert.Equal(t, "Namespace", objects[0].GetObjectKind().GroupVersionKind().Kind)
			assert.Equal(t, "ns-testuser", objects[0].GetName())
		})

		t.Run("filter non-namespace objects", func(t *testing.T) {
			// when
			objects, err := tierTemplate.process(s, runtimeParams, template.RetainAllButNamespaces)

			// then
			require.NoError(t, err)
			require.Len(t, objects, 1)
			assert.Equal(t, "ConfigMap", objects[0].GetObjectKind().GroupVersionKind().Kind)
			assert.Equal(t, "config-testuser", objects[0].GetName())
		})

		t.Run("custom filter function", func(t *testing.T) {
			// Custom filter to retain only ConfigMaps
			configMapFilter := func(obj runtime.RawExtension) bool {
				return obj.Object.GetObjectKind().GroupVersionKind().Kind == "ConfigMap"
			}

			// when
			objects, err := tierTemplate.process(s, runtimeParams, configMapFilter)

			// then
			require.NoError(t, err)
			require.Len(t, objects, 1)
			assert.Equal(t, "ConfigMap", objects[0].GetObjectKind().GroupVersionKind().Kind)
			assert.Equal(t, "config-testuser", objects[0].GetName())
		})
	})

	t.Run("complex template with multiple objects", func(t *testing.T) {
		// given
		nsTemplate := `{
			"apiVersion": "v1",
			"kind": "Namespace",
			"metadata": {
				"name": "{{.SPACE_NAME}}-dev",
				"labels": {
					"toolchain.dev.openshift.com/space": "{{.SPACE_NAME}}",
					"tier": "{{.TIER_NAME}}"
				}
			}
		}`

		rbTemplate := `{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind": "RoleBinding",
			"metadata": {
				"name": "{{.SPACE_NAME}}-admin",
				"namespace": "{{.SPACE_NAME}}-dev"
			},
			"subjects": [{
				"kind": "User",
				"name": "{{.USERNAME}}"
			}],
			"roleRef": {
				"kind": "ClusterRole",
				"name": "admin",
				"apiGroup": "rbac.authorization.k8s.io"
			}
		}`

		ttr := createTestTTR("test-ttr", []string{nsTemplate, rbTemplate}, []toolchainv1alpha1.Parameter{
			{Name: "TIER_NAME", Value: "basic"},
		})
		tierTemplate := createTestTierTemplate(ttr)

		runtimeParams := map[string]string{
			"SPACE_NAME": "johnsmith",
			"USERNAME":   "john.smith",
		}

		// when
		objects, err := tierTemplate.process(s, runtimeParams)

		// then
		require.NoError(t, err)
		require.Len(t, objects, 2)

		// Check namespace
		ns := objects[0]
		assert.Equal(t, "Namespace", ns.GetObjectKind().GroupVersionKind().Kind)
		assert.Equal(t, "johnsmith-dev", ns.GetName())
		assert.Equal(t, "johnsmith", ns.GetLabels()["toolchain.dev.openshift.com/space"])
		assert.Equal(t, "basic", ns.GetLabels()["tier"])

		// Check role binding
		rb := objects[1]
		assert.Equal(t, "RoleBinding", rb.GetObjectKind().GroupVersionKind().Kind)
		assert.Equal(t, "johnsmith-admin", rb.GetName())
		assert.Equal(t, "johnsmith-dev", rb.GetNamespace())
	})

}

func TestConvertParametersToMap(t *testing.T) {
	// given
	ttr := &toolchainv1alpha1.TierTemplateRevision{
		Spec: toolchainv1alpha1.TierTemplateRevisionSpec{
			Parameters: []toolchainv1alpha1.Parameter{
				{Name: "STATIC_PARAM1", Value: "static-value1"},
				{Name: "STATIC_PARAM2", Value: "static-value2"},
			},
		},
	}

	tierTemplate := createTestTierTemplate(ttr)

	runtimeParams := map[string]string{
		"RUNTIME_PARAM1": "runtime-value1",
		"RUNTIME_PARAM2": "runtime-value2",
		"STATIC_PARAM1":  "overridden-value", // Should override static param
	}

	// when
	result := tierTemplate.convertParametersToMap(runtimeParams)

	// then
	expected := map[string]string{
		"STATIC_PARAM1":  "overridden-value", // Runtime param overrides static
		"STATIC_PARAM2":  "static-value2",
		"RUNTIME_PARAM1": "runtime-value1",
		"RUNTIME_PARAM2": "runtime-value2",
	}

	assert.Equal(t, expected, result)
}
func TestGetTierTemplate(t *testing.T) {

	// given
	// Setup Scheme for all resources (required before adding objects in the fake client)
	s := runtime.NewScheme()
	err := apis.AddToScheme(s)
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

// Test helper functions and data structures for TestProcessGoTemplates
type templateProcessTestCase struct {
	name          string
	templates     []string
	staticParams  []toolchainv1alpha1.Parameter
	runtimeParams map[string]string
	filters       []template.FilterFunc
	expectedCount int
	expectedError string
	validate      func(t *testing.T, objects []runtimeclient.Object)
}

// Helper to create a TierTemplateRevision for testing
func createTestTTR(name string, templates []string, params []toolchainv1alpha1.Parameter) *toolchainv1alpha1.TierTemplateRevision {
	templateObjects := make([]runtime.RawExtension, len(templates))
	for i, tmpl := range templates {
		templateObjects[i] = runtime.RawExtension{Raw: []byte(tmpl)}
	}

	return &toolchainv1alpha1.TierTemplateRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "toolchain-host-operator",
		},
		Spec: toolchainv1alpha1.TierTemplateRevisionSpec{
			TemplateObjects: templateObjects,
			Parameters:      params,
		},
	}
}

// Helper to create a tierTemplate with TTR
func createTestTierTemplate(ttr *toolchainv1alpha1.TierTemplateRevision) *tierTemplate {
	return &tierTemplate{
		templateRef: "test-template",
		tierName:    "basic",
		typeName:    "dev",
		ttr:         ttr,
	}
}
