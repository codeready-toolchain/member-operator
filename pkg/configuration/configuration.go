// Package configuration is in charge of the validation and extraction of all
// the configuration details from a configuration file or environment variables.
package configuration

import (
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// prefixes
const (
	// MemberEnvPrefix will be used for member environment variable name prefixing.
	MemberEnvPrefix = "MEMBER_OPERATOR"
)

// Configuration constants
const (
	// IdentityProvider specifies an identity provider (IdP) for newly created users
	IdentityProvider = "identity.provider"

	// DefaultIdentityProvider the default value used for the identity provider (IdP) for newly created users
	DefaultIdentityProvider = "rhd"

	// MemberStatusName specifies the name of the toolchain member status resource that provides information about the toolchain components in this cluster
	MemberStatusName = "member.status"

	// DefaultMemberStatusName the default name for the member status resource created during initialization of the operator
	DefaultMemberStatusName = "toolchain-member-status"
)

// ToolchainCluster configuration constants
const (
	clusterHealthCheckPeriod        = "cluster.healthcheck.period"
	defaultClusterHealthCheckPeriod = "10s"

	toolchainClusterTimeout          = "cluster.healthcheck.timeout"
	defaultClusterHealthCheckTimeout = "3s"
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

func LoadConfig() *Config {
	return initConfig()
}

func (c *Config) setConfigDefaults() {
	c.member.SetTypeByDefaultValue(true)
	c.member.SetDefault(IdentityProvider, DefaultIdentityProvider)
	c.member.SetDefault(MemberStatusName, DefaultMemberStatusName)
	c.member.SetDefault(clusterHealthCheckPeriod, defaultClusterHealthCheckPeriod)
	c.member.SetDefault(toolchainClusterTimeout, defaultClusterHealthCheckTimeout)
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
	return c.member.GetString(IdentityProvider)
}

// GetMemberStatusName returns the configured name of the member status resource
func (c *Config) GetMemberStatusName() string {
	return c.member.GetString(MemberStatusName)
}

// GetClusterHealthCheckPeriod returns the configured cluster health check period
func (c *Config) GetClusterHealthCheckPeriod() time.Duration {
	return c.member.GetDuration(clusterHealthCheckPeriod)
}

// GetToolchainClusterTimeout returns the configured cluster health check timeout
func (c *Config) GetToolchainClusterTimeout() time.Duration {
	return c.member.GetDuration(toolchainClusterTimeout)
}
