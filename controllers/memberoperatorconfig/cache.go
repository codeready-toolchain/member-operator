package memberoperatorconfig

import (
	"context"
	"sync"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var configCache = &cache{}

var cacheLog = logf.Log.WithName("cache_memberoperatorconfig")

type cache struct {
	sync.RWMutex
	config *toolchainv1alpha1.MemberOperatorConfig
}

func (c *cache) set(config *toolchainv1alpha1.MemberOperatorConfig) {
	c.Lock()
	defer c.Unlock()
	c.config = config.DeepCopy()
}

func (c *cache) get() *toolchainv1alpha1.MemberOperatorConfig {
	c.RLock()
	defer c.RUnlock()
	return c.config.DeepCopy()
}

func updateConfig(config *toolchainv1alpha1.MemberOperatorConfig) {
	configCache.set(config)
}

func loadLatest(cl client.Client, namespace string) error {
	config := &toolchainv1alpha1.MemberOperatorConfig{}
	if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: "config"}, config); err != nil {
		if apierrors.IsNotFound(err) {
			cacheLog.Info("MemberOperatorConfig resource with the name 'config' wasn't found, default configuration will be used", "namespace", namespace)
			return nil
		}
		return err
	}
	configCache.set(config)
	return nil
}

// GetConfig returns a cached host-operator config.
// If no config is stored in the cache, then it retrieves it from the cluster and stores in the cache.
// If the resource is not found, then returns the default config.
// If any failure happens while getting the MemberOperatorConfig resource, then returns an error.
func GetConfig(cl client.Client, namespace string) (MemberOperatorConfig, error) {
	config := configCache.get()
	if config == nil {
		err := loadLatest(cl, namespace)
		if err != nil {
			return MemberOperatorConfig{cfg: &toolchainv1alpha1.MemberOperatorConfigSpec{}}, err
		}
		config = configCache.get()
	}
	if config == nil {
		return MemberOperatorConfig{cfg: &toolchainv1alpha1.MemberOperatorConfigSpec{}}, nil
	}
	return MemberOperatorConfig{cfg: &config.Spec}, nil
}

// Reset resets the cache.
// Should be used only in tests, but since it has to be used in other packages,
// then the function has to be exported and placed here.
func Reset() {
	configCache = &cache{}
}
