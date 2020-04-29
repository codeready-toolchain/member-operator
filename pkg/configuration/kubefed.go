package configuration

import (
	"time"

	"sigs.k8s.io/kubefed/pkg/apis/core/v1beta1/defaults"
)

// Configuration constants
const (
	ClusterHealthCheckPeriod        = "cluster.healthcheck.period"
	DefaultClusterHealthCheckPeriod = defaults.DefaultClusterHealthCheckPeriod

	ClusterHealthCheckTimeout        = "cluster.healthcheck.timeout"
	DefaultClusterHealthCheckTimeout = defaults.DefaultClusterHealthCheckTimeout

	ClusterHealthCheckFailureThreshold        = "cluster.healthcheck.failurethreshold"
	DefaultClusterHealthCheckFailureThreshold = defaults.DefaultClusterHealthCheckFailureThreshold

	ClusterHealthCheckSuccessThreshold        = "cluster.healthcheck.successthreshold"
	DefaultClusterHealthCheckSuccessThreshold = defaults.DefaultClusterHealthCheckSuccessThreshold

	ClusterAvailableDelay        = "cluster.available.delay"
	DefaultClusterAvailableDelay = defaults.DefaultClusterAvailableDelay

	ClusterUnavailableDelay        = "cluster.unavailable.delay"
	DefaultClusterUnavailableDelay = defaults.DefaultClusterUnavailableDelay
)

// GetClusterHealthCheckPeriod returns the configured member cluster health check period
func (c *Registry) GetClusterHealthCheckPeriod() time.Duration {
	return c.member.GetDuration(ClusterHealthCheckPeriod)
}

// GetClusterHealthCheckTimeout returns the configured member cluster health check timeout
func (c *Registry) GetClusterHealthCheckTimeout() time.Duration {
	return c.member.GetDuration(ClusterHealthCheckTimeout)
}

// GetClusterHealthCheckFailureThreshold returns the configured member cluster health check failure threshold
func (c *Registry) GetClusterHealthCheckFailureThreshold() int64 {
	return c.member.GetInt64(ClusterHealthCheckFailureThreshold)
}

// GetClusterHealthCheckSuccessThreshold returns the configured member cluster health check failure threshold
func (c *Registry) GetClusterHealthCheckSuccessThreshold() int64 {
	return c.member.GetInt64(ClusterHealthCheckSuccessThreshold)
}

// GetClusterAvailableDelay returns the configured member cluster available delay
func (c *Registry) GetClusterAvailableDelay() time.Duration {
	return c.member.GetDuration(ClusterAvailableDelay)
}

// GetClusterUnavailableDelay returns the configured member cluster unavailable delay
func (c *Registry) GetClusterUnavailableDelay() time.Duration {
	return c.member.GetDuration(ClusterUnavailableDelay)
}
