package loki

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mikehsu0618/alertsnitch/internal"
	"github.com/mikehsu0618/alertsnitch/internal/metrics"
)

// fakeLoki is an httptest server that emulates the Loki push + labels API and
// records the streams it received.
type fakeLoki struct {
	server     *httptest.Server
	mu         sync.Mutex
	received   []stream
	pushStatus int // status code to return from /push
	pushCount  int
}

func newFakeLoki() *fakeLoki {
	f := &fakeLoki{pushStatus: http.StatusNoContent}
	mux := http.NewServeMux()
	mux.HandleFunc("/loki/api/v1/labels", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/loki/api/v1/push", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.pushCount++
		if f.pushStatus >= 300 {
			http.Error(w, "boom", f.pushStatus)
			return
		}
		body := readBody(r)
		var p payload
		if err := json.Unmarshal(body, &p); err == nil {
			f.received = append(f.received, p.Streams...)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	f.server = httptest.NewServer(mux)
	return f
}

func readBody(r *http.Request) []byte {
	var reader io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil
		}
		defer gz.Close()
		reader = gz
	}
	b, _ := io.ReadAll(reader)
	return b
}

func (f *fakeLoki) streams() []stream {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]stream, len(f.received))
	copy(out, f.received)
	return out
}

func (f *fakeLoki) close() { f.server.Close() }

func testConfig(t *testing.T, rawURL string) Config {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return Config{URL: u, RequestTimeout: defaultTimeout}
}

func testAlertGroup() *internal.AlertGroup {
	now := time.Now()
	return &internal.AlertGroup{
		Version:  "4",
		Receiver: "team-x",
		Status:   "firing",
		CommonLabels: map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
			"untracked": "should-not-be-a-label",
		},
		Alerts: internal.Alerts{
			{Status: "firing", StartsAt: now, Labels: map[string]string{"severity": "critical"}},
		},
	}
}

func TestNew_PingsAndFailsWhenUnreachable(t *testing.T) {
	// An unroutable address: New must fail because the ping does not succeed.
	cfg := testConfig(t, "http://127.0.0.1:1")
	cfg.RequestTimeout = time.Second
	_, err := New(cfg)
	assert.Error(t, err)
}

func TestSave_SyncShipsStreams(t *testing.T) {
	fake := newFakeLoki()
	defer fake.close()

	client, err := New(testConfig(t, fake.server.URL))
	require.NoError(t, err)
	defer client.Close(context.Background())

	ag := testAlertGroup()
	ag.Receiver = "sync-ok"
	saved := testutil.ToFloat64(metrics.AlertsSavedTotal.WithLabelValues("sync-ok", "firing"))

	require.NoError(t, client.Save(context.Background(), ag, map[string]string{"source": "alertmanager"}))

	streams := fake.streams()
	require.Len(t, streams, 1)
	labels := streams[0].Stream
	assert.Equal(t, "alertsnitch", labels["service_name"])
	assert.Equal(t, "HighCPU", labels["alert_name"])
	assert.Equal(t, "critical", labels["severity"], "allowed label promoted")
	assert.Equal(t, "alertmanager", labels["source"], "query-param label applied")
	assert.Equal(t, "firing", labels["alert_status"])
	_, untracked := labels["untracked"]
	assert.False(t, untracked, "non-allowed label must not become a stream label")

	assert.Equal(t, saved+1, testutil.ToFloat64(metrics.AlertsSavedTotal.WithLabelValues("sync-ok", "firing")))
}

func TestSave_SyncFailureRecordsFailureMetric(t *testing.T) {
	fake := newFakeLoki()
	defer fake.close()

	client, err := New(testConfig(t, fake.server.URL))
	require.NoError(t, err)
	defer client.Close(context.Background())

	fake.pushStatus = http.StatusInternalServerError
	ag := testAlertGroup()
	ag.Receiver = "sync-fail"
	failed := testutil.ToFloat64(metrics.AlertsSavingFailuresTotal.WithLabelValues("sync-fail", "firing"))

	err = client.Save(context.Background(), ag, nil)
	assert.Error(t, err)
	assert.Equal(t, failed+1, testutil.ToFloat64(metrics.AlertsSavingFailuresTotal.WithLabelValues("sync-fail", "firing")))
}

func TestSave_BatchFlushesOnClose(t *testing.T) {
	fake := newFakeLoki()
	defer fake.close()

	cfg := testConfig(t, fake.server.URL)
	cfg.Batch = DefaultBatchConfig()
	cfg.Batch.Enabled = true
	cfg.Batch.Size = 100               // large, so it won't flush by size
	cfg.Batch.FlushTimeout = time.Hour // won't flush by time either
	client, err := New(cfg)
	require.NoError(t, err)

	ag := testAlertGroup()
	ag.Receiver = "batch-ok"
	saved := testutil.ToFloat64(metrics.AlertsSavedTotal.WithLabelValues("batch-ok", "firing"))

	require.NoError(t, client.Save(context.Background(), ag, nil))
	assert.Empty(t, fake.streams(), "nothing should be shipped before flush")

	// Close must drain the buffered alert within the deadline.
	require.NoError(t, client.Close(context.Background()))
	assert.NotEmpty(t, fake.streams(), "buffered alert must be flushed on Close")
	assert.Equal(t, saved+1, testutil.ToFloat64(metrics.AlertsSavedTotal.WithLabelValues("batch-ok", "firing")))
}

// TestSave_BatchFailureIsCounted is the regression test for the A1 bug: in
// batch mode a flush that ultimately fails must increment the failure counter,
// not be silently dropped.
func TestSave_BatchFailureIsCounted(t *testing.T) {
	fake := newFakeLoki()
	defer fake.close()

	cfg := testConfig(t, fake.server.URL)
	cfg.Batch = DefaultBatchConfig()
	cfg.Batch.Enabled = true
	cfg.Batch.MaxRetries = 1
	cfg.Batch.RetryDelay = time.Millisecond
	client, err := New(cfg)
	require.NoError(t, err)

	fake.pushStatus = http.StatusInternalServerError
	ag := testAlertGroup()
	ag.Receiver = "batch-fail"
	failed := testutil.ToFloat64(metrics.AlertsSavingFailuresTotal.WithLabelValues("batch-fail", "firing"))
	saved := testutil.ToFloat64(metrics.AlertsSavedTotal.WithLabelValues("batch-fail", "firing"))

	require.NoError(t, client.Save(context.Background(), ag, nil))
	require.NoError(t, client.Close(context.Background()))

	assert.Equal(t, failed+1, testutil.ToFloat64(metrics.AlertsSavingFailuresTotal.WithLabelValues("batch-fail", "firing")), "failed flush must be counted")
	assert.Equal(t, saved, testutil.ToFloat64(metrics.AlertsSavedTotal.WithLabelValues("batch-fail", "firing")), "failed flush must not be counted as saved")
}
