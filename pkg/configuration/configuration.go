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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("configuration")

// prefixes
const (
	// MemberEnvPrefix will be used for member environment variable name prefixing.
	MemberEnvPrefix  = "MEMBER_OPERATOR"
	MemberStatusName = "toolchain-member-status"
)

// Configuration constants
const (
	// varMemberStatusRefreshTime specifies how often the MemberStatus should load and refresh the current member cluster status
	varMemberStatusRefreshTime = "memberstatus.refresh.time"

	// defaultMemberStatusRefreshTime is the default refresh period for MemberStatus
	defaultMemberStatusRefreshTime = "5s"

	// varConsoleNamespace is the console route namespace
	varConsoleNamespace = "console.namespace"

	// defaultConsoleNamespace is the default console route namespace
	defaultConsoleNamespace = "openshift-console"

	// varConsoleRouteName is the console route name
	varConsoleRouteName = "console.route.name"

	// defaultConsoleRouteName is the default console route name
	defaultConsoleRouteName = "console"

	// varCheRequired is set to true if Che/CRW operator is expected to be installed on the cluster. May be used in monitoring.
	varCheRequired = "che.required"

	// defaultCheRequired is the default value for che.required param
	defaultCheRequired = false

	// varCheUserDeletionEnabled is a way to turn the Che user deletion logic on/off
	varCheUserDeletionEnabled = "che.user_deletion_enabled"

	// defaultCheUserDeletionEnabled is the default value for che.required param
	defaultCheUserDeletionEnabled = false

	// varCheNamespace is the che route namespace
	varCheNamespace = "che.namespace"

	// defaultCheNamespace is the default che route namespace
	defaultCheNamespace = "codeready-workspaces-operator"

	// varCheRouteName is the che dashboard route
	varCheRouteName = "che.route.name"

	// defaultCheRouteName is the default che dashboard route
	defaultCheRouteName = "codeready"

	// varCheKeycloakRouteName is the che keycloak route
	varCheKeycloakRouteName = "che.keycloak.route.name"

	// defaultCheKeycloakRouteName is the default che keycloak route
	defaultCheKeycloakRouteName = "codeready"

	// varCheAdminUsername is the che admin user username
	varCheAdminUsername = "che.admin.username"

	// varCheAdminPassword is the che admin user password
	varCheAdminPassword = "che.admin.password"

	// varWebhookImage contains the image location of the member-operator-webhook
	varWebhookImage = "webhook.image"

	// varDeployWebhook determines whether a webhook deployment will be created
	varDeployWebhook = "deploy.webhook"

	// defaultDeployWebhook is true by default
	defaultDeployWebhook = true
)

// ToolchainCluster configuration constants
const (
	clusterHealthCheckPeriod        = "cluster.healthcheck.period"
	defaultClusterHealthCheckPeriod = "10s"

	toolchainClusterTimeout          = "cluster.healthcheck.timeout"
	defaultClusterHealthCheckTimeout = "3s"
)

const (
	identityProviderName        = "identity.provider"
	defaultIdentityProviderName = "rhd"
)

// Config encapsulates the Viper configuration registry which stores the
// configuration data in-memory.
type Config struct {
	member       *viper.Viper
	secretValues map[string]string
}

// initConfig creates an initial, empty registry.
func initConfig(secret map[string]string) *Config {
	c := Config{
		member:       viper.New(),
		secretValues: secret,
	}
	c.member.SetEnvPrefix(MemberEnvPrefix)
	c.member.AutomaticEnv()
	c.member.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	c.setConfigDefaults()
	return &c
}

func LoadConfig(cl client.Client) (*Config, error) {

	secret, err := configuration.LoadFromSecret("MEMBER_OPERATOR_SECRET_NAME", cl)
	if err != nil {
		return nil, err
	}

	err = configuration.LoadFromConfigMap(MemberEnvPrefix, "MEMBER_OPERATOR_CONFIG_MAP_NAME", cl)
	if err != nil {
		return nil, err
	}

	return initConfig(secret), nil
}

