package memberoperatorconfig

import (
	"fmt"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// constants
const (
	MemberStatusName = "toolchain-member-status"
)

var logger = logf.Log.WithName("configuration")

type Configuration struct {
	cfg     *toolchainv1alpha1.MemberOperatorConfigSpec
	secrets map[string]map[string]string
}

// GetConfiguration returns a Configuration using the cache, or if the cache was not initialized
// then retrieves the latest config using the provided client and updates the cache
func GetConfiguration(cl client.Client) (Configuration, error) {
	config, secrets, err := commonconfig.GetConfig(cl, &toolchainv1alpha1.MemberOperatorConfig{})
	if err != nil {
		// return default config
		logger.Error(err, "failed to retrieve Configuration")
		return Configuration{cfg: &toolchainv1alpha1.MemberOperatorConfigSpec{}}, err
	}
	return newConfiguration(config, secrets), nil
}

// GetCachedConfiguration returns a Configuration directly from the cache
func GetCachedConfiguration() Configuration {
	config, secrets := commonconfig.GetCachedConfig()
	return newConfiguration(config, secrets)
}

// ForceLoadConfiguration updates the cache using the provided client and returns the latest Configuration
func ForceLoadConfiguration(cl client.Client) (Configuration, error) {
	config, secrets, err := commonconfig.LoadLatest(cl, &toolchainv1alpha1.MemberOperatorConfig{})
	if err != nil {
		// return default config
		logger.Error(err, "failed to force load Configuration")
		return Configuration{cfg: &toolchainv1alpha1.MemberOperatorConfigSpec{}}, err
	}
	return newConfiguration(config, secrets), nil
}

func newConfiguration(config runtime.Object, secrets map[string]map[string]string) Configuration {
	if config == nil {
		// return default config if there's no config resource
		return Configuration{cfg: &toolchainv1alpha1.MemberOperatorConfigSpec{}}
	}

	membercfg, ok := config.(*toolchainv1alpha1.MemberOperatorConfig)
	if !ok {
		// return default config
		logger.Error(fmt.Errorf("cache does not contain Configuration resource type"), "failed to get Configuration from resource, using default configuration")
		return Configuration{cfg: &toolchainv1alpha1.MemberOperatorConfigSpec{}}
	}
	return Configuration{cfg: &membercfg.Spec, secrets: secrets}
}

func (c *Configuration) Print() {
	logger.Info("Member operator configuration variables", "MemberOperatorConfigSpec", c.cfg)
}

func (c *Configuration) Auth() AuthConfig {
	return AuthConfig{auth: c.cfg.Auth}
}

func (c *Configuration) Autoscaler() AutoscalerConfig {
	return AutoscalerConfig{autoscaler: c.cfg.Autoscaler}
}

func (c *Configuration) Che() CheConfig {
	return CheConfig{
		che:     c.cfg.Che,
		secrets: c.secrets,
	}
}

func (c *Configuration) Console() ConsoleConfig {
	return ConsoleConfig{console: c.cfg.Console}
}

func (c *Configuration) GitHubSecret() GitHubSecret {
	return GitHubSecret{
		s:       c.cfg.GitHubSecret,
		secrets: c.secrets,
	}
}

func (c *Configuration) MemberEnvironment() string {
	return commonconfig.GetString(c.cfg.Environment, "prod")
}

func (c *Configuration) MemberStatus() MemberStatusConfig {
	return MemberStatusConfig{c.cfg.MemberStatus}
}

func (c *Configuration) SkipUserCreation() bool {
	return commonconfig.GetBool(c.cfg.SkipUserCreation, false)
}

func (c *Configuration) ToolchainCluster() ToolchainClusterConfig {
	return ToolchainClusterConfig{c.cfg.ToolchainCluster}
}

func (c *Configuration) Webhook() WebhookConfig {
	return WebhookConfig{c.cfg.Webhook}
}

func (c *Configuration) WebConsolePlugin() WebConsolePluginConfig {
	return WebConsolePluginConfig{c.cfg.WebConsolePlugin}
}

type AuthConfig struct {
	auth toolchainv1alpha1.AuthConfig
}

func (a AuthConfig) Idp() string {
	return commonconfig.GetString(a.auth.Idp, "rhd")
}

type AutoscalerConfig struct {
	autoscaler toolchainv1alpha1.AutoscalerConfig
}

func (a AutoscalerConfig) Deploy() bool {
	return commonconfig.GetBool(a.autoscaler.Deploy, true) // TODO it is temporarily changed to true but should be changed back to false after autoscaler handling is moved to memberoperatorconfig controller
}

func (a AutoscalerConfig) BufferMemory() string {
	return commonconfig.GetString(a.autoscaler.BufferMemory, "50Mi") // TODO temporarily changed to e2e value, should be changed back to "" after autoscaler handling is moved to memberoperatorconfig controller
}

func (a AutoscalerConfig) BufferReplicas() int {
	return commonconfig.GetInt(a.autoscaler.BufferReplicas, 2) // TODO temporarily changed to e2e value, should be changed back to 1 after autoscaler handling is moved to memberoperatorconfig controller
}

type GitHubSecret struct {
	s       toolchainv1alpha1.GitHubSecret
	secrets map[string]map[string]string
}

func (gh GitHubSecret) githubSecret(secretKey string) string {
	secret := commonconfig.GetString(gh.s.Ref, "")
	return gh.secrets[secret][secretKey]
}

func (gh GitHubSecret) AccessTokenKey() string {
	key := commonconfig.GetString(gh.s.AccessTokenKey, "")
	return gh.githubSecret(key)
}

type CheConfig struct {
	che     toolchainv1alpha1.CheConfig
	secrets map[string]map[string]string
}

func (a CheConfig) cheSecret(cheSecretKey string) string {
	cheSecret := commonconfig.GetString(a.che.Secret.Ref, "")
	return a.secrets[cheSecret][cheSecretKey]
}

func (a CheConfig) AdminUserName() string {
	adminUsernameKey := commonconfig.GetString(a.che.Secret.CheAdminUsernameKey, "")
	return a.cheSecret(adminUsernameKey)
}

func (a CheConfig) AdminPassword() string {
	adminPasswordKey := commonconfig.GetString(a.che.Secret.CheAdminPasswordKey, "")
	return a.cheSecret(adminPasswordKey)
}

func (a CheConfig) IsRequired() bool {
	return commonconfig.GetBool(a.che.Required, false)
}

func (a CheConfig) IsUserDeletionEnabled() bool {
	return commonconfig.GetBool(a.che.UserDeletionEnabled, false)
}

func (a CheConfig) KeycloakRouteName() string {
	return commonconfig.GetString(a.che.KeycloakRouteName, "codeready")
}

func (a CheConfig) Namespace() string {
	return commonconfig.GetString(a.che.Namespace, "codeready-workspaces-operator")
}

func (a CheConfig) RouteName() string {
	return commonconfig.GetString(a.che.RouteName, "codeready")
}

func (a CheConfig) IsDevSpacesMode() bool {
	return a.Namespace() == "crw" && a.RouteName() == "devspaces"
}

type ConsoleConfig struct {
	console toolchainv1alpha1.ConsoleConfig
}

func (a ConsoleConfig) Namespace() string {
	return commonconfig.GetString(a.console.Namespace, "openshift-console")
}

func (a ConsoleConfig) RouteName() string {
	return commonconfig.GetString(a.console.RouteName, "console")
}

type MemberStatusConfig struct {
	memberStatus toolchainv1alpha1.MemberStatusConfig
}

func (a MemberStatusConfig) RefreshPeriod() time.Duration {
	defaultRefreshPeriod := "5s"
	refreshPeriod := commonconfig.GetString(a.memberStatus.RefreshPeriod, defaultRefreshPeriod)
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
	healthCheckPeriod := commonconfig.GetString(a.t.HealthCheckPeriod, defaultClusterHealthCheckPeriod)
	d, err := time.ParseDuration(healthCheckPeriod)
	if err != nil {
		d, _ = time.ParseDuration(defaultClusterHealthCheckPeriod)
	}
	return d
}

func (a ToolchainClusterConfig) HealthCheckTimeout() time.Duration {
	defaultClusterHealthCheckTimeout := "3s"
	healthCheckTimeout := commonconfig.GetString(a.t.HealthCheckTimeout, defaultClusterHealthCheckTimeout)
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
	return commonconfig.GetBool(a.w.Deploy, true)
}

type WebConsolePluginConfig struct {
	w toolchainv1alpha1.WebConsolePlugin
}

func (a WebConsolePluginConfig) Deploy() bool {
	return commonconfig.GetBool(a.w.Deploy, false)
}

func (a WebConsolePluginConfig) PendoKey() string {
	return commonconfig.GetString(a.w.PendoKey, "")
}

func (a WebConsolePluginConfig) PendoHost() string {
	return commonconfig.GetString(a.w.PendoHost, "cdn.pendo.io")
}
