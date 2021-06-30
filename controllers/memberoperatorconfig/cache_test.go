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
	"k8s.io/apimachinery/pkg/runtime"
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
		cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
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
		cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
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
		cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
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
		cl.MockList = func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
			return fmt.Errorf("list error")
		}

		// when
		err := loadLatest(cl, MemberOperatorNs)

		// then
		require.EqualError(t, err, "list error")
	})
}

func TestLoadSecrets(t *testing.T) {
	secretData := map[string][]byte{
		"che-admin-username": []byte("cheadmin"),
		"che-admin-password": []byte("password"),
	}
	secretData2 := map[string][]byte{
		"che-admin-username2": []byte("cheadmin2"),
		"che-admin-password2": []byte("password2"),
	}

	t.Run("one secret found", func(t *testing.T) {
		// given
		secret := newSecret("che-secret", secretData)
		cl := NewFakeClient(t, secret)

		// when
		secrets, err := loadSecrets(cl, MemberOperatorNs)

		// then
		expected := map[string]string{
			"che-admin-username": "cheadmin",
			"che-admin-password": "password",
		}
		require.NoError(t, err)
		require.Equal(t, expected, secrets["che-secret"])
	})

	t.Run("two secrets found", func(t *testing.T) {
		// given
		secret := newSecret("che-secret", secretData)
		secret2 := newSecret("che-secret2", secretData2)
		cl := NewFakeClient(t, secret, secret2)

		// when
		secrets, err := loadSecrets(cl, MemberOperatorNs)

		// then
		expected := map[string]string{
			"che-admin-username": "cheadmin",
			"che-admin-password": "password",
		}
		expected2 := map[string]string{
			"che-admin-username2": "cheadmin2",
			"che-admin-password2": "password2",
		}
		require.NoError(t, err)
		require.Equal(t, expected, secrets["che-secret"])
		require.Equal(t, expected2, secrets["che-secret2"])
	})

	t.Run("secrets from another namespace not listed", func(t *testing.T) {
		// given
		secret := newSecret("che-secret", secretData)
		secret.Namespace = "default"
		secret2 := newSecret("che-secret2", secretData2)
		secret2.Namespace = "default"
		cl := NewFakeClient(t, secret, secret2)

		// when
		secrets, err := loadSecrets(cl, MemberOperatorNs)

		// then
		require.NoError(t, err)
		require.Empty(t, secrets)
	})

	t.Run("service account secrets are not listed", func(t *testing.T) {
		// given
		secret := newSecret("che-secret", secretData)
		secret.Annotations = map[string]string{
			"kubernetes.io/service-account.name": "default-something",
		}
		secret2 := newSecret("che-secret2", secretData2)
		secret2.Annotations = map[string]string{
			"kubernetes.io/service-account.name": "builder-something",
		}
		secret3 := newSecret("che-secret3", secretData2)
		secret3.Annotations = map[string]string{
			"kubernetes.io/service-account.name": "deployer-something",
		}
		cl := NewFakeClient(t, secret, secret2, secret3)

		// when
		secrets, err := loadSecrets(cl, MemberOperatorNs)

		// then
		require.NoError(t, err)
		require.Empty(t, secrets)
	})

	t.Run("no secrets found", func(t *testing.T) {
		// given
		cl := NewFakeClient(t)

		// when
		secrets, err := loadSecrets(cl, MemberOperatorNs)

		// then
		require.NoError(t, err)
		require.Empty(t, secrets)
	})

	t.Run("list secrets error", func(t *testing.T) {
		// given
		cl := NewFakeClient(t)
		cl.MockList = func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
			return fmt.Errorf("list error")
		}

		// when
		secrets, err := loadSecrets(cl, MemberOperatorNs)

		// then
		require.EqualError(t, err, "list error")
		require.Empty(t, secrets)
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
