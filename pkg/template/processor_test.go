package template_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/member-operator/pkg/template"

	"context"
	"fmt"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	authv1 "github.com/openshift/api/authorization/v1"
	projectv1 "github.com/openshift/api/project/v1"
	"github.com/satori/go.uuid"
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

var projectRequestObj = `
- apiVersion: project.openshift.io/v1
  kind: ProjectRequest
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
		project, commit, user := templateVars()
		values := paramsKeyValues(project, commit, user)

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(projectRequestObj, roleBindingObj), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)

		// project request
		verifyResource(t, objs[0].Object.GetObjectKind(), project, commit, user)

		// role binding
		verifyResource(t, objs[1].Object.GetObjectKind(), project, user)
	})

	t.Run("should overwrite default value of commit parameter", func(t *testing.T) {
		// given
		s := addToScheme(t)
		project, commit, _ := templateVars()

		userDefault := "toolchain-dev"
		values := map[string]string{
			"PROJECT_NAME": project,
			"COMMIT":       commit,
		}

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(projectRequestObj, roleBindingObj), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)

		// project request
		verifyResource(t, objs[0].Object.GetObjectKind(), project, commit, userDefault)

		// role binding
		verifyResource(t, objs[1].Object.GetObjectKind(), project, userDefault)
	})

	t.Run("should not fail for random extra param", func(t *testing.T) {
		// given
		s := addToScheme(t)
		project, commit, user := templateVars()
		values := paramsKeyValues(project, commit, user)

		// adding random param
		random := getNameFromTime("random")
		values[random] = random

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(projectRequestObj), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 1)
		// project request
		verifyResource(t, objs[0].Object.GetObjectKind(), project, commit, user)
	})

	t.Run("should process template with default parameters", func(t *testing.T) {
		// given
		s := addToScheme(t)
		// default values
		commit, user := "123abc", "toolchain-dev"

		project := uuid.NewV4().String()
		values := make(map[string]string)
		values["PROJECT_NAME"] = project

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(projectRequestObj, roleBindingObj), values)

		// then
		require.NoError(t, err)
		require.Len(t, objs, 2)

		// project request
		verifyResource(t, objs[0].Object.GetObjectKind(), project, commit, user)

		// role binding
		verifyResource(t, objs[1].Object.GetObjectKind(), project, user)
	})

	t.Run("should process template with missing required params", func(t *testing.T) {
		// given
		s := addToScheme(t)
		values := make(map[string]string)

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		objs, err := p.Process(templateContent(projectRequestObj, roleBindingObj), values)

		// then
		require.Error(t, err, "fail to process as not providing required param PROJECT_NAME")
		assert.Nil(t, objs)
	})

	t.Run("should fail to process template for invalid template content", func(t *testing.T) {
		// given
		s := addToScheme(t)
		project, commit, user := templateVars()
		values := paramsKeyValues(project, commit, user)

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		// when
		_, err := p.Process([]byte(projectRequestObj), values)

		// then
		assert.Error(t, err)
	})
}

func TestProcessAndApply(t *testing.T) {
	t.Run("should create project Request", func(t *testing.T) {
		//given
		s := addToScheme(t)
		project, commit, user := templateVars()
		values := paramsKeyValues(project, commit, user)

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		//when
		err := p.ProcessAndApply(templateContent(projectRequestObj), values)

		//then
		require.NoError(t, err)
		verifyProjectRequest(t, cl, project)
	})

	t.Run("should create role binding", func(t *testing.T) {
		//given
		s := addToScheme(t)
		project, commit, user := templateVars()
		values := paramsKeyValues(project, commit, user)

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		//when
		err := p.ProcessAndApply(templateContent(roleBindingObj), values)

		//then
		require.NoError(t, err)
		verifyRoleBinding(t, cl, project)
	})

	t.Run("should create project request role binding", func(t *testing.T) {
		//given
		s := addToScheme(t)
		project, commit, user := templateVars()
		values := paramsKeyValues(project, commit, user)
		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		//when
		err := p.ProcessAndApply(templateContent(projectRequestObj, roleBindingObj), values)

		//then
		require.NoError(t, err)
		verifyProjectRequest(t, cl, project)
		verifyRoleBinding(t, cl, project)
	})

	t.Run("should update existing role binding", func(t *testing.T) {
		//given
		s := addToScheme(t)
		project, commit, user := templateVars()
		values := paramsKeyValues(project, commit, user)

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s)

		err := p.ProcessAndApply(templateContent(roleBindingObj), values)

		require.NoError(t, err)
		verifyRoleBinding(t, cl, project)

		//when
		err = p.ProcessAndApply(templateContent(roleBindingObj+newUser), values)

		//then
		require.NoError(t, err)
		binding, err := roleBinding(cl, project)
		require.NoError(t, err)

		require.Len(t, binding.Subjects, 2)
		verifyRoleBinding(t, cl, project)
	})

	t.Run("should fail to create template object", func(t *testing.T) {
		//given
		cl := test.NewFakeClient(t)
		cl.MockCreate = func(ctx context.Context, obj runtime.Object) error {
			return errors.New("failed to create resource")
		}

		s := addToScheme(t)
		project, commit, user := templateVars()

		values := paramsKeyValues(project, commit, user)

		p := template.NewProcessor(cl, s)

		//when
		err := p.ProcessAndApply(templateContent(roleBindingObj), values)
		//then
		require.Error(t, err)
	})

	t.Run("should fail to update template object", func(t *testing.T) {
		//given
		cl := test.NewFakeClient(t)
		cl.MockUpdate = func(ctx context.Context, obj runtime.Object) error {
			return errors.New("failed to update resource")
		}

		s := addToScheme(t)
		project, commit, user := templateVars()

		values := paramsKeyValues(project, commit, user)

		p := template.NewProcessor(cl, s)
		err := p.ProcessAndApply(templateContent(roleBindingObj), values)
		require.NoError(t, err)

		//when
		err = p.ProcessAndApply(templateContent(roleBindingObj), values)
		//then
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

func paramsKeyValues(project, commit, user string) map[string]string {
	return map[string]string{
		"PROJECT_NAME": project,
		"COMMIT":       commit,
		"USER_NAME":    user,
	}
}

func templateVars() (string, string, string) {
	return getNameFromTime("project"), getNameFromTime("sha"), getNameFromTime("user")
}

func getNameFromTime(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func verifyResource(t *testing.T, objKind schema.ObjectKind, vars ...string) {
	require.IsType(t, &unstructured.Unstructured{}, objKind)
	projectRequest := objKind.(*unstructured.Unstructured)
	prJson, err := projectRequest.MarshalJSON()
	require.NoError(t, err, "failed to marshal json for projectrequest")
	for _, v := range vars {
		assert.Contains(t, string(prJson), v, fmt.Sprintf("missing %s", v))
	}
}

func verifyProjectRequest(t *testing.T, c client.Client, projectRequestName string) {
	// check that the project request was created
	pr, err := projectRequest(c, projectRequestName)

	require.NoError(t, err)
	assert.NotNil(t, pr)
}

func verifyRoleBinding(t *testing.T, c client.Client, ns string) {
	// check that the rolebinding is created in the namespace
	// (the fake client just records the request but does not perform any consistency check)
	rb, err := roleBinding(c, ns)

	require.NoError(t, err)
	assert.NotNil(t, rb)
}

func projectRequest(c client.Client, projectRequestName string) (*projectv1.ProjectRequest, error) {
	var pr projectv1.ProjectRequest
	err := c.Get(context.TODO(), types.NamespacedName{Name: projectRequestName, Namespace: ""}, &pr) // project request is cluster-scoped

	return &pr, err
}

func roleBinding(c client.Client, ns string) (*authv1.RoleBinding, error) {
	var rb authv1.RoleBinding
	err := c.Get(context.TODO(), types.NamespacedName{
		Namespace: ns,
		Name:      fmt.Sprintf("%s-edit", ns),
	}, &rb)

	return &rb, err
}
