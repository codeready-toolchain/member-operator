package metrics_test

import (
	"testing"
	"time"

	"github.com/codeready-toolchain/member-operator/pkg/metrics"
	"github.com/codeready-toolchain/member-operator/version"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestResetMetrics(t *testing.T) {
	// given
	version.Commit = "short12-34567890"
	metrics.Reset()
	defer metrics.Reset()
	now := time.Now()

	// when
	metrics.Reset()

	// then
	assert.InDelta(t, float64(now.Unix()), promtestutil.ToFloat64(metrics.MemberOperatorShortCommitGaugeVec.WithLabelValues("short12")), float64(time.Minute.Seconds()))
	assert.InDelta(t, float64(now.Unix()), promtestutil.ToFloat64(metrics.MemberOperatorCommitGaugeVec.WithLabelValues("short12-34567890")), float64(time.Minute.Seconds()))
}
