package test

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func AssertObject(t *testing.T, fakeClient *test.FakeClient, namespace, name string, actualResource client.Object, assertContent func()) {
	err := fakeClient.Get(context.TODO(), test.NamespacedName(namespace, name), actualResource)
	require.NoError(t, err)
	require.NotNil(t, assertContent)
	assertContent()
}

func AssertObjectNotFound(t *testing.T, fakeClient *test.FakeClient, namespace, name string, actualResource client.Object) {
	err := fakeClient.Get(context.TODO(), test.NamespacedName(namespace, name), actualResource)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}
