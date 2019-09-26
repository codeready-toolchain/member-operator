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
	errs "github.com/pkg/errors"

	authv1 "github.com/openshift/api/authorization/v1"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestProcess(t *testing.T) {

	user := getNameWithTimestamp("user")
	commit := getNameWithTimestamp("sha")
	defaultCommit := "123abc"

	s := addToScheme(t)
	codecFactory := serializer.NewCodecFactory(s)
	decoder := codecFactory.UniversalDeserializer()

	cl := test.NewFakeClient(t)
	p := template.NewProcessor(cl, s)

	t.Run("should process template successfully", func(t *testing.T) {
		// given
		values := map[string]string{
			"USERNAME": user,
			"COMMIT":   commit,
		}
		tmpl, err := decodeTemplate(decoder, namespaceAndRolebindingTmpl)
		require.NoError(t, err)

		// when
		objs, err := p.Process(tmpl, values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)
		// assert namespace
		assertObject(t, expectedObj{
			template: namespaceObj,
			username: user,
			commit:   commit,
		}, objs[0])
		// assert rolebinding
		assertObject(t, expectedObj{
			template: rolebindingObj,
			username: user,
			commit:   commit,
		}, objs[1])

	})

	t.Run("should overwrite default value of commit parameter", func(t *testing.T) {
		// given
		values := map[string]string{
			"USERNAME": user,
			"COMMIT":   commit,
		}
		tmpl, err := decodeTemplate(decoder, namespaceAndRolebindingTmpl)
		require.NoError(t, err)

		// when
		objs, err := p.Process(tmpl, values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)

		// assert namespace
		assertObject(t, expectedObj{
			template: namespaceObj,
			username: user,
			commit:   commit,
		}, objs[0])
		// assert rolebinding
		assertObject(t, expectedObj{
			template: rolebindingObj,
			username: user,
			commit:   commit,
		}, objs[1])
	})

	t.Run("should not fail for random extra param", func(t *testing.T) {
		// given
		random := getNameWithTimestamp("random")
		values := map[string]string{
			"USERNAME": user,
			"COMMIT":   commit,
			"random":   random, // extra, unused param
		}
		tmpl, err := decodeTemplate(decoder, namespaceTmpl)
		require.NoError(t, err)

		// when
		objs, err := p.Process(tmpl, values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 1)
		// assert namespace
		assertObject(t, expectedObj{
			template: namespaceObj,
			username: user,
			commit:   commit,
		}, objs[0])
	})

	t.Run("should process template with default commit parameter", func(t *testing.T) {
		// given
		values := map[string]string{
			"USERNAME": user,
		}
		tmpl, err := decodeTemplate(decoder, namespaceAndRolebindingTmpl)
		require.NoError(t, err)

		// when
		objs, err := p.Process(tmpl, values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)
		// assert namespace
		assertObject(t, expectedObj{
			template: namespaceObj,
			username: user,
			commit:   defaultCommit,
		}, objs[0])
		// assert rolebinding
		assertObject(t, expectedObj{
			template: rolebindingObj,
			username: user,
			commit:   defaultCommit,
		}, objs[1])
	})

	t.Run("should fail because of missing required parameter", func(t *testing.T) {
		// given
		values := make(map[string]string)
		tmpl, err := decodeTemplate(decoder, namespaceAndRolebindingTmpl)
		require.NoError(t, err)

		// when
		objs, err := p.Process(tmpl, values)

		// then
		require.Error(t, err, "fail to process as not providing required param USERNAME")
		assert.Nil(t, objs)
	})

	t.Run("filter results", func(t *testing.T) {

		t.Run("return namespace", func(t *testing.T) {
			// given
			values := map[string]string{
				"USERNAME": user,
			}
			tmpl, err := decodeTemplate(decoder, namespaceAndRolebindingTmpl)
			require.NoError(t, err)

			// when
			objs, err := p.Process(tmpl, values, template.RetainNamespaces)

			// then
			require.NoError(t, err)
			require.Len(t, objs, 1)
			// assert rolebinding
			assertObject(t, expectedObj{
				template: namespaceObj,
				username: user,
				commit:   defaultCommit,
			}, objs[0])
		})

		t.Run("return other resources", func(t *testing.T) {
			// given
			values := map[string]string{
				"USERNAME": user,
				"COMMIT":   commit,
			}
			tmpl, err := decodeTemplate(decoder, namespaceAndRolebindingTmpl)
			require.NoError(t, err)

			// when
			objs, err := p.Process(tmpl, values, template.RetainAllButNamespaces)

			// then
			require.NoError(t, err)
			require.Len(t, objs, 1)
			// assert namespace
			assertObject(t, expectedObj{
				template: rolebindingObj,
				username: user,
				commit:   commit,
			}, objs[0])
		})

	})
}