func (c *Config) setConfigDefaults() {
	c.member.SetTypeByDefaultValue(true)
	c.member.SetDefault(clusterHealthCheckPeriod, defaultClusterHealthCheckPeriod)
	c.member.SetDefault(toolchainClusterTimeout, defaultClusterHealthCheckTimeout)
	c.member.SetDefault(identityProviderName, defaultIdentityProviderName)
	c.member.SetDefault(varMemberStatusRefreshTime, defaultMemberStatusRefreshTime)
	c.member.SetDefault(varConsoleNamespace, defaultConsoleNamespace)
	c.member.SetDefault(varConsoleRouteName, defaultConsoleRouteName)
	c.member.SetDefault(varCheNamespace, defaultCheNamespace)
	c.member.SetDefault(varCheRequired, defaultCheRequired)
	c.member.SetDefault(varCheUserDeletionEnabled, defaultCheUserDeletionEnabled)
	c.member.SetDefault(varCheRouteName, defaultCheRouteName)
	c.member.SetDefault(varCheKeycloakRouteName, defaultCheKeycloakRouteName)
	c.member.SetDefault(varDeployWebhook, defaultDeployWebhook)
}

func (c *Config) Print() {
	logWithValuesMemberOperator := log
	for key, value := range c.GetAllMemberParameters() {
		logWithValuesMemberOperator = logWithValuesMemberOperator.WithValues(key, value)
	}
	logWithValuesMemberOperator.Info("Member operator configuration variables:")
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
func (c *Config) GetIdP() string { //nolint: golint
	return c.member.GetString(identityProviderName)
}

// GetClusterHealthCheckPeriod returns the configured cluster health check period
func (c *Config) GetClusterHealthCheckPeriod() time.Duration {
	return c.member.GetDuration(clusterHealthCheckPeriod)
}

// GetToolchainClusterTimeout returns the configured cluster health check timeout
func (c *Config) GetToolchainClusterTimeout() time.Duration {
	return c.member.GetDuration(toolchainClusterTimeout)
}

// GetMemberStatusRefreshTime returns the time how often the MemberStatus should load and refresh the current hosted-toolchain status
func (c *Config) GetMemberStatusRefreshTime() time.Duration {
	return c.member.GetDuration(varMemberStatusRefreshTime)
}

// GetConsoleNamespace returns the console route namespace
func (c *Config) GetConsoleNamespace() string {
	return c.member.GetString(varConsoleNamespace)
}

// GetConsoleRouteName returns the console route name
func (c *Config) GetConsoleRouteName() string {
	return c.member.GetString(varConsoleRouteName)
}

// IsCheRequired returns true if the Che operator is expected to be installed. May be used in monitoring.
func (c *Config) IsCheRequired() bool {
	return c.member.GetBool(varCheRequired)
}

// IsCheUserDeletionEnabled returns true if Che user deletion should be handled by the member operator
func (c *Config) IsCheUserDeletionEnabled() bool {
	return c.member.GetBool(varCheUserDeletionEnabled)
}

// GetCheNamespace returns the Che route namespace
func (c *Config) GetCheNamespace() string {
	return c.member.GetString(varCheNamespace)
}

// GetCheRouteName returns the name of the Che dashboard route
func (c *Config) GetCheRouteName() string {
	return c.member.GetString(varCheRouteName)
}

// GetCheKeycloakRouteName returns the name of Che's keycloak route
func (c *Config) GetCheKeycloakRouteName() string {
	return c.member.GetString(varCheKeycloakRouteName)
}

// GetCheAdminUsername returns the member cluster's Che admin username
func (c *Config) GetCheAdminUsername() string {
	return c.secretValues[varCheAdminUsername]
}

// GetCheAdminPassword returns the member cluster's Che admin password
func (c *Config) GetCheAdminPassword() string {
	return c.secretValues[varCheAdminPassword]
}

// GetMemberOperatorWebhookImage returns the member operator webhook image location
func (c *Config) GetMemberOperatorWebhookImage() string {
	return c.member.GetString(varWebhookImage)
}

// DoDeployWebhook returns true if the Webhook should be deployed
func (c *Config) DoDeployWebhook() bool {
	return c.member.GetBool(varDeployWebhook)
}
