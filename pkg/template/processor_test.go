package template_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/template"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	authv1 "github.com/openshift/api/authorization/v1"
	uuid "github.com/satori/go.uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var templateMetadata = `
apiVersion: template.openshift.io/v1
kind: Template
metadata:
  labels:
    provider: codeready-toolchain
    version: ${COMMIT}
  name: basic-tier-template
objects:`

var namespaceObj = `
- apiVersion: v1
  kind: Namespace
  metadata:
    annotations:
      openshift.io/description: ${PROJECT_NAME}-user
      openshift.io/display-name: ${PROJECT_NAME}
      openshift.io/requester: ${USER_NAME}
    labels:
      provider: codeready-toolchain
      version: ${COMMIT}
    name: ${PROJECT_NAME}`

var roleBindingObj = `
- apiVersion: authorization.openshift.io/v1
  kind: RoleBinding
  metadata:
    name: ${PROJECT_NAME}-edit
    namespace: ${PROJECT_NAME}
  roleRef:
    kind: ClusterRole
    name: edit
  subjects:
  - kind: User
    name: ${USER_NAME}`

var templateParams = `
parameters:
- name: PROJECT_NAME
  required: true
- name: USER_NAME
  value: toolchain-dev
  required: true
- name: COMMIT
  value: 123abc
  required: true
`

var newUser = `
  - kind: User
    name: newUser
