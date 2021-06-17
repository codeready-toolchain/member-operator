package memberoperatorconfig

import (
	"context"
	"fmt"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	testconfig "github.com/codeready-toolchain/toolchain-common/pkg/test/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcileWhenMemberOperatorConfigIsAvailable(t *testing.T) {
	// given
	config := newMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("10s"))
	cl := test.NewFakeClient(t, config)
	controller := Reconciler{
		Client: cl,
		Log:    ctrl.Log.WithName("controllers").WithName("MemberOperatorConfig"),
	}

	// when
	_, err := controller.Reconcile(newRequest())

	// then
	require.NoError(t, err)
	actual, err := GetConfig(test.NewFakeClient(t), test.MemberOperatorNs)
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, actual.MemberStatus().RefreshPeriod())

	t.Run("update with new version", func(t *testing.T) {
		// given
		refreshPeriod := "8s"
		config.Spec.MemberStatus.RefreshPeriod = &refreshPeriod
		err := cl.Update(context.TODO(), config)
		require.NoError(t, err)

		// when
		_, err = controller.Reconcile(newRequest())

		// then
		require.NoError(t, err)
		actual, err := GetConfig(test.NewFakeClient(t), test.MemberOperatorNs)
		require.NoError(t, err)
		assert.Equal(t, 8*time.Second, actual.MemberStatus().RefreshPeriod())
	})
}

func TestReconcileWhenReturnsError(t *testing.T) {
	// given
	cl := test.NewFakeClient(t)
	cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
		return fmt.Errorf("some error")
	}
	controller := Reconciler{
		Client: cl,
		Log:    ctrl.Log.WithName("controllers").WithName("MemberOperatorConfig"),
	}

	// when
	_, err := controller.Reconcile(newRequest())

	// then
	require.Error(t, err)
	actual, err := GetConfig(test.NewFakeClient(t), test.MemberOperatorNs)
	require.NoError(t, err)
	matchesDefaultConfig(t, actual)
}

func TestReconcileWhenMemberOperatorConfigIsNotPresent(t *testing.T) {
	// given
	controller := Reconciler{
		Client: test.NewFakeClient(t),
		Log:    ctrl.Log.WithName("controllers").WithName("MemberOperatorConfig"),
	}

	// when
	_, err := controller.Reconcile(newRequest())

	// then
	require.NoError(t, err)
	actual, err := GetConfig(test.NewFakeClient(t), test.MemberOperatorNs)
	require.NoError(t, err)
	matchesDefaultConfig(t, actual)
}

func newRequest() reconcile.Request {
	return reconcile.Request{
		NamespacedName: test.NamespacedName(test.MemberOperatorNs, "config"),
	}
}

func newMemberOperatorConfigWithReset(t *testing.T, options ...testconfig.MemberOperatorConfigOption) *toolchainv1alpha1.MemberOperatorConfig {
	t.Cleanup(Reset)
	return testconfig.NewMemberOperatorConfig(options...)
}

func matchesDefaultConfig(t *testing.T, actual MemberOperatorConfig) {
	assert.Equal(t, 5*time.Second, actual.MemberStatus().RefreshPeriod())
}
