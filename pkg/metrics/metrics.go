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
	// MemberOperatorVersionGaugeVec reflects the current short commit of the member-operator (via the `version` label)
	MemberOperatorVersionGaugeVec *prometheus.GaugeVec // DEPRECATED: use MemberOperatorShortCommitGaugeVec instead
	// MemberOperatorShortCommitGaugeVec reflects the current short git commit of the member-operator (via the `commit` label)
	MemberOperatorShortCommitGaugeVec *prometheus.GaugeVec
	// MemberOperatorCommitGaugeVec reflects the current full git commit of the member-operator (via the `commit` label)
	MemberOperatorCommitGaugeVec *prometheus.GaugeVec
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
	MemberOperatorVersionGaugeVec = newGaugeVec("member_operator_version", "Current short commit of the member operator", "commit")
	MemberOperatorShortCommitGaugeVec = newGaugeVec("member_operator_short_commit", "Current short commit of the member operator", "commit")
	MemberOperatorCommitGaugeVec = newGaugeVec("member_operator_commit", "Current full commit of the member operator", "commit")
	// expose the MemberOperatorVersionGaugeVec metric (static ie, 1 value per build/deployment)
	MemberOperatorVersionGaugeVec.WithLabelValues(version.Commit[0:7]).Set(1)
	MemberOperatorShortCommitGaugeVec.WithLabelValues(version.Commit[0:7]).SetToCurrentTime() // automatically set the value to the current time, so that the highest value is the current commit
	MemberOperatorCommitGaugeVec.WithLabelValues(version.Commit).SetToCurrentTime()           // automatically set the value to the current time, so that the highest value is the current commit
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

	log.Info("custom metrics registered")
}
