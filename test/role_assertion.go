package test

import (
	"context"
	"fmt"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type RoleAssertion struct {
	role           *rbacv1.Role
	client         client.Client
	namespacedName types.NamespacedName
	t              test.T
}

func (a *RoleAssertion) loadRole() error {
	role := &rbacv1.Role{}
	err := a.client.Get(context.TODO(), a.namespacedName, role)
	a.role = role
	return err
}

func AssertThatRole(t test.T, namespace, name string, client client.Client) *RoleAssertion {
	return &RoleAssertion{
		client:         client,
		namespacedName: test.NamespacedName(namespace, name),
		t:              t,
	}
}

func (a *RoleAssertion) Exists() *RoleAssertion {
	err := a.loadRole()
	require.NoError(a.t, err)
	return a
}

func (a *RoleAssertion) DoesNotExist() *RoleAssertion {
	err := a.loadRole()
	require.EqualError(a.t, err, fmt.Sprintf(`roles.rbac.authorization.k8s.io "%s" not found`, a.namespacedName.Name))
	return a
}

func (a *RoleAssertion) HasLabel(key, value string) *RoleAssertion {
	err := a.loadRole()
	require.NoError(a.t, err)
	require.Contains(a.t, a.role.Labels, key)
	assert.Equal(a.t, value, a.role.Labels[key])
	return a
}
