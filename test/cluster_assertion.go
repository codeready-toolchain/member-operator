package test

import (
	"context"
	"encoding/json"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ClusterAssertion struct {
	client runtimeclient.Client
	t      test.T
}

func AssertThatCluster(t test.T, client runtimeclient.Client) *ClusterAssertion {
	return &ClusterAssertion{
		client: client,
		t:      t,
	}
}

func (a *ClusterAssertion) HasResource(name string, obj runtimeclient.Object, options ...ResourceOption) *ClusterAssertion {
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: name}, obj)
	require.NoError(a.t, err)
	for _, check := range options {
		check(a.t, obj)
	}
	return a
}

func (a *ClusterAssertion) HasNoResource(name string, obj runtimeclient.Object) *ClusterAssertion {
	err := a.client.Get(context.TODO(), types.NamespacedName{Name: name}, obj)
	require.Error(a.t, err, "did not expect resource '%s/%s' to exist", obj.GetObjectKind().GroupVersionKind().Kind, name)
	assert.True(a.t, errors.IsNotFound(err))
	return a
}

type ResourceOption func(t test.T, obj runtimeclient.Object)

func WithLabel(key, value string) ResourceOption {
	return func(t test.T, obj runtimeclient.Object) {
		v, exists := obj.GetLabels()[key]
		require.True(t, exists)
		assert.Equal(t, value, v)
	}
}

func Containing(value string) ResourceOption {
	return func(t test.T, obj runtimeclient.Object) {
		content, err := json.Marshal(obj)
		require.NoError(t, err)
		assert.Contains(t, string(content), value)
	}
}

func HasDeletionTimestamp() ResourceOption {
	return func(t test.T, obj runtimeclient.Object) {
		assert.NotNil(t, obj.GetDeletionTimestamp())
	}
}
