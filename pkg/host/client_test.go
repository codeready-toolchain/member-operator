package host

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	testcommon "github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNewCachedHostClientInitializer(t *testing.T) {
	// given
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	t.Run("success", func(t *testing.T) {
		initializer := NewCachedHostClientInitializer(s, NewGetHostCluster(true))
		initializer.initCachedClient = func(_ context.Context, _ *runtime.Scheme, _ *cluster.CachedToolchainCluster, _ string) (client.Client, error) {
			return testcommon.NewFakeClient(t), nil
		}

		// when
		hostClient, err := initializer.GetHostClient(context.TODO())

		// then
		require.NoError(t, err)
		require.NotNil(t, hostClient)
		require.NotNil(t, hostClient.Client)
		require.Equal(t, testcommon.HostOperatorNs, hostClient.Namespace)
	})

	t.Run("failure", func(t *testing.T) {
		t.Run("host cluster not found", func(t *testing.T) {
			initializer := NewCachedHostClientInitializer(s, NewGetHostCluster(false))

			// when
			hostClient, err := initializer.GetHostClient(context.TODO())

			// then
			require.EqualError(t, err, "host cluster not found")
			require.Nil(t, hostClient)
		})

		t.Run("init client fails", func(t *testing.T) {
			initializer := NewCachedHostClientInitializer(s, NewGetHostCluster(true))
			initializer.initCachedClient = func(_ context.Context, _ *runtime.Scheme, _ *cluster.CachedToolchainCluster, _ string) (client.Client, error) {
				return nil, errors.New("some error")
			}

			// when
			hostClient, err := initializer.GetHostClient(context.TODO())

			// then
			require.EqualError(t, err, "some error")
			require.Nil(t, hostClient)
		})

		t.Run("list fails", func(t *testing.T) {
			initializer := NewCachedHostClientInitializer(s, NewGetHostCluster(true))
			cl := testcommon.NewFakeClient(t)
			cl.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				return errors.New("some list error")
			}
			initializer.initCachedClient = func(_ context.Context, _ *runtime.Scheme, _ *cluster.CachedToolchainCluster, _ string) (client.Client, error) {
				return cl, nil
			}

			// when
			hostClient, err := initializer.GetHostClient(context.TODO())

			// then
			require.ErrorContains(t, err, "informer cache sync failed for resource")
			require.ErrorContains(t, err, "some list error")
			require.Nil(t, hostClient)
		})
	})

	t.Run("cache", func(t *testing.T) {
		t.Run("once initialized, then it doesn't try again", func(t *testing.T) {
			// given
			initializer := NewCachedHostClientInitializer(s, NewGetHostCluster(false))
			cachedClient := NewNamespacedClient(testcommon.NewFakeClient(t), testcommon.HostOperatorNs)
			initializer.cachedHostClusterClient = cachedClient
			initializer.initCachedClient = func(_ context.Context, _ *runtime.Scheme, _ *cluster.CachedToolchainCluster, _ string) (client.Client, error) {
				return nil, errors.New("shouldn't be called")
			}

			// when
			hostClient, err := initializer.GetHostClient(context.TODO())

			// then
			require.NoError(t, err)
			assert.Same(t, cachedClient, hostClient)
		})

		t.Run("test multiple calls in parallel", func(t *testing.T) {
			// given
			var gate sync.WaitGroup
			gate.Add(1)
			var waitForFinished sync.WaitGroup
			initializer := NewCachedHostClientInitializer(s, NewGetHostCluster(true))
			initializer.initCachedClient = func(_ context.Context, _ *runtime.Scheme, _ *cluster.CachedToolchainCluster, _ string) (client.Client, error) {
				return testcommon.NewFakeClient(t), nil
			}

			for i := 0; i < 1000; i++ {
				waitForFinished.Add(1)
				go func() {
					// given
					defer waitForFinished.Done()
					gate.Wait()

					// when
					hostClient, err := initializer.GetHostClient(context.TODO())

					// then
					assert.NoError(t, err)
					assert.NotNil(t, hostClient)
					assert.NotNil(t, hostClient.Client)
					assert.Equal(t, testcommon.HostOperatorNs, hostClient.Namespace)
				}()
			}

			// when
			gate.Done()

			// then
			waitForFinished.Wait()
		})
	})
}

func NewGetHostCluster(ok bool) cluster.GetHostClusterFunc {
	if !ok {
		return func() (*cluster.CachedToolchainCluster, bool) {
			return nil, false
		}
	}

	return func() (toolchainCluster *cluster.CachedToolchainCluster, b bool) {
		return &cluster.CachedToolchainCluster{
			Config: &cluster.Config{
				OperatorNamespace: testcommon.HostOperatorNs,
			},
		}, true
	}
}
