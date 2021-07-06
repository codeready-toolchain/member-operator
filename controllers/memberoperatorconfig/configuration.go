package memberoperatorconfig

import (
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// constants
const (
	MemberStatusName = "toolchain-member-status"
)

var log = logf.Log.WithName("configuration")

type Configuration struct {
	m       *toolchainv1alpha1.MemberOperatorConfigSpec
	secrets map[string]map[string]string
}

func (c *Configuration) Print() {
	log.Info("Member operator configuration variables", "MemberOperatorConfigSpec", c.m)
}

func (c *Configuration) Auth() AuthConfig {
	return AuthConfig{auth: c.m.Auth}
}

func (c *Configuration) Autoscaler() AutoscalerConfig {
	return AutoscalerConfig{autoscaler: c.m.Autoscaler}
}

func (c *Configuration) Che() CheConfig {
	return CheConfig{
		che:     c.m.Che,
		secrets: c.secrets,
	}
}

func (c *Configuration) Console() ConsoleConfig {
	return ConsoleConfig{console: c.m.Console}
}

func (c *Configuration) MemberStatus() MemberStatusConfig {
	return MemberStatusConfig{c.m.MemberStatus}
}

func (c *Configuration) ToolchainCluster() ToolchainClusterConfig {
	return ToolchainClusterConfig{c.m.ToolchainCluster}
}

func (c *Configuration) Webhook() WebhookConfig {
	return WebhookConfig{c.m.Webhook}
}

type AuthConfig struct {
	auth toolchainv1alpha1.AuthConfig
}

func (a AuthConfig) Idp() string {
	return getString(a.auth.Idp, "rhd")
}

type AutoscalerConfig struct {
	autoscaler toolchainv1alpha1.AutoscalerConfig
}

func (a AutoscalerConfig) Deploy() bool {
	return getBool(a.autoscaler.Deploy, true) // TODO it is temporarily changed to true but should be changed back to false after autoscaler handling is moved to memberoperatorconfig controller
}

func (a AutoscalerConfig) BufferMemory() string {
	return getString(a.autoscaler.BufferMemory, "50Mi") // TODO temporarily changed to e2e value, should be changed back to "" after autoscaler handling is moved to memberoperatorconfig controller
}

func (a AutoscalerConfig) BufferReplicas() int {
	return getInt(a.autoscaler.BufferReplicas, 2) // TODO temporarily changed to e2e value, should be changed back to 1 after autoscaler handling is moved to memberoperatorconfig controller
}

type CheConfig struct {
	che     toolchainv1alpha1.CheConfig
	secrets map[string]map[string]string
}

func (a CheConfig) cheSecret(cheSecretKey string) string {
	cheSecret := getString(a.che.Secret.Ref, "")
	return a.secrets[cheSecret][cheSecretKey]
}

func (a CheConfig) AdminUserName() string {
	adminUsernameKey := getString(a.che.Secret.CheAdminUsernameKey, "")
	return a.cheSecret(adminUsernameKey)
}

func (a CheConfig) AdminPassword() string {
	adminPasswordKey := getString(a.che.Secret.CheAdminPasswordKey, "")
	return a.cheSecret(adminPasswordKey)
}

func (a CheConfig) IsRequired() bool {
	return getBool(a.che.Required, false)
}

func (a CheConfig) IsUserDeletionEnabled() bool {
	return getBool(a.che.UserDeletionEnabled, false)
}

func (a CheConfig) KeycloakRouteName() string {
	return getString(a.che.KeycloakRouteName, "codeready")
}

func (a CheConfig) Namespace() string {
	return getString(a.che.Namespace, "codeready-workspaces-operator")
}

func (a CheConfig) RouteName() string {
	return getString(a.che.RouteName, "codeready")
}

type ConsoleConfig struct {
	console toolchainv1alpha1.ConsoleConfig
}

func (a ConsoleConfig) Namespace() string {
	return getString(a.console.Namespace, "openshift-console")
}

func (a ConsoleConfig) RouteName() string {
	return getString(a.console.RouteName, "console")
}

type MemberStatusConfig struct {
	memberStatus toolchainv1alpha1.MemberStatusConfig
}

func (a MemberStatusConfig) RefreshPeriod() time.Duration {
	defaultRefreshPeriod := "5s"
	refreshPeriod := getString(a.memberStatus.RefreshPeriod, defaultRefreshPeriod)
	d, err := time.ParseDuration(refreshPeriod)
	if err != nil {
		d, _ = time.ParseDuration(defaultRefreshPeriod)
	}
	return d
}

type ToolchainClusterConfig struct {
	t toolchainv1alpha1.ToolchainClusterConfig
}

func (a ToolchainClusterConfig) HealthCheckPeriod() time.Duration {
	defaultClusterHealthCheckPeriod := "10s"
	healthCheckPeriod := getString(a.t.HealthCheckPeriod, defaultClusterHealthCheckPeriod)
	d, err := time.ParseDuration(healthCheckPeriod)
	if err != nil {
		d, _ = time.ParseDuration(defaultClusterHealthCheckPeriod)
	}
	return d
}

func (a ToolchainClusterConfig) HealthCheckTimeout() time.Duration {
	defaultClusterHealthCheckTimeout := "3s"
	healthCheckTimeout := getString(a.t.HealthCheckTimeout, defaultClusterHealthCheckTimeout)
	d, err := time.ParseDuration(healthCheckTimeout)
	if err != nil {
		d, _ = time.ParseDuration(defaultClusterHealthCheckTimeout)
	}
	return d
}

type WebhookConfig struct {
	w toolchainv1alpha1.WebhookConfig
}

func (a WebhookConfig) Deploy() bool {
	return getBool(a.w.Deploy, true)
}

func getBool(value *bool, defaultValue bool) bool {
	if value != nil {
		return *value
	}
	return defaultValue
}

func getInt(value *int, defaultValue int) int {
	if value != nil {
		return *value
	}
	return defaultValue
}

func getString(value *string, defaultValue string) string {
	if value != nil {
		return *value
	}
	return defaultValue
}