func TestProcessAndApply(t *testing.T) {

	commit := getNameWithTimestamp("sha")
	user := getNameWithTimestamp("user")

	s := addToScheme(t)
	codecFactory := serializer.NewCodecFactory(s)
	decoder := codecFactory.UniversalDeserializer()

	values := map[string]string{
		"USERNAME": user,
		"COMMIT":   commit,
	}

	t.Run("should create namespace alone", func(t *testing.T) {
		// given
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)
		tmpl, err := decodeTemplate(decoder, namespaceTmpl)
		require.NoError(t, err)
		objs, err := p.Process(tmpl, values)
		require.NoError(t, err)

		// when
		err = p.Apply(objs)

		// then
		require.NoError(t, err)
		assertNamespaceExists(t, cl, user)
	})

	t.Run("should create role binding alone", func(t *testing.T) {
		// given
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)
		tmpl, err := decodeTemplate(decoder, rolebindingTmpl)
		require.NoError(t, err)
		objs, err := p.Process(tmpl, values)
		require.NoError(t, err)

		// when
		err = p.Apply(objs)

		// then
		require.NoError(t, err)
		assertRoleBindingExists(t, cl, user)
	})

	t.Run("should create namespace and role binding", func(t *testing.T) {
		// given
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)
		tmpl, err := decodeTemplate(decoder, namespaceAndRolebindingTmpl)
		require.NoError(t, err)
		objs, err := p.Process(tmpl, values)
		require.NoError(t, err)

		// when
		err = p.Apply(objs)

		// then
		require.NoError(t, err)
		assertNamespaceExists(t, cl, user)
		assertRoleBindingExists(t, cl, user)

	})

	t.Run("should update existing role binding", func(t *testing.T) {
		// given
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)
		tmpl, err := decodeTemplate(decoder, rolebindingTmpl)
		require.NoError(t, err)
		objs, err := p.Process(tmpl, values)
		require.NoError(t, err)
		err = p.Apply(objs)
		require.NoError(t, err)
		assertRoleBindingExists(t, cl, user)

		// when rolebinding changes
		tmpl, err = decodeTemplate(decoder, namespaceAndRolebindingWithExtraUserTmpl)
		require.NoError(t, err)
		objs, err = p.Process(tmpl, values)
		require.NoError(t, err)
		err = p.Apply(objs)

		// then
		require.NoError(t, err)
		binding := assertRoleBindingExists(t, cl, user)
		require.Len(t, binding.Subjects, 2)
		assert.Equal(t, "User", binding.Subjects[0].Kind)
		assert.Equal(t, user, binding.Subjects[0].Name)
		assert.Equal(t, "User", binding.Subjects[1].Kind)
		assert.Equal(t, "extraUser", binding.Subjects[1].Name)
	})

	t.Run("failures", func(t *testing.T) {

		t.Run("should fail to create template object", func(t *testing.T) {
			// given
			cl := test.NewFakeClient(t)
			p := template.NewProcessor(cl, s)
			cl.MockCreate = func(ctx context.Context, obj runtime.Object) error {
				return errors.New("failed to create resource")
			}
			tmpl, err := decodeTemplate(decoder, rolebindingTmpl)
			require.NoError(t, err)

			// when
			objs, err := p.Process(tmpl, values)
			require.NoError(t, err)
			err = p.Apply(objs)

			// then
			require.Error(t, err)
		})

		t.Run("should fail to update template object", func(t *testing.T) {
			// given
			cl := test.NewFakeClient(t)
			p := template.NewProcessor(cl, s)
			cl.MockUpdate = func(ctx context.Context, obj runtime.Object) error {
				return errors.New("failed to update resource")
			}
			tmpl, err := decodeTemplate(decoder, rolebindingTmpl)
			require.NoError(t, err)
			objs, err := p.Process(tmpl, values)
			require.NoError(t, err)
			err = p.Apply(objs)
			require.NoError(t, err)

			// when
			tmpl, err = decodeTemplate(decoder, rolebindingTmpl)
			require.NoError(t, err)
			objs, err = p.Process(tmpl, values)
			require.NoError(t, err)
			err = p.Apply(objs)

			// then
			assert.Error(t, err)
		})
	})

	t.Run("should create with extra labels and ownerref", func(t *testing.T) {

		// given
		values := map[string]string{
			"USERNAME": user,
			"COMMIT":   commit,
		}
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)
		tmpl, err := decodeTemplate(decoder, namespaceAndRolebindingTmpl)
		require.NoError(t, err)
		objs, err := p.Process(tmpl, values)
		require.NoError(t, err)

		// when adding labels and an owner reference
		obj := objs[0]
		nsObj, ok := obj.Object.(*unstructured.Unstructured)
		require.True(t, ok)
		nsObj.SetOwnerReferences([]metav1.OwnerReference{
			{
				APIVersion: "crt/v1",
				Kind:       "NSTemplateSet",
				Name:       "foo",
			},
		})
		nsObj.SetLabels(map[string]string{
			"provider": "codeready-toolchain",
			"version":  commit,
			"extra":    "foo",
		})
		err = p.Apply(objs)

		// then
		require.NoError(t, err)
		ns := assertNamespaceExists(t, cl, user)
		// verify labels
		assert.Equal(t, map[string]string{
			"provider": "codeready-toolchain",
			"version":  commit,
			"extra":    "foo",
		}, ns.Labels)
		// verify owner refs
		assert.Equal(t, []metav1.OwnerReference{
			{
				APIVersion: "crt/v1",
				Kind:       "NSTemplateSet",
				Name:       "foo",
			},
		}, ns.OwnerReferences)
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
	expected, err := newObject(expectedObj.template, expectedObj.username, expectedObj.commit)
	require.NoError(t, err, "failed to create object from template")
	assert.Equal(t, expected, actual.Object)
}

