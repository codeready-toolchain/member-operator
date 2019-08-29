package template_test

import (
	"github.com/codeready-toolchain/member-operator/pkg/template"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sort"
	"testing"
)

var tmplObjs = `
- apiVersion: v1
  kind: Secret
  metadata:
    name: user-tmpl-test
- apiVersion: project.openshift.io/v1
  kind: ProjectRequest
  metadata:
    name: ${PROJECT_NAME}
- apiVersion: v1
  kind: ServiceAccount
  metadata:
    name: user-tmpl-test
- apiVersion: v1
  kind: RoleBinding
  metadata:
    name: user-tmpl-test
- apiVersion: v1
  kind: RoleBindingRestriction
  metadata:
    name: user-tmpl-test
- apiVersion: v1
  kind: ResourceQuota
  metadata:
    name: user-tmpl-test
- apiVersion: v1
  kind: LimitRange
  metadata:
    name: user-tmpl-test`

var sortedOrder = []string{template.ProjectRequest, template.RoleBindingRestriction, template.LimitRange, template.ResourceQuota, template.Secret, template.ServiceAccount, template.RoleBinding}

func TestSort(t *testing.T) {
	// given
	s := addToScheme(t)
	project, commit, user := templateVars()
	values := paramsKeyValues(project, commit, user)

	cl := test.NewFakeClient(t)
	p := template.NewProcessor(cl, s)

	objs, err := p.Process(templateContent(tmplObjs), values)
	require.NoError(t, err)
	// when

	// when
	sort.Sort(template.ByKind(objs))

	// then
	require.NoError(t, err)
	for i := range objs {
		assert.Equal(t, sortedOrder[i], template.Kind(objs[i]))
	}
}

func TestReverseSort(t *testing.T) {
	// given
	s := addToScheme(t)
	project, commit, user := templateVars()
	values := paramsKeyValues(project, commit, user)

	cl := test.NewFakeClient(t)
	p := template.NewProcessor(cl, s)

	objs, err := p.Process(templateContent(tmplObjs), values)
	require.NoError(t, err)

	// when
	sort.Sort(sort.Reverse(template.ByKind(objs)))

	// then
	require.NoError(t, err)
	for i := range objs {
		assert.Equal(t, sortedOrder[len(sortedOrder)-1-i], template.Kind(objs[i]))
	}
}
