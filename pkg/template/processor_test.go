package template_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/member-operator/pkg/template"

	"github.com/go-logr/zapr"
	apitemplate "github.com/openshift/api/template/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"fmt"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"context"
	authv1 "github.com/openshift/api/authorization/v1"
	projectv1 "github.com/openshift/api/project/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"github.com/satori/go.uuid"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

func TestProcess(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		// given
		s := addToScheme(t)
		project, commit, user := templateVars()

		values := map[string]string{
			"PROJECT_NAME": project,
			"COMMIT":       commit,
			"USER_NAME":    user,
		}

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s, logger(t))

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

	t.Run("overwrite default value of commit", func(t *testing.T) {
		// given
		s := addToScheme(t)
		project, commit, _ := templateVars()

		userDefault := "toolchain-dev"
		values := map[string]string{
			"PROJECT_NAME": project,
			"COMMIT":       commit,
		}

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s, logger(t))

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

	t.Run("random extra param - fail", func(t *testing.T) {
		// given
		s := addToScheme(t)
		project, commit, user := templateVars()
		values := map[string]string{
			"PROJECT_NAME": project,
			"COMMIT":       commit,
			"USER_NAME":    user,
		}

		// adding random param
		random := uuid.NewV4().String()
		values[random] = random

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s, logger(t))

		// when
		objs, err := p.Process(templateContent(projectRequestObj), values)

		// then
		require.EqualError(t, err, fmt.Sprintf("unknown parameter name \"%s\"", random))
		require.Len(t, objs, 0)
	})

	t.Run("template with default", func(t *testing.T) {
		// given
		s := addToScheme(t)
		// default values
		commit, user := "123abc", "toolchain-dev"

		project := uuid.NewV4().String()
		values := make(map[string]string)
		values["PROJECT_NAME"] = project

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s, logger(t))

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

	t.Run("template with missing required params", func(t *testing.T) {
		// given
		s := addToScheme(t)

		values := make(map[string]string)

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s, logger(t))

		// when
		objs, err := p.Process(templateContent(projectRequestObj, roleBindingObj), values)

		// then
		require.Error(t, err, "fail to process as not providing required param PROJECT_NAME")
		assert.Nil(t, objs)
	})


}

func TestProcessAndApply(t *testing.T) {
	t.Run("project Request - ok", func(t *testing.T) {
		//given
		s := addToScheme(t)
		project, commit, user := templateVars()

		values := map[string]string{
			"PROJECT_NAME": project,
			"COMMIT":       commit,
			"USER_NAME":    user,
		}

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s, logger(t))

		//when
		err := p.ProcessAndApply(templateContent(projectRequestObj), values)

		//then
		require.NoError(t, err)
		verifyProjectRequest(t, cl, project)
	})

	t.Run("role binding - ok", func(t *testing.T) {
		//given
		s := addToScheme(t)
		project, commit, user := templateVars()

		values := map[string]string{
			"PROJECT_NAME": project,
			"COMMIT":       commit,
			"USER_NAME":    user,
		}

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s, logger(t))

		//when
		err := p.ProcessAndApply(templateContent(roleBindingObj), values)

		//then
		require.NoError(t, err)
		verifyRoleBinding(t, cl, "foo")
	})

	t.Run("project request role binding - ok", func(t *testing.T) {
		//given
		s := addToScheme(t)
		project, commit, user := templateVars()

		values := map[string]string{
			"PROJECT_NAME": project,
			"COMMIT":       commit,
			"USER_NAME":    user,
		}

		cl := test.NewFakeClient(t)
		p := template.NewProcessor(cl, s, logger(t))

		//when
		err := p.ProcessAndApply(templateContent(projectRequestObj, roleBindingObj), values)

		//then
		require.NoError(t, err)
		verifyProjectRequest(t, cl, project)
		verifyRoleBinding(t, cl, project)
	})
}

func addToScheme(t *testing.T) *runtime.Scheme {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	utilruntime.Must(apitemplate.Install(s)) // see https://github.com/openshift/oc/blob/master/cmd/oc/oc.go#L77
	return s
}

func templateContent(objs ... string) []byte {
	tmpl := templateMetadata
	for _, obj := range objs {
		tmpl += obj
	}

	return []byte(tmpl + templateParams)
}

func logger(t *testing.T) logr.Logger {
	zapLog, err := zap.NewDevelopment()
	require.NoError(t, err)
	return zapr.NewLogger(zapLog)
}

func templateVars() (string, string, string) {
	return uuid.NewV4().String(), uuid.NewV4().String(), uuid.NewV4().String()
}

func verifyResource(t *testing.T, objKind schema.ObjectKind, vars ... string) {
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
