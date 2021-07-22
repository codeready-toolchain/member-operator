package memberoperatorconfig

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	testconfig "github.com/codeready-toolchain/toolchain-common/pkg/test/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcileWhenMemberOperatorConfigIsAvailable(t *testing.T) {
	// given
	config := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("10s"))
	cl := test.NewFakeClient(t, config)
	controller := Reconciler{
		Client: cl,
		Log:    ctrl.Log.WithName("controllers").WithName("MemberOperatorConfig"),
	}

	// when
	_, err := controller.Reconcile(context.TODO(), newRequest())

	// then
	require.NoError(t, err)
	actual, err := GetConfig(test.NewFakeClient(t))
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, actual.MemberStatus().RefreshPeriod())

	t.Run("update with new version", func(t *testing.T) {
		// given
		refreshPeriod := "8s"
		config.Spec.MemberStatus.RefreshPeriod = &refreshPeriod
		err := cl.Update(context.TODO(), config)
		require.NoError(t, err)

		// when
		_, err = controller.Reconcile(context.TODO(), newRequest())

		// then
		require.NoError(t, err)
		actual, err := GetConfig(test.NewFakeClient(t))
		require.NoError(t, err)
		assert.Equal(t, 8*time.Second, actual.MemberStatus().RefreshPeriod())
	})
}

func TestReconcileWhenGetConfigReturnsError(t *testing.T) {
	// given
	cl := test.NewFakeClient(t)
	cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
		return fmt.Errorf("get error")
	}
	controller := Reconciler{
		Client: cl,
		Log:    ctrl.Log.WithName("controllers").WithName("MemberOperatorConfig"),
	}

	// when
	_, err := controller.Reconcile(context.TODO(), newRequest())

	// then
	require.EqualError(t, err, "get error")
	actual, err := GetConfig(test.NewFakeClient(t))
	require.NoError(t, err)
	matchesDefaultConfig(t, actual)
}

func TestReconcileWhenListSecretsReturnsError(t *testing.T) {
	// given
	config := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("10s"))
	cl := test.NewFakeClient(t, config)
	cl.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
		return fmt.Errorf("list error")
	}
	controller := Reconciler{
		Client: cl,
		Log:    ctrl.Log.WithName("controllers").WithName("MemberOperatorConfig"),
	}

	// when
	_, err := controller.Reconcile(context.TODO(), newRequest())

	// then
	require.EqualError(t, err, "list error")
	actual, err := GetConfig(test.NewFakeClient(t))
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
	_, err := controller.Reconcile(context.TODO(), newRequest())

	// then
	require.NoError(t, err)
	actual, err := GetConfig(test.NewFakeClient(t))
	require.NoError(t, err)
	matchesDefaultConfig(t, actual)
}

func newRequest() reconcile.Request {
	return reconcile.Request{
		NamespacedName: test.NamespacedName(test.MemberOperatorNs, "config"),
	}
}

func matchesDefaultConfig(t *testing.T, actual Configuration) {
	assert.Equal(t, 5*time.Second, actual.MemberStatus().RefreshPeriod())
}
