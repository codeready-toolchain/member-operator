package test

import (
	"context"
	"fmt"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type RoleBindingAssertion struct {
	rolebinding    *rbacv1.RoleBinding
	client         runtimeclient.Client
	namespacedName types.NamespacedName
	t              test.T
}

func (a *RoleBindingAssertion) loadRoleBinding() error {
	rolebinding := &rbacv1.RoleBinding{}
	err := a.client.Get(context.TODO(), a.namespacedName, rolebinding)
	a.rolebinding = rolebinding
	return err
}

func AssertThatRoleBinding(t test.T, namespace, name string, client runtimeclient.Client) *RoleBindingAssertion {
	return &RoleBindingAssertion{
		client:         client,
		namespacedName: test.NamespacedName(namespace, name),
		t:              t,
	}
}

func (a *RoleBindingAssertion) Exists() *RoleBindingAssertion {
	err := a.loadRoleBinding()
	require.NoError(a.t, err)
	return a
}

func (a *RoleBindingAssertion) DoesNotExist() *RoleBindingAssertion {
	err := a.loadRoleBinding()
	require.EqualError(a.t, err, fmt.Sprintf(`rolebindings.rbac.authorization.k8s.io "%s" not found`, a.namespacedName.Name))
	return a
}

func (a *RoleBindingAssertion) HasLabel(key, value string) *RoleBindingAssertion {
	err := a.loadRoleBinding()
	require.NoError(a.t, err)
	require.Contains(a.t, a.rolebinding.Labels, key)
	assert.Equal(a.t, value, a.rolebinding.Labels[key])
	return a
}
