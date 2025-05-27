package host

import (
	"context"
	"fmt"
	"sync"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	runtimecluster "sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CachedHostClientInit takes care of initializing a cached host cluster client
type CachedHostClientInit struct {
	getLock                 sync.Mutex
	initLock                sync.RWMutex
	scheme                  *runtime.Scheme
	cachedHostClusterClient *NamespacedClient
	getHostCluster          cluster.GetHostClusterFunc
	initCachedClient        func(ctx context.Context, scheme *runtime.Scheme, cachedHostCluster *cluster.CachedToolchainCluster, hostNamespace string) (client.Client, error)
}

func NewCachedHostClientInitializer(scheme *runtime.Scheme, getHostCluster cluster.GetHostClusterFunc) *CachedHostClientInit {
	return &CachedHostClientInit{
		scheme:           scheme,
		initCachedClient: initCachedClient,
		getHostCluster:   getHostCluster,
	}
}

func NewNamespacedClient(client client.Client, namespace string) *NamespacedClient {
	return &NamespacedClient{Client: client, Namespace: namespace}
}

// NamespacedClient holds the client and the operator namespace
type NamespacedClient struct {
	client.Client
	Namespace string
}

type ClientGetter func(ctx context.Context) (*NamespacedClient, error)

// GetHostClient returns NamespacedClient backed by cached client for host operator namespace.
func (c *CachedHostClientInit) GetHostClient(ctx context.Context) (*NamespacedClient, error) {
	c.getLock.Lock()
	defer c.getLock.Unlock()
	if c.cachedHostClusterClient == nil {
		return c.init(ctx)
	}
	return c.cachedHostClusterClient, nil
}

func (c *CachedHostClientInit) init(ctx context.Context) (*NamespacedClient, error) {
	c.initLock.Lock()
	defer c.initLock.Unlock()
	logger := log.FromContext(ctx)
	logger.Info("Initializing cached host client")
	cachedHostCluster, found := c.getHostCluster()
	if !found {
		return nil, fmt.Errorf("host cluster not found")
	}
	hostNamespace := cachedHostCluster.OperatorNamespace
	cachedClient, err := c.initCachedClient(ctx, c.scheme, cachedHostCluster, hostNamespace)
	if err != nil {
		return nil, err
	}
	// populate the cache backed by shared informers that are initialized lazily on the first call
	// for the given GVK with all resources we are interested in from the host-operator namespace
	objectsToList := map[string]client.ObjectList{
		"TierTemplate":         &toolchainv1alpha1.TierTemplateList{},
		"TierTemplateRevision": &toolchainv1alpha1.TierTemplateRevisionList{},
	}

	for resourceName := range objectsToList {
		logger.Info("Syncing informer cache with resources", "resourceName", resourceName)
		if err := cachedClient.List(ctx, objectsToList[resourceName], client.InNamespace(hostNamespace)); err != nil {
			return nil, fmt.Errorf("informer cache sync failed for resource %s: %w", resourceName, err)
		}
	}

	logger.Info("Host cluster client initialized")
	c.cachedHostClusterClient = NewNamespacedClient(cachedClient, hostNamespace)
	return c.cachedHostClusterClient, nil
}

func initCachedClient(ctx context.Context, scheme *runtime.Scheme, cachedHostCluster *cluster.CachedToolchainCluster, hostNamespace string) (client.Client, error) {

	hostCluster, err := runtimecluster.New(cachedHostCluster.RestConfig, func(options *runtimecluster.Options) {
		options.Scheme = scheme
		// cache only in the host-operator namespace
		options.Cache.DefaultNamespaces = map[string]cache.Config{hostNamespace: {}}
	})
	if err != nil {
		return nil, err
	}
	go func() {
		if err := hostCluster.Start(ctx); err != nil {
			panic(fmt.Errorf("failed to create cached client: %w", err))
		}
	}()

	if !hostCluster.GetCache().WaitForCacheSync(ctx) {
		return nil, fmt.Errorf("unable to sync the cache of the client")
	}
	return hostCluster.GetClient(), nil
}