type expectedObj struct {
	template string
	username string
	commit   string
}

func newObject(template, username, commit string) (*unstructured.Unstructured, error) {
	tmpl := texttemplate.New("")
	tmpl, err := tmpl.Parse(template)
	if err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(nil)
	err = tmpl.Execute(buf, struct {
		Username string
		Commit   string
	}{
		Username: username,
		Commit:   commit,
	})
	if err != nil {
		return nil, err
	}
	result := &unstructured.Unstructured{}
	err = result.UnmarshalJSON(buf.Bytes())
	return result, err
}

func assertNamespaceExists(t *testing.T, c client.Client, nsName string) corev1.Namespace {
	// check that the namespace was created
	var ns corev1.Namespace
	err := c.Get(context.TODO(), types.NamespacedName{Name: nsName, Namespace: ""}, &ns) // assert namespace is cluster-scoped
	require.NoError(t, err)
	return ns
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
	return rb
}

type decodeFunc func([]byte, *schema.GroupVersionKind, runtime.Object) (runtime.Object, *schema.GroupVersionKind, error)

func decodeTemplate(decoder runtime.Decoder, tmplContent string) (*templatev1.Template, error) {
	obj, _, err := decoder.Decode([]byte(tmplContent), nil, nil)
	if err != nil {
		return nil, errs.Wrapf(err, "unable to decode template")
	}
	tmpl, ok := obj.(*templatev1.Template)
	if !ok {
		return nil, fmt.Errorf("unable to convert object type %T to Template, must be a v1.Template", obj)
	}
	return tmpl, nil
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
      openshift.io/description: ${USERNAME}-user
      openshift.io/display-name: ${USERNAME}
      openshift.io/requester: ${USERNAME}
    labels:
      provider: codeready-toolchain
      version: ${COMMIT}
    name: ${USERNAME}
parameters:
- name: USERNAME
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
    name: ${USERNAME}-edit
    namespace: ${USERNAME}
  roleRef:
    kind: ClusterRole
    name: edit
  subjects:
  - kind: User
    name: ${USERNAME}
parameters:
- name: USERNAME
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
      openshift.io/description: ${USERNAME}-user
      openshift.io/display-name: ${USERNAME}
      openshift.io/requester: ${USERNAME}
    labels:
      provider: codeready-toolchain
      version: ${COMMIT}
    name: ${USERNAME}
- apiVersion: authorization.openshift.io/v1
  kind: RoleBinding
  metadata:
    name: ${USERNAME}-edit
    namespace: ${USERNAME}
  roleRef:
    kind: ClusterRole
    name: edit
  subjects:
  - kind: User
    name: ${USERNAME}
parameters:
- name: USERNAME
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
      openshift.io/description: ${USERNAME}-user
      openshift.io/display-name: ${USERNAME}
      openshift.io/requester: ${USERNAME}
    labels:
      provider: codeready-toolchain
      version: ${COMMIT}
    name: ${USERNAME}
- apiVersion: authorization.openshift.io/v1
  kind: RoleBinding
  metadata:
    name: ${USERNAME}-edit
    namespace: ${USERNAME}
  roleRef:
    kind: ClusterRole
    name: edit
  subjects:
  - kind: User
    name: ${USERNAME}
  - kind: User
    name: extraUser
parameters:
- name: USERNAME
  value: toolchain-dev
  required: true
- name: COMMIT
  value: 123abc
  required: true`

	namespaceObj = `{ 
	"apiVersion": "v1",
	"kind": "Namespace",
	"metadata": {
		"annotations": {
			"openshift.io/description": "{{ .Username }}-user",
			"openshift.io/display-name": "{{ .Username }}",
			"openshift.io/requester": "{{ .Username }}"
		},
		"labels": {
			"provider": "codeready-toolchain",
			"version": "{{ .Commit }}"
		},
		"name": "{{ .Username }}"
	}
}`

	rolebindingObj = `{
	"apiVersion": "authorization.openshift.io/v1",
	"kind": "RoleBinding",
	"metadata": {
		"name": "{{ .Username }}-edit",
    	"namespace": "{{ .Username }}"
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
)
