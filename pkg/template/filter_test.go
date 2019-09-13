package template_test

import (
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestFilter(t *testing.T) {

	ns1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "Namespace",
			"metadata": map[string]interface{}{
				"name": "ns1",
			},
		},
	}
	ns2 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "Namespace",
			"metadata": map[string]interface{}{
				"name": "ns2",
			},
		},
	}
	rb1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "RoleBinding",
			"metadata": map[string]interface{}{
				"name": "rb1",
			},
		},
	}
	rb2 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "RoleBinding",
			"metadata": map[string]interface{}{
				"name": "rb2",
			},
		},
	}

	t.Run("no filter", func(t *testing.T) {
		// given
		objs := []runtime.RawExtension{
			{
				Object: ns1,
			},
			{
				Object: rb1,
			},
			{
				Object: ns2,
			},
			{
				Object: rb2,
			},
		}
		// when
		result := template.Filter(objs)
		// then
		require.Len(t, result, 4)
		assert.Equal(t, ns1, result[0].Object)
		assert.Equal(t, rb1, result[1].Object)
		assert.Equal(t, ns2, result[2].Object)
		assert.Equal(t, rb2, result[3].Object)
	})

	t.Run("all filters", func(t *testing.T) {
		// given
		objs := []runtime.RawExtension{
			{
				Object: ns1,
			},
			{
				Object: rb1,
			},
			{
				Object: ns2,
			},
			{
				Object: rb2,
			},
		}
		// when
		result := template.Filter(objs, template.RetainNamespaces, template.RetainAllButNamespaces)
		// then
		require.Empty(t, result)
	})

	t.Run("filter namespaces", func(t *testing.T) {

		t.Run("with a single filter", func(t *testing.T) {

			t.Run("return one", func(t *testing.T) {
				// given
				objs := []runtime.RawExtension{
					{
						Object: ns1,
					},
					{
						Object: rb1,
					},
				}
				// when
				result := template.Filter(objs, template.RetainNamespaces)
				// then
				require.Len(t, result, 1)
				assert.Equal(t, ns1, result[0].Object)

			})
			t.Run("return multiple", func(t *testing.T) {
				// given
				objs := []runtime.RawExtension{
					{
						Object: ns1,
					},
					{
						Object: rb1,
					},
					{
						Object: ns2,
					},
					{
						Object: rb2,
					},
				}
				// when
				result := template.Filter(objs, template.RetainNamespaces)
				// then
				require.Len(t, result, 2)
				assert.Equal(t, ns1, result[0].Object)
				assert.Equal(t, ns2, result[1].Object)
			})
			t.Run("return none", func(t *testing.T) {
				// given
				objs := []runtime.RawExtension{
					{
						Object: rb1,
					},
					{
						Object: rb2,
					},
				}
				// when
				result := template.Filter(objs, template.RetainNamespaces)
				// then
				require.Empty(t, result)
			})
		})
	})

	t.Run("filter other resources", func(t *testing.T) {

		t.Run("return one", func(t *testing.T) {
			// given
			objs := []runtime.RawExtension{
				{
					Object: ns1,
				},
				{
					Object: rb1,
				},
			}
			// when
			result := template.Filter(objs, template.RetainAllButNamespaces)
			// then
			require.Len(t, result, 1)
			assert.Equal(t, rb1, result[0].Object)

		})
		t.Run("return multiple", func(t *testing.T) {
			// given
			objs := []runtime.RawExtension{
				{
					Object: ns1,
				},
				{
					Object: rb1,
				},
				{
					Object: ns2,
				},
				{
					Object: rb2,
				},
			}
			// when
			result := template.Filter(objs, template.RetainAllButNamespaces)
			// then
			require.Len(t, result, 2)
			assert.Equal(t, rb1, result[0].Object)
			assert.Equal(t, rb2, result[1].Object)
		})

		t.Run("return none", func(t *testing.T) {
			// given
			objs := []runtime.RawExtension{
				{
					Object: ns1,
				},
				{
					Object: ns2,
				},
			}
			// when
			result := template.Filter(objs, template.RetainAllButNamespaces)
			// then
			require.Empty(t, result)
		})
	})
}
