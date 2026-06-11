package null

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"

	"github.com/mikehsu0618/alertsnitch/internal"
	"github.com/mikehsu0618/alertsnitch/internal/metrics"
)

func TestNullStore(t *testing.T) {
	var s Store

	// Implements the storage contracts.
	var _ internal.Storer = s
	var _ internal.HealthChecker = s

	assert.NoError(t, s.Save(context.Background(), &internal.AlertGroup{}, nil))
	assert.NoError(t, s.Close(context.Background()))

	h := s.CheckHealth(context.Background())
	assert.True(t, h.Ready)
	assert.True(t, h.Healthy)
}

// TestNullStore_RecordsSavedMetric guards against the regression where moving
// saved/failed accounting into the backends left the non-Loki backends silent:
// every backend must record persistence outcomes.
func TestNullStore_RecordsSavedMetric(t *testing.T) {
	var s Store
	ag := &internal.AlertGroup{
		Receiver: "null-metric",
		Status:   "firing",
		Alerts:   internal.Alerts{{Status: "firing"}, {Status: "firing"}},
	}
	before := testutil.ToFloat64(metrics.AlertsSavedTotal.WithLabelValues("null-metric", "firing"))

	assert.NoError(t, s.Save(context.Background(), ag, nil))

	assert.Equal(t, before+2, testutil.ToFloat64(metrics.AlertsSavedTotal.WithLabelValues("null-metric", "firing")))
}
