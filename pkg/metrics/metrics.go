package metrics

import (
	"github.com/codeready-toolchain/member-operator/version"
	"github.com/prometheus/client_golang/prometheus"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	k8smetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var log = logf.Log.WithName("toolchain_metrics")

// gauge with labels
var (
	// MemberOperatorVersionGaugeVec reflects the current version of the member-operator (via the `version` label)
	MemberOperatorVersionGaugeVec *prometheus.GaugeVec
)

// collections
var (
	allGaugeVecs = []*prometheus.GaugeVec{}
)

func init() {
	initMetrics()
}

const metricsPrefix = "sandbox_"

func initMetrics() {
	log.Info("initializing custom metrics")
	MemberOperatorVersionGaugeVec = newGaugeVec("member_operator_version", "Current version of the member operator", "commit")
	log.Info("custom metrics initialized")
}

// Reset resets all metrics. For testing purpose only!
func Reset() {
	log.Info("resetting custom metrics")
	initMetrics()
}

func newGaugeVec(name, help string, labels ...string) *prometheus.GaugeVec {
	v := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: metricsPrefix + name,
		Help: help,
	}, labels)
	allGaugeVecs = append(allGaugeVecs, v)
	return v
}

// RegisterCustomMetrics registers the custom metrics
func RegisterCustomMetrics() {
	// register metrics
	for _, v := range allGaugeVecs {
		k8smetrics.Registry.MustRegister(v)
	}

	// expose the MemberOperatorVersionGaugeVec metric (static ie, 1 value per build/deployment)
	MemberOperatorVersionGaugeVec.WithLabelValues(version.Commit[0:7]).Set(1)

	log.Info("custom metrics registered")
}