`

func TestProcess(t *testing.T) {
	t.Run("should process template successfully", func(t *testing.T) {
		// given
		s := addToScheme(t)
		namespace, commit, user := templateVars()
		values := paramsKeyValues(namespace, commit, user)

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(namespaceObj, roleBindingObj), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)

		// namespace
		verifyResource(t, objs[0].Object.GetObjectKind(), namespace, commit, user)

		// role binding
		verifyResource(t, objs[1].Object.GetObjectKind(), namespace, user)
	})

	t.Run("should overwrite default value of commit parameter", func(t *testing.T) {
		// given
		s := addToScheme(t)
		namespace, commit, _ := templateVars()

		userDefault := "toolchain-dev"
		values := map[string]string{
			"PROJECT_NAME": namespace,
			"COMMIT":       commit,
		}

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(namespaceObj, roleBindingObj), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)

		// namespace
		verifyResource(t, objs[0].Object.GetObjectKind(), namespace, commit, userDefault)

		// role binding
		verifyResource(t, objs[1].Object.GetObjectKind(), namespace, userDefault)
	})

	t.Run("should not fail for random extra param", func(t *testing.T) {
		// given
		s := addToScheme(t)
		namespace, commit, user := templateVars()
		values := paramsKeyValues(namespace, commit, user)

		// adding random param
		random := getNameWithTimestamp("random")
		values[random] = random

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(namespaceObj), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 1)
		// namespace
		verifyResource(t, objs[0].Object.GetObjectKind(), namespace, commit, user)
	})

	t.Run("should process template with default parameters", func(t *testing.T) {
		// given
		s := addToScheme(t)
		// default values
		commit, user := "123abc", "toolchain-dev"

		namespace := uuid.NewV4().String()
		values := make(map[string]string)
		values["PROJECT_NAME"] = namespace

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(namespaceObj, roleBindingObj), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)

		// namespace
		verifyResource(t, objs[0].Object.GetObjectKind(), namespace, commit, user)

		// role binding
		verifyResource(t, objs[1].Object.GetObjectKind(), namespace, user)
	})

	t.Run("should process template with missing required params", func(t *testing.T) {
		// given
		s := addToScheme(t)
		values := make(map[string]string)

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(namespaceObj, roleBindingObj), values)

		// then
		require.Error(t, err, "fail to process as not providing required param PROJECT_NAME")
		assert.Nil(t, objs)
	})

	t.Run("should fail to process template for invalid template content", func(t *testing.T) {
		// given
		s := addToScheme(t)
		namespace, commit, user := templateVars()
		values := paramsKeyValues(namespace, commit, user)

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		_, err := p.Process([]byte(namespaceObj), values)

		// then
		assert.Error(t, err)
	})
}

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

	t.Run("filter namespaces", func(t *testing.T) {

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

func TestProcessAndApply(t *testing.T) {

	t.Run("should create namespace alone", func(t *testing.T) {

		// given
		s := addToScheme(t)
		namespace, commit, user := templateVars()
		values := paramsKeyValues(namespace, commit, user)
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(namespaceObj), values)
		require.NoError(t, err)
		err = p.Apply(objs, 2*time.Second)

		// then
		require.NoError(t, err)
		verifyNamespace(t, cl, namespace)
	})

	t.Run("should create role binding alone", func(t *testing.T) {
		// given
		s := addToScheme(t)
		namespace, commit, user := templateVars()
		values := paramsKeyValues(namespace, commit, user)

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(roleBindingObj), values)
		require.NoError(t, err)
		err = p.Apply(objs, 2*time.Second)

		// then
		require.NoError(t, err)
		verifyRoleBinding(t, cl, namespace)
	})

	t.Run("should create namespace and role binding", func(t *testing.T) {

		t.Run("success", func(t *testing.T) {
			// given
			s := addToScheme(t)
			namespace, commit, user := templateVars()
			values := paramsKeyValues(namespace, commit, user)
			cl := test.NewFakeClient(t)
			p := template.NewProcessor(cl, s)

			// when
			objs, err := p.Process(templateContent(namespaceObj, roleBindingObj), values)
			require.NoError(t, err)
			err = p.Apply(objs, 2*time.Second)

			// then
			require.NoError(t, err)
			verifyNamespace(t, cl, namespace)
			verifyRoleBinding(t, cl, namespace)
		})

		t.Run("failure", func(t *testing.T) {

			t.Run("client error", func(t *testing.T) {

				t.Run("namespace does not exist", func(t *testing.T) {
					// given
					s := addToScheme(t)
					namespace, commit, user := templateVars()
					values := paramsKeyValues(namespace, commit, user)

					cl := test.NewFakeClient(t)
					cl.MockCreate = func(ctx context.Context, obj runtime.Object) error {
						if obj.GetObjectKind().GroupVersionKind().Kind == "RoleBinding" {
							return errors.New("mock error")
						}
						return nil
					}
					p := template.NewProcessor(cl, s)

					// when
					objs, err := p.Process(templateContent(namespaceObj, roleBindingObj), values)
					require.NoError(t, err)
					err = p.Apply(objs, 2*time.Second)

					// then
					require.Error(t, err)
				})
			})

		})
	})

	t.Run("should update existing role binding", func(t *testing.T) {
		// given
		s := addToScheme(t)
		namespace, commit, user := templateVars()
		values := paramsKeyValues(namespace, commit, user)

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		objs, err := p.Process(templateContent(roleBindingObj), values)
		require.NoError(t, err)
		err = p.Apply(objs, 2*time.Second)

		require.NoError(t, err)
		verifyRoleBinding(t, cl, namespace)

		// when
		objs, err = p.Process(templateContent(roleBindingObj, newUser), values)
		require.NoError(t, err)
		err = p.Apply(objs, 2*time.Second)

		// then
		require.NoError(t, err)
		binding := verifyRoleBinding(t, cl, namespace)
		require.Len(t, binding.Subjects, 2)
	})

	t.Run("should fail to create template object", func(t *testing.T) {
		// given
		cl := test.NewFakeClient(t)
		cl.MockCreate = func(ctx context.Context, obj runtime.Object) error {
			return errors.New("failed to create resource")
		}

		s := addToScheme(t)
		namespace, commit, user := templateVars()

		values := paramsKeyValues(namespace, commit, user)

		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(roleBindingObj), values)
		require.NoError(t, err)
		err = p.Apply(objs, 2*time.Second)

		// then
		require.Error(t, err)
	})

	t.Run("should fail to update template object", func(t *testing.T) {
		// given
		cl := test.NewFakeClient(t)
		cl.MockUpdate = func(ctx context.Context, obj runtime.Object) error {
			return errors.New("failed to update resource")
		}

		s := addToScheme(t)
		namespace, commit, user := templateVars()

		values := paramsKeyValues(namespace, commit, user)

		p := template.NewProcessor(cl, s)
		objs, err := p.Process(templateContent(roleBindingObj), values)
		require.NoError(t, err)
		err = p.Apply(objs, 2*time.Second)
		require.NoError(t, err)

		// when
		objs, err = p.Process(templateContent(roleBindingObj), values)
		require.NoError(t, err)
		err = p.Apply(objs, 2*time.Second)

		// then
		assert.Error(t, err)
	})
}

func addToScheme(t *testing.T) *runtime.Scheme {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	return s
}

func templateContent(objs ...string) []byte {
	tmpl := templateMetadata
	for _, obj := range objs {
		tmpl += obj
	}

	return []byte(tmpl + templateParams)
}

func paramsKeyValues(namespace, commit, user string) map[string]string {
	return map[string]string{
		"PROJECT_NAME": namespace,
		"COMMIT":       commit,
		"USER_NAME":    user,
	}
}

func templateVars() (string, string, string) {
	return getNameWithTimestamp("namespace"), getNameWithTimestamp("sha"), getNameWithTimestamp("user")
}

func getNameWithTimestamp(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func verifyResource(t *testing.T, objKind schema.ObjectKind, vars ...string) {
	require.IsType(t, &unstructured.Unstructured{}, objKind)
	obj := objKind.(*unstructured.Unstructured)
	objJson, err := obj.MarshalJSON()
	require.NoError(t, err, "failed to marshal json from unstructured object")
	for _, v := range vars {
		assert.Contains(t, string(objJson), v, fmt.Sprintf("missing %s", v))
	}
}

func verifyNamespace(t *testing.T, c client.Client, nsName string) {
	// check that the namespace was created
	var ns corev1.Namespace
	err := c.Get(context.TODO(), types.NamespacedName{Name: nsName, Namespace: ""}, &ns) // namespace is cluster-scoped

	require.NoError(t, err)
	assert.NotNil(t, ns)
}

func verifyRoleBinding(t *testing.T, c client.Client, ns string) authv1.RoleBinding {
	// check that the rolebinding is created in the namespace
	// (the fake client just records the request but does not perform any consistency check)
	var rb authv1.RoleBinding
	err := c.Get(context.TODO(), types.NamespacedName{
		Namespace: ns,
		Name:      fmt.Sprintf("%s-edit", ns),
	}, &rb)

	require.NoError(t, err)
	require.NotNil(t, rb)
	return rb
}
