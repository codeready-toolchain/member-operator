package memberoperatorconfig

import (
	"context"
	"sync"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	common "github.com/codeready-toolchain/toolchain-common/pkg/configuration"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var configCache = &cache{}

var cacheLog = logf.Log.WithName("cache_memberoperatorconfig")

type cache struct {
	sync.RWMutex
	config  *toolchainv1alpha1.MemberOperatorConfig
	secrets map[string]map[string]string // map of secret key-value pairs indexed by secret name
}

func (c *cache) set(config *toolchainv1alpha1.MemberOperatorConfig, secrets map[string]map[string]string) {
	c.Lock()
	defer c.Unlock()
	c.config = config.DeepCopy()
	c.secrets = common.CopyOf(secrets)
}

func (c *cache) get() (*toolchainv1alpha1.MemberOperatorConfig, map[string]map[string]string) {
	c.RLock()
	defer c.RUnlock()
	return c.config.DeepCopy(), common.CopyOf(c.secrets)
}

func updateConfig(config *toolchainv1alpha1.MemberOperatorConfig, secrets map[string]map[string]string) {
	configCache.set(config, secrets)
}

func loadLatest(cl client.Client, namespace string) (Configuration, error) {
	config := &toolchainv1alpha1.MemberOperatorConfig{}
	if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: "config"}, config); err != nil {
		if apierrors.IsNotFound(err) {
			cacheLog.Info("MemberOperatorConfig resource with the name 'config' wasn't found, default configuration will be used", "namespace", namespace)
			return Configuration{m: &toolchainv1alpha1.MemberOperatorConfigSpec{}}, nil
		}
		return Configuration{m: &toolchainv1alpha1.MemberOperatorConfigSpec{}}, err
	}

	allSecrets, err := common.LoadSecrets(cl, namespace)
	if err != nil {
		return Configuration{m: &toolchainv1alpha1.MemberOperatorConfigSpec{}}, err
	}

	configCache.set(config, allSecrets)
	return getConfigOrDefault(), nil
}

// GetConfig returns a cached memberoperator config.
// If no config is stored in the cache, then it retrieves it from the cluster and stores in the cache.
// If the resource is not found, then returns the default config.
// If any failure happens while getting the MemberOperatorConfig resource, then returns an error.
func GetConfig(cl client.Client, namespace string) (Configuration, error) {
	config, _ := configCache.get()
	if config == nil {
		return loadLatest(cl, namespace)
	}
	return getConfigOrDefault(), nil
}

func getConfigOrDefault() Configuration {
	config, secrets := configCache.get()
	if config == nil {
		return Configuration{m: &toolchainv1alpha1.MemberOperatorConfigSpec{}, secrets: secrets}
	}
	return Configuration{m: &config.Spec, secrets: secrets}
}

// Reset resets the cache.
// Should be used only in tests, but since it has to be used in other packages,
// then the function has to be exported and placed here.
func Reset() {
	configCache = &cache{}
}
