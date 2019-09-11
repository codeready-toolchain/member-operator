package template_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	texttemplate "text/template"
	"time"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/template"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	authv1 "github.com/openshift/api/authorization/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestProcess(t *testing.T) {

	namespace := getNameWithTimestamp("namespace")
	user := getNameWithTimestamp("user")
	defaultUser := "toolchain-dev"
	commit := getNameWithTimestamp("sha")
	defaultCommit := "123abc"

	t.Run("should process template successfully", func(t *testing.T) {
		// given
		s := addToScheme(t)
		values := map[string]string{
			"PROJECT_NAME": namespace,
			"COMMIT":       commit,
			"USER_NAME":    user,
		}
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)
		// when
		objs, err := p.Process([]byte(namespaceAndRolebindingTmpl), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)
		// assert namespace
		assertObject(t, expectedObj{
			template:    namespaceObj,
			projectName: namespace,
			username:    user,
			commit:      commit,
		}, objs[0])
		// assert rolebinding
		assertObject(t, expectedObj{
			template:    rolebindingObj,
			projectName: namespace,
			username:    user,
			commit:      commit,
		}, objs[1])

	})

	t.Run("should overwrite default value of commit parameter", func(t *testing.T) {
		// given
		s := addToScheme(t)
		values := map[string]string{
			"PROJECT_NAME": namespace,
			"COMMIT":       commit,
		}
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)
		// when
		// when
		objs, err := p.Process([]byte(namespaceAndRolebindingTmpl), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)

		// assert namespace
		assertObject(t, expectedObj{
			template:    namespaceObj,
			projectName: namespace,
			username:    defaultUser,
			commit:      commit,
		}, objs[0])
		// assert rolebinding
		assertObject(t, expectedObj{
			template:    rolebindingObj,
			projectName: namespace,
			username:    defaultUser,
			commit:      commit,
		}, objs[1])
	})

	t.Run("should not fail for random extra param", func(t *testing.T) {
		// given
		s := addToScheme(t)
		random := getNameWithTimestamp("random")
		values := map[string]string{
			"PROJECT_NAME": namespace,
			"COMMIT":       commit,
			"USER_NAME":    user,
			"random":       random, // extra, unused param
		}
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)
		// when
		objs, err := p.Process([]byte(namespaceTmpl), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 1)
		// assert namespace
		assertObject(t, expectedObj{
			template:    namespaceObj,
			projectName: namespace,
			username:    user,
			commit:      commit,
		}, objs[0])
	})

	t.Run("should process template with default parameters", func(t *testing.T) {
		// given
		s := addToScheme(t)
		// default values
		commit := "123abc"
		user := "toolchain-dev"
		values := make(map[string]string)
		values["PROJECT_NAME"] = namespace
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process([]byte(namespaceAndRolebindingTmpl), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)
		// assert namespace
		assertObject(t, expectedObj{
			template:    namespaceObj,
			projectName: namespace,
			username:    user,
			commit:      commit,
		}, objs[0])
		// assert rolebinding
		assertObject(t, expectedObj{
			template:    rolebindingObj,
			projectName: namespace,
			username:    user,
			commit:      commit,
		}, objs[1])
	})

	t.Run("process template should fail because of missing required parameter", func(t *testing.T) {
		// given
		s := addToScheme(t)
		values := make(map[string]string)
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process([]byte(namespaceAndRolebindingTmpl), values)

		// then
		require.Error(t, err, "fail to process as not providing required param PROJECT_NAME")
		assert.Nil(t, objs)
	})

	t.Run("should fail to process template for invalid template content", func(t *testing.T) {
		// given
		s := addToScheme(t)
		values := map[string]string{
			"PROJECT_NAME": namespace,
			"COMMIT":       commit,
			"USER_NAME":    user,
		}
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		_, err := p.Process([]byte(invalidTmpl), values)

		// then
		assert.Error(t, err)
	})

	t.Run("filter results", func(t *testing.T) {

		t.Run("return namespace", func(t *testing.T) {
			// given
			s := addToScheme(t)
			// default values
			values := map[string]string{
				"PROJECT_NAME": namespace,
			}
			cl := test.NewFakeClient(t)
			p := template.NewProcessor(cl, s)

			// when
			objs, err := p.Process([]byte(namespaceAndRolebindingTmpl), values, template.RetainNamespaces)

			// then
			require.NoError(t, err)
			require.Len(t, objs, 1)
			// assert rolebinding
			assertObject(t, expectedObj{
				template:    namespaceObj,
				projectName: namespace,
				username:    defaultUser,
				commit:      defaultCommit,
			}, objs[0])
		})

		t.Run("return other resources", func(t *testing.T) {
			// given
			s := addToScheme(t)
			// default values
			values := map[string]string{
				"PROJECT_NAME": namespace,
				"COMMIT":       commit,
				"USER_NAME":    user,
			}
			cl := test.NewFakeClient(t)
			p := template.NewProcessor(cl, s)

			// when
			objs, err := p.Process([]byte(namespaceAndRolebindingTmpl), values, template.RetainAllButNamespaces)

			// then
			require.NoError(t, err)
			require.Len(t, objs, 1)
			// assert namespace
			assertObject(t, expectedObj{
				template:    rolebindingObj,
				projectName: namespace,
				username:    user,
				commit:      commit,
			}, objs[0])
		})

	})
}

