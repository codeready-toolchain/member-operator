package metrics

import (
	"testing"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	k8smetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestInitGaugeVec(t *testing.T) {
	// given
	m := newGaugeVec("test_gauge_vec", "test gauge description", "cluster_name")

	// when
	m.WithLabelValues("member-1").Set(1)
	m.WithLabelValues("member-2").Set(2)

	// then
	assert.InDelta(t, float64(1), promtestutil.ToFloat64(m.WithLabelValues("member-1")), 0.01)
	assert.InDelta(t, float64(2), promtestutil.ToFloat64(m.WithLabelValues("member-2")), 0.01)
}

func TestRegisterCustomMetrics(t *testing.T) {
	// when
	RegisterCustomMetrics()

	// then
	// verify all metrics were registered successfully
	for _, m := range allGaugeVecs {
		assert.True(t, k8smetrics.Registry.Unregister(m))
	}
}
