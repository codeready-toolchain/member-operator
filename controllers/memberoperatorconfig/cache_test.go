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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		assert.Equal(t, 10*time.Second, actual.MemberStatus().RefreshPeriod())

		t.Run("returns the same when the cache hasn't been updated", func(t *testing.T) {
			// given
			newConfig := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("10s"))
			cl := NewFakeClient(t, newConfig)

			// when
			actual, err := GetConfig(cl, MemberOperatorNs)

			// then
			require.NoError(t, err)
			assert.Equal(t, 10*time.Second, actual.MemberStatus().RefreshPeriod())
		})

		t.Run("returns the new config when the cache was updated", func(t *testing.T) {
			// given
			newConfig := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("11s"))
			cl := NewFakeClient(t)

			// when
			updateConfig(newConfig, map[string]map[string]string{})

			// then
			actual, err := GetConfig(cl, MemberOperatorNs)
			require.NoError(t, err)
			assert.Equal(t, 11*time.Second, actual.MemberStatus().RefreshPeriod())
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