func TestProcessAndApply(t *testing.T) {

	namespace := getNameWithTimestamp("namespace")
	commit := getNameWithTimestamp("sha")
	user := getNameWithTimestamp("user")

	t.Run("should create namespace alone", func(t *testing.T) {
		// given
		s := addToScheme(t)
		values := map[string]string{
			"PROJECT_NAME": namespace,
			"COMMIT":       commit,
			"USER_NAME":    user,
		}
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process([]byte(namespaceTmpl), values)
		require.NoError(t, err)
		err = p.Apply(objs)

		// then
		require.NoError(t, err)
		assertNamespaceExists(t, cl, namespace)
	})

	t.Run("should create role binding alone", func(t *testing.T) {
		// given
		s := addToScheme(t)
		values := map[string]string{
			"PROJECT_NAME": namespace,
			"COMMIT":       commit,
			"USER_NAME":    user,
		}

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process([]byte(rolebindingTmpl), values)
		require.NoError(t, err)
		err = p.Apply(objs)

		// then
		require.NoError(t, err)
		assertRoleBindingExists(t, cl, namespace)
	})

	t.Run("should create namespace and role binding", func(t *testing.T) {

		t.Run("success", func(t *testing.T) {
			// given
			s := addToScheme(t)
			values := map[string]string{
				"PROJECT_NAME": namespace,
				"COMMIT":       commit,
				"USER_NAME":    user,
			}
			cl := test.NewFakeClient(t)
			p := template.NewProcessor(cl, s)

			// when
			objs, err := p.Process([]byte(namespaceAndRolebindingTmpl), values)
			require.NoError(t, err)
			err = p.Apply(objs)

			// then
			require.NoError(t, err)
			assertNamespaceExists(t, cl, namespace)
			assertRoleBindingExists(t, cl, namespace)
		})

		t.Run("failure", func(t *testing.T) {

			t.Run("client error", func(t *testing.T) {

				t.Run("namespace does not exist", func(t *testing.T) {
					// given
					s := addToScheme(t)
					values := map[string]string{
						"PROJECT_NAME": namespace,
						"COMMIT":       commit,
						"USER_NAME":    user,
					}
					cl := test.NewFakeClient(t)
					cl.MockCreate = func(ctx context.Context, obj runtime.Object) error {
						if obj.GetObjectKind().GroupVersionKind().Kind == "RoleBinding" {
							return errors.New("mock error")
						}
						return nil
					}
					p := template.NewProcessor(cl, s)

					// when
					objs, err := p.Process([]byte(namespaceAndRolebindingTmpl), values)
					require.NoError(t, err)
					err = p.Apply(objs)

					// then
					require.Error(t, err)
				})
			})

		})
	})

	t.Run("should update existing role binding", func(t *testing.T) {
		// given
		s := addToScheme(t)
		values := map[string]string{
			"PROJECT_NAME": namespace,
			"COMMIT":       commit,
			"USER_NAME":    user,
		}

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)
		objs, err := p.Process([]byte(rolebindingTmpl), values)
		require.NoError(t, err)
		err = p.Apply(objs)
		require.NoError(t, err)
		assertRoleBindingExists(t, cl, namespace)

		// when
		objs, err = p.Process([]byte(namespaceAndRolebindingWithExtraUserTmpl), values)
		require.NoError(t, err)
		err = p.Apply(objs)

		// then
		require.NoError(t, err)
		binding := assertRoleBindingExists(t, cl, namespace)
		require.Len(t, binding.Subjects, 2)
	})

	t.Run("should fail to create template object", func(t *testing.T) {
		// given
		cl := test.NewFakeClient(t)
		cl.MockCreate = func(ctx context.Context, obj runtime.Object) error {
			return errors.New("failed to create resource")
		}
		s := addToScheme(t)
		values := map[string]string{
			"PROJECT_NAME": namespace,
			"COMMIT":       commit,
			"USER_NAME":    user,
		}
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process([]byte(rolebindingTmpl), values)
		require.NoError(t, err)
		err = p.Apply(objs)

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
		values := map[string]string{
			"PROJECT_NAME": namespace,
			"COMMIT":       commit,
			"USER_NAME":    user,
		}
		p := template.NewProcessor(cl, s)
		objs, err := p.Process([]byte(rolebindingTmpl), values)
		require.NoError(t, err)
		err = p.Apply(objs)
		require.NoError(t, err)

		// when
		objs, err = p.Process([]byte(rolebindingTmpl), values)
		require.NoError(t, err)
		err = p.Apply(objs)

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

func getNameWithTimestamp(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func assertObject(t *testing.T, expectedObj expectedObj, actual runtime.RawExtension) {
	// objJson, err := actual.Marshal()
	// require.NoError(t, err, "failed to marshal json from unstructured object")
	expected, err := newObject(expectedObj.template, expectedObj.projectName, expectedObj.username, expectedObj.commit)
	require.NoError(t, err, "failed to create object from template")
	assert.Equal(t, expected, actual.Object)
}

type expectedObj struct {
	template    string
	projectName string
	username    string
	commit      string
}

func newObject(template, projectName, username, commit string) (*unstructured.Unstructured, error) {
	tmpl := texttemplate.New("")
	tmpl, err := tmpl.Parse(template)
	if err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(nil)
	err = tmpl.Execute(buf, struct {
		ProjectName string
		Username    string
		Commit      string
	}{
		ProjectName: projectName,
		Username:    username,
		Commit:      commit,
	})
	if err != nil {
		return nil, err
	}
	result := &unstructured.Unstructured{}
	err = result.UnmarshalJSON(buf.Bytes())
	return result, err
}

func assertNamespaceExists(t *testing.T, c client.Client, nsName string) {
	// check that the namespace was created
	var ns corev1.Namespace
	err := c.Get(context.TODO(), types.NamespacedName{Name: nsName, Namespace: ""}, &ns) // assert namespace is cluster-scoped

	require.NoError(t, err)
	assert.NotNil(t, ns)
}

func assertRoleBindingExists(t *testing.T, c client.Client, ns string) authv1.RoleBinding {
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

const (
	namespaceTmpl = `apiVersion: template.openshift.io/v1
kind: Template
metadata:
  labels:
    provider: codeready-toolchain
    version: ${COMMIT}
  name: basic-tier-template
objects:
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
    name: ${PROJECT_NAME}
parameters:
- name: PROJECT_NAME
  required: true
- name: USER_NAME
  value: toolchain-dev
  required: true
- name: COMMIT
  value: 123abc
  required: true`

	rolebindingTmpl = `apiVersion: template.openshift.io/v1
kind: Template
metadata:
  labels:
    provider: codeready-toolchain
    version: ${COMMIT}
  name: basic-tier-template
objects:
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
    name: ${USER_NAME}
parameters:
- name: PROJECT_NAME
  required: true
- name: USER_NAME
  value: toolchain-dev
  required: true
- name: COMMIT
  value: 123abc
  required: true`

	namespaceAndRolebindingTmpl = `apiVersion: template.openshift.io/v1
kind: Template
metadata:
  labels:
    provider: codeready-toolchain
    version: ${COMMIT}
  name: basic-tier-template
objects:
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
    name: ${PROJECT_NAME}
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
    name: ${USER_NAME}
parameters:
- name: PROJECT_NAME
  required: true
- name: USER_NAME
  value: toolchain-dev
  required: true
- name: COMMIT
  value: 123abc
  required: true`

	namespaceAndRolebindingWithExtraUserTmpl = `apiVersion: template.openshift.io/v1
kind: Template
metadata:
  labels:
    provider: codeready-toolchain
    version: ${COMMIT}
  name: basic-tier-template
objects:
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
    name: ${PROJECT_NAME}
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
    name: ${USER_NAME}
  - kind: User
    name: extraUser
parameters:
- name: PROJECT_NAME
  required: true
- name: USER_NAME
  value: toolchain-dev
  required: true
- name: COMMIT
  value: 123abc
  required: true`

	invalidTmpl = `foo`

	namespaceObj = `{ 
	"apiVersion": "v1",
	"kind": "Namespace",
	"metadata": {
		"annotations": {
			"openshift.io/description": "{{ .ProjectName }}-user",
			"openshift.io/display-name": "{{ .ProjectName }}",
			"openshift.io/requester": "{{ .Username }}"
		},
		"labels": {
			"provider": "codeready-toolchain",
			"version": "{{ .Commit }}"
		},
		"name": "{{ .ProjectName }}"
	}
}`

	rolebindingObj = `{
	"apiVersion": "authorization.openshift.io/v1",
	"kind": "RoleBinding",
	"metadata": {
		"name": "{{ .ProjectName }}-edit",
    	"namespace": "{{ .ProjectName }}"
	},
	"roleRef": {
		"kind": "ClusterRole",
		"name": "edit"
	},
	"subjects": [
		{
			"kind": "User",
			"name": "{{ .Username }}"
		}
	]
}`

	rolebindingWithExtraUserObj = `{
	"apiVersion": "authorization.openshift.io/v1",
	"kind": "RoleBinding",
	"metadata": {
		"name": "{{ .ProjectName }}-edit",
    	"namespace": "{{ .ProjectName }}"
	}
	"roleRef": {
		"kind": "ClusterRole",
		"name": "edit"
	},
	"subjects": [
		{
			"kind": "User",
			"name": "{{ .Username }}"
		},
		{
			"kind": "extraUser",
			"name": "extra"
		}
	]
}`
)
