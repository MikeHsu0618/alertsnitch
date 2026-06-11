// Package loki implements the Loki storage backend: it ships AlertManager
// alert groups to a Loki instance as JSON log lines, with optional batching,
// multi-tenancy, and TLS/mTLS.
package loki

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/mikehsu0618/alertsnitch/internal"
	"github.com/mikehsu0618/alertsnitch/internal/metrics"
)

// Client is the Loki storage backend. It satisfies internal.Storer and
// internal.HealthChecker.
type Client struct {
	client        *http.Client
	cfg           Config
	allowedLabels map[string]bool

	batch *batchProcessor // nil when batch mode is disabled
}

// New constructs a Loki client from a typed configuration and verifies
// reachability before returning.
func New(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid loki configuration: %w", err)
	}

	tlsConfig, err := buildTLSConfig(cfg.TLS)
	if err != nil {
		return nil, fmt.Errorf("failed to build TLS config: %w", err)
	}

	c := &Client{
		client: &http.Client{
			Timeout: cfg.RequestTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
				TLSClientConfig:     tlsConfig,
				Proxy:               http.ProxyFromEnvironment,
			},
		},
		cfg:           cfg,
		allowedLabels: allowedLabelSet(cfg.AllowedLabels),
	}

	if cfg.Batch.Enabled {
		c.batch = newBatchProcessor(c, cfg.Batch)
		c.batch.start()
		logrus.Infof("Loki batch processing enabled: size=%d, timeout=%v", cfg.Batch.Size, cfg.Batch.FlushTimeout)
	}

	pingCtx, cancel := context.WithTimeout(context.Background(), cfg.RequestTimeout)
	defer cancel()
	if err := c.ping(pingCtx); err != nil {
		if c.batch != nil {
			c.batch.stop(context.Background())
		}
		metrics.DatabaseUp.Set(0)
		return nil, err
	}
	metrics.DatabaseUp.Set(1)

	return c, nil
}

// Save persists an alert group. In batch mode it enqueues the group and the
// background processor ships and accounts for it; otherwise it ships
// synchronously and records the outcome immediately.
func (c *Client) Save(ctx context.Context, data *internal.AlertGroup, extraLabels map[string]string) error {
	if c.batch != nil {
		return c.batch.enqueue(ctx, queuedAlert{
			group:       data,
			extraLabels: extraLabels,
		})
	}

	streams, err := c.dataToStream(data, extraLabels)
	if err != nil {
		return fmt.Errorf("error converting data to stream: %w", err)
	}

	err = c.pushPayload(ctx, payload{Streams: streams})
	recordOutcome(data.Receiver, data.Status, len(data.Alerts), err)
	return err
}

// Close flushes any buffered alerts and releases resources within ctx.
func (c *Client) Close(ctx context.Context) error {
	if c.batch != nil {
		c.batch.stop(ctx)
	}
	return nil
}

// recordOutcome reports a durable persistence outcome. For batch mode this is
// the fix for the original lie where enqueue was counted as saved: the outcome
// is recorded at flush resolution, and queue-full drops count as failures.
func recordOutcome(receiver, status string, alertCount int, err error) {
	metrics.RecordSaveOutcome(receiver, status, alertCount, err)
}
