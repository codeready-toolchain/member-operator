// Package configuration is in charge of the validation and extraction of all
// the configuration details from a configuration file or environment variables.
package configuration

import (
	"os"
	"strings"
	"time"

	"github.com/codeready-toolchain/toolchain-common/pkg/configuration"

	"github.com/spf13/viper"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// prefixes
const (
	// MemberEnvPrefix will be used for member environment variable name prefixing.
	MemberEnvPrefix = "MEMBER_OPERATOR"
)

// Configuration constants
const (
	// MemberStatusName specifies the name of the toolchain member status resource that provides information about the toolchain components in this cluster
	MemberStatusName = "member.status"

	// DefaultMemberStatusName the default name for the member status resource created during initialization of the operator
	DefaultMemberStatusName = "toolchain-member-status"
)

// Kubefed configuration constants
const (
	ClusterHealthCheckPeriod        = "cluster.healthcheck.period"
	DefaultClusterHealthCheckPeriod = "10s"

	ClusterHealthCheckTimeout        = "cluster.healthcheck.timeout"
	DefaultClusterHealthCheckTimeout = "3s"

	ClusterHealthCheckFailureThreshold        = "cluster.healthcheck.failure.threshold"
	DefaultClusterHealthCheckFailureThreshold = 3

	ClusterHealthCheckSuccessThreshold        = "cluster.healthcheck.success.threshold"
	DefaultClusterHealthCheckSuccessThreshold = 1

	ClusterAvailableDelay        = "cluster.available.delay"
	DefaultClusterAvailableDelay = "20s"

	ClusterUnavailableDelay        = "cluster.unavailable.delay"
	DefaultClusterUnavailableDelay = "60s"
)

const (
	identityProviderName        = "identity.provider"
	defaultIdentityProviderName = "rhd"
)

// Config encapsulates the Viper configuration registry which stores the
// configuration data in-memory.
type Config struct {
	member *viper.Viper
}

// initConfig creates an initial, empty registry.
func initConfig() *Config {
	c := Config{
		member: viper.New(),
	}
	c.member.SetEnvPrefix(MemberEnvPrefix)
	c.member.AutomaticEnv()
	c.member.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	c.setConfigDefaults()
	return &c
}

func LoadConfig(cl client.Client) (*Config, error) {
	err := configuration.LoadFromConfigMap(MemberEnvPrefix, "MEMBER_OPERATOR_CONFIG_MAP_NAME", cl)
	if err != nil {
		return nil, err
	}

	return initConfig(), nil
}

func (c *Config) setConfigDefaults() {
	c.member.SetTypeByDefaultValue(true)
	c.member.SetDefault(MemberStatusName, DefaultMemberStatusName)
	c.member.SetDefault(ClusterHealthCheckPeriod, DefaultClusterHealthCheckPeriod)
	c.member.SetDefault(ClusterHealthCheckTimeout, DefaultClusterHealthCheckTimeout)
	c.member.SetDefault(ClusterHealthCheckFailureThreshold, DefaultClusterHealthCheckFailureThreshold)
	c.member.SetDefault(ClusterHealthCheckSuccessThreshold, DefaultClusterHealthCheckSuccessThreshold)
	c.member.SetDefault(ClusterAvailableDelay, DefaultClusterAvailableDelay)
	c.member.SetDefault(ClusterUnavailableDelay, DefaultClusterUnavailableDelay)
	c.member.SetDefault(identityProviderName, defaultIdentityProviderName)
}

// GetAllMemberParameters returns the map with key-values pairs of parameters that have MEMBER_OPERATOR prefix
func (c *Config) GetAllMemberParameters() map[string]string {
	vars := map[string]string{}

	for _, env := range os.Environ() {
		keyValue := strings.SplitN(env, "=", 2)
		if len(keyValue) < 2 {
			continue
		}
		if strings.HasPrefix(keyValue[0], MemberEnvPrefix+"_") {
			vars[keyValue[0]] = keyValue[1]
		}
	}
	return vars
}

// GetIdP returns the configured Identity Provider (IdP) for the member operator
// Openshift clusters can be configured with multiple IdPs. This config option allows admins to specify which IdP should be used by the toolchain operator.
func (c *Config) GetIdP() string {
	return c.member.GetString(identityProviderName)
}

// GetMemberStatusName returns the configured name of the member status resource
func (c *Config) GetMemberStatusName() string {
	return c.member.GetString(MemberStatusName)
}

// GetClusterHealthCheckPeriod returns the configured cluster health check period
func (c *Config) GetClusterHealthCheckPeriod() time.Duration {
	return c.member.GetDuration(ClusterHealthCheckPeriod)
}

// GetClusterHealthCheckTimeout returns the configured cluster health check timeout
func (c *Config) GetClusterHealthCheckTimeout() time.Duration {
	return c.member.GetDuration(ClusterHealthCheckTimeout)
}

// GetClusterHealthCheckFailureThreshold returns the configured cluster health check failure threshold
func (c *Config) GetClusterHealthCheckFailureThreshold() int64 {
	return c.member.GetInt64(ClusterHealthCheckFailureThreshold)
}

// GetClusterHealthCheckSuccessThreshold returns the configured cluster health check failure threshold
func (c *Config) GetClusterHealthCheckSuccessThreshold() int64 {
	return c.member.GetInt64(ClusterHealthCheckSuccessThreshold)
}

// GetClusterAvailableDelay returns the configured cluster available delay
func (c *Config) GetClusterAvailableDelay() time.Duration {
	return c.member.GetDuration(ClusterAvailableDelay)
}

// GetClusterUnavailableDelay returns the configured cluster unavailable delay
func (c *Config) GetClusterUnavailableDelay() time.Duration {
	return c.member.GetDuration(ClusterUnavailableDelay)
}
