package memberoperatorconfig

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	. "github.com/codeready-toolchain/toolchain-common/pkg/test"
	testconfig "github.com/codeready-toolchain/toolchain-common/pkg/test/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCache(t *testing.T) {
	// given
	cl := NewFakeClient(t)

	// when
	defaultConfig, err := GetConfig(cl, MemberOperatorNs)

	// then
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, defaultConfig.MemberStatus().RefreshPeriod())

	t.Run("return config that is stored in cache", func(t *testing.T) {
		// given
		config := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("10s"))
		cl := NewFakeClient(t, config)

		// when
		actual, err := GetConfig(cl, MemberOperatorNs)

		// then
		require.NoError(t, err)
		assert.Equal(t, 10*time.Second, actual.MemberStatus().RefreshPeriod()) // regular value
		assert.Equal(t, "", actual.Che().AdminUserName())                      // secret value

		t.Run("returns the same when the cache hasn't been updated", func(t *testing.T) {
			// when
			actual, err := GetConfig(cl, MemberOperatorNs)

			// then
			require.NoError(t, err)
			assert.Equal(t, 10*time.Second, actual.MemberStatus().RefreshPeriod()) // regular value
			assert.Equal(t, "", actual.Che().AdminUserName())                      // secret value
		})

		t.Run("returns the new config when the cache was updated", func(t *testing.T) {
			// given
			newConfig := NewMemberOperatorConfigWithReset(t,
				testconfig.MemberStatus().RefreshPeriod("11s"),
				testconfig.Che().Secret().
					Ref("che-secret").
					CheAdminUsernameKey("che-admin-username"))
			cl := NewFakeClient(t)
			secretData := map[string]map[string]string{
				"che-secret": {
					"che-admin-username": "cheadmin",
				},
			}
			// when
			updateConfig(newConfig, secretData)

			// then
			actual, err := GetConfig(cl, MemberOperatorNs)
			require.NoError(t, err)
			assert.Equal(t, 11*time.Second, actual.MemberStatus().RefreshPeriod())
			assert.Equal(t, "cheadmin", actual.Che().AdminUserName()) // secret value
		})
	})
}

func TestGetConfigFailed(t *testing.T) {
	// given
	t.Run("config not found", func(t *testing.T) {
		config := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("11s"))
		cl := NewFakeClient(t, config)
		cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
			return apierrors.NewNotFound(schema.GroupResource{}, "config")
		}

		// when
		defaultConfig, err := GetConfig(cl, MemberOperatorNs)

		// then
		require.NoError(t, err)
		assert.Equal(t, 5*time.Second, defaultConfig.MemberStatus().RefreshPeriod())

	})

	t.Run("error getting config", func(t *testing.T) {
		config := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("11s"))
		cl := NewFakeClient(t, config)
		cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
			return fmt.Errorf("some error")
		}

		// when
		defaultConfig, err := GetConfig(cl, MemberOperatorNs)

		// then
		require.Error(t, err)
		assert.Equal(t, 5*time.Second, defaultConfig.MemberStatus().RefreshPeriod())

	})
}

func TestLoadLatest(t *testing.T) {
	t.Run("config found", func(t *testing.T) {
		initconfig := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("1s"))
		// given
		cl := NewFakeClient(t, initconfig)

		// when
		err := loadLatest(cl, MemberOperatorNs)

		// then
		require.NoError(t, err)
		actual, err := GetConfig(cl, MemberOperatorNs)
		require.NoError(t, err)
		assert.Equal(t, 1*time.Second, actual.MemberStatus().RefreshPeriod())

		t.Run("returns the same when the config hasn't been updated", func(t *testing.T) {
			// when
			err := loadLatest(cl, MemberOperatorNs)

			// then
			require.NoError(t, err)
			actual, err = GetConfig(cl, MemberOperatorNs)
			require.NoError(t, err)
			assert.Equal(t, 1*time.Second, actual.MemberStatus().RefreshPeriod())
		})

		t.Run("returns the new value when the config has been updated", func(t *testing.T) {
			// get
			changedConfig := UpdateMemberOperatorConfigWithReset(t, cl, testconfig.MemberStatus().RefreshPeriod("20s"))
			err = cl.Update(context.TODO(), changedConfig)
			require.NoError(t, err)

			// when
			err = loadLatest(cl, MemberOperatorNs)

			// then
			require.NoError(t, err)
			actual, err = GetConfig(cl, MemberOperatorNs)
			require.NoError(t, err)
			assert.Equal(t, 20*time.Second, actual.MemberStatus().RefreshPeriod())
		})
	})

	t.Run("config not found", func(t *testing.T) {
		// given
		cl := NewFakeClient(t)

		// when
		err := loadLatest(cl, MemberOperatorNs)

		// then
		require.NoError(t, err)
	})

	t.Run("get config error", func(t *testing.T) {
		initconfig := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("1s"))
		// given
		cl := NewFakeClient(t, initconfig)
		cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
			return fmt.Errorf("get error")
		}

		// when
		err := loadLatest(cl, MemberOperatorNs)

		// then
		require.EqualError(t, err, "get error")
	})

	t.Run("load secrets error", func(t *testing.T) {
		initconfig := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("1s"))
		// given
		cl := NewFakeClient(t, initconfig)
		cl.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			return fmt.Errorf("list error")
		}

		// when
		err := loadLatest(cl, MemberOperatorNs)

		// then
		require.EqualError(t, err, "list error")
	})
}

func TestMultipleExecutionsInParallel(t *testing.T) {
	// given
	var latch sync.WaitGroup
	latch.Add(1)
	var waitForFinished sync.WaitGroup
	initconfig := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("1s"))
	cl := NewFakeClient(t, initconfig)

	for i := 1; i < 1001; i++ {
		waitForFinished.Add(2)
		go func() {
			defer waitForFinished.Done()
			latch.Wait()

			// when
			config, err := GetConfig(cl, MemberOperatorNs)

			// then
			require.NoError(t, err)
			assert.NotEmpty(t, config.MemberStatus().RefreshPeriod())
		}()
		go func(i int) {
			defer waitForFinished.Done()
			latch.Wait()
			config := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod(fmt.Sprintf("%ds", i)))
			updateConfig(config, map[string]map[string]string{})
		}(i)
	}

	// when
	latch.Done()
	waitForFinished.Wait()
	config, err := GetConfig(NewFakeClient(t), MemberOperatorNs)

	// then
	require.NoError(t, err)
	assert.NotEmpty(t, config.MemberStatus().RefreshPeriod())
}

func newSecret(name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: MemberOperatorNs,
		},
		Data: data,
	}
}
