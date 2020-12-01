package test

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
)

func AssertMemberObject(t *testing.T, fakeClient *test.FakeClient, name string, actualResource runtime.Object, assertContent func()) {
	AssertObject(t, fakeClient, test.MemberOperatorNs, name, actualResource, assertContent)
}

func AssertObject(t *testing.T, fakeClient *test.FakeClient, namespace, name string, actualResource runtime.Object, assertContent func()) {
	err := fakeClient.Get(context.TODO(), test.NamespacedName(namespace, name), actualResource)
	if assertContent == nil {
		require.Error(t, err)
		require.True(t, errors.IsNotFound(err))
	} else {
		require.NoError(t, err)
		assertContent()
	}
}
