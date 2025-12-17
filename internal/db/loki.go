package db

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gitlab.com/yakshaving.art/alertsnitch/internal"
	"gitlab.com/yakshaving.art/alertsnitch/internal/metrics"
	"gitlab.com/yakshaving.art/alertsnitch/internal/middleware"
)

const (
	defaultTimeout   = 5 * time.Second
	lokiAPIPath      = "loki/api/v1"
	minTimeout       = 1 * time.Second
	maxTimeout       = 30 * time.Second
	maxErrorBodySize = 64 * 1024

	defaultBatchSize    = 100
	defaultFlushTimeout = 5 * time.Second
	defaultMaxRetries   = 3
	defaultRetryDelay   = time.Second
)

type stream struct {
	Stream map[string]string `json:"stream"`
	Values []row             `json:"values"`
}
type row struct {
	At  time.Time
	Val string
}

func (r row) MarshalJSON() ([]byte, error) {
	return json.Marshal([]string{fmt.Sprintf("%d", r.At.UnixNano()), r.Val})
}

func (r *row) UnmarshalJSON(data []byte) error {
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	if len(arr) != 2 {
		return fmt.Errorf("expected array of length 2, got %d", len(arr))
	}

	timestamp, err := strconv.ParseInt(arr[0], 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse timestamp: %w", err)
	}

	r.At = time.Unix(0, timestamp)
	r.Val = arr[1]
	return nil
}

type payload struct {
	Streams []stream `json:"streams"`
}

// alertGroupWithParams wraps an AlertGroup with its associated query parameters
// This is used for batch processing to preserve query parameters from the original request context
type alertGroupWithParams struct {
	AlertGroup  *internal.AlertGroup
	QueryParams map[string]string
}

type Config interface {
	Validate() error
}

type LokiConfig struct {
	Url            *url.URL
	Auth           AuthConfig
	RequestTimeout time.Duration
	TLS            TLSConfig
	Batch          BatchConfig
	// AllowedLabels specifies which labels from CommonLabels/GroupLabels should be extracted as stream labels
	// If empty, default labels will be used
	AllowedLabels []string
}

type TLSConfig struct {
	InsecureSkipVerify bool
	CACertPath         string
	ClientCertPath     string
	ClientKeyPath      string
}

type AuthConfig struct {
	TenantID          string
	BasicAuthUser     string
	BasicAuthPassword string
}

type BatchConfig struct {
	Enabled      bool
	Size         int
	FlushTimeout time.Duration
	MaxRetries   int
	RetryDelay   time.Duration
}

func (c *LokiConfig) Validate() error {
	if c.Url == nil {
		return errors.New("URL is required")
	}

	if c.Url.Scheme != "http" && c.Url.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme: %s, only http and https are supported", c.Url.Scheme)
	}

	if c.RequestTimeout == 0 {
		c.RequestTimeout = defaultTimeout
	} else if c.RequestTimeout < minTimeout {
		return fmt.Errorf("request timeout too short: %v, minimum is %v", c.RequestTimeout, minTimeout)
	} else if c.RequestTimeout > maxTimeout {
		return fmt.Errorf("request timeout too long: %v, maximum is %v", c.RequestTimeout, maxTimeout)
	}

	if err := c.Auth.Validate(); err != nil {
		return fmt.Errorf("auth config validation failed: %w", err)
	}

	if err := c.TLS.Validate(); err != nil {
		return fmt.Errorf("TLS config validation failed: %w", err)
	}

	return nil
}

func (a AuthConfig) Validate() error {
	if a.BasicAuthUser != "" && a.BasicAuthPassword == "" {
		return errors.New("basic auth password is required when basic auth user is set")
	}

	if a.BasicAuthPassword != "" && a.BasicAuthUser == "" {
		return errors.New("basic auth user is required when basic auth password is set")
	}

	return nil
}

func (t TLSConfig) Validate() error {
	if (t.ClientCertPath != "" && t.ClientKeyPath == "") || (t.ClientCertPath == "" && t.ClientKeyPath != "") {
		return errors.New("both client certificate path and key path must be provided together")
	}

	if t.CACertPath != "" {
		if _, err := os.Stat(t.CACertPath); os.IsNotExist(err) {
			return fmt.Errorf("CA certificate file not found: %s", t.CACertPath)
		}
	}

	if t.ClientCertPath != "" {
		if _, err := os.Stat(t.ClientCertPath); os.IsNotExist(err) {
			return fmt.Errorf("client certificate file not found: %s", t.ClientCertPath)
		}
	}

	if t.ClientKeyPath != "" {
		if _, err := os.Stat(t.ClientKeyPath); os.IsNotExist(err) {
			return fmt.Errorf("client key file not found: %s", t.ClientKeyPath)
		}
	}

	if t.InsecureSkipVerify {
		logrus.Warn("TLS certificate verification is disabled - this should only be used in testing environments")
	}

	return nil
}

type lokiClient struct {
	client *http.Client
	cfg    LokiConfig

	batchEnabled  bool
	alertCh       chan alertGroupWithParams
	stopCh        chan struct{}
	wg            sync.WaitGroup
	mu            sync.RWMutex
	started       bool
	allowedLabels map[string]bool
}

func connectLoki(args ConnectionArgs) (*lokiClient, error) {
	// TODO Rename DSN to Url
	if args.DSN == "" {
		return nil, fmt.Errorf("empty Loki endpoint provided, can't connect to Loki database")
	}

	endpoint, err := url.Parse(args.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Loki endpoint: %s", err)
	}

	batchConfig := BatchConfig{
		Enabled:      false,
		Size:         defaultBatchSize,
		FlushTimeout: defaultFlushTimeout,
		MaxRetries:   defaultMaxRetries,
		RetryDelay:   defaultRetryDelay,
	}

	if enabled := args.Options["batch_enabled"]; enabled == "true" {
		batchConfig.Enabled = true

		if size := args.Options["batch_size"]; size != "" {
			if val, err := strconv.Atoi(size); err == nil && val > 0 {
				batchConfig.Size = val
			}
		}

		if timeout := args.Options["batch_flush_timeout"]; timeout != "" {
			if val, err := time.ParseDuration(timeout); err == nil && val > 0 {
				batchConfig.FlushTimeout = val
			}
		}

		if retries := args.Options["batch_max_retries"]; retries != "" {
			if val, err := strconv.Atoi(retries); err == nil && val >= 0 {
				batchConfig.MaxRetries = val
			}
		}
	}

	// Parse allowed labels from options (comma-separated list)
	var allowedLabelsList []string
	if labels := args.Options["allowed_labels"]; labels != "" {
		for _, label := range strings.Split(labels, ",") {
			label = strings.TrimSpace(label)
			if label != "" {
				allowedLabelsList = append(allowedLabelsList, label)
			}
		}
	}

	cfg := LokiConfig{
		Url: endpoint,
		Auth: AuthConfig{
			TenantID:          args.Options["tenant_id"],
			BasicAuthUser:     args.Options["basic_auth_user"],
			BasicAuthPassword: args.Options["basic_auth_password"],
		},
		RequestTimeout: defaultTimeout,
		TLS: TLSConfig{
			InsecureSkipVerify: args.Options["tls_insecure_skip_verify"] == "true",
			CACertPath:         args.Options["tls_ca_cert_path"],
			ClientCertPath:     args.Options["tls_client_cert_path"],
			ClientKeyPath:      args.Options["tls_client_key_path"],
		},
		Batch:         batchConfig,
		AllowedLabels: allowedLabelsList,
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid Loki configuration: %w", err)
	}

	tlsConfig, err := buildTLSConfig(cfg.TLS)
	if err != nil {
		return nil, fmt.Errorf("failed to build TLS config: %w", err)
	}

	httpClient := &http.Client{
		Timeout: cfg.RequestTimeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			TLSClientConfig:     tlsConfig,
			Proxy:               http.ProxyFromEnvironment,
		},
	}

	// Build allowed labels map from config or use defaults
	allowedLabelsMap := make(map[string]bool)
	if len(cfg.AllowedLabels) > 0 {
		for _, label := range cfg.AllowedLabels {
			allowedLabelsMap[label] = true
		}
		logrus.Infof("Using custom allowed labels: %v", cfg.AllowedLabels)
	} else {
		// Use default allowed labels
		allowedLabelsMap = map[string]bool{
			"severity":  true,
			"priority":  true,
			"level":     true,
			"instance":  true,
			"job":       true,
			"team":      true,
			"env":       true,
			"service":   true,
			"pod":       true,
			"namespace": true,
			"node":      true,
			"container": true,
			"cluster":   true,
		}
	}

	client := &lokiClient{
		client:        httpClient,
		cfg:           cfg,
		batchEnabled:  cfg.Batch.Enabled,
		stopCh:        make(chan struct{}),
		allowedLabels: allowedLabelsMap,
	}

	if cfg.Batch.Enabled {
		// Use a larger buffer to handle high concurrent loads
		bufferSize := cfg.Batch.Size * 10
		if bufferSize < 1000 {
			bufferSize = 1000
		}
		client.alertCh = make(chan alertGroupWithParams, bufferSize)
		client.startBatchProcessor()
		logrus.Infof("Loki batch processing enabled: size=%d, timeout=%v, buffer=%d", cfg.Batch.Size, cfg.Batch.FlushTimeout, bufferSize)
	}

	if err := client.Ping(); err != nil {
		if cfg.Batch.Enabled {
			client.stopBatchProcessor()
		}
		return nil, err
	}

	return client, nil
}

func (c *lokiClient) startBatchProcessor() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return
	}

	c.started = true
	c.wg.Add(1)

	go c.processBatches()
}

func (c *lokiClient) stopBatchProcessor() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		return
	}

	close(c.stopCh)
	c.wg.Wait()
	c.started = false
}

func (c *lokiClient) processBatches() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.cfg.Batch.FlushTimeout)
	defer ticker.Stop()

	batch := make([]alertGroupWithParams, 0, c.cfg.Batch.Size)

	for {
		select {
		case <-c.stopCh:
			if len(batch) > 0 {
				c.flushBatch(context.Background(), batch)
			}
			return

		case alert := <-c.alertCh:
			batch = append(batch, alert)
			if len(batch) >= c.cfg.Batch.Size {
				c.flushBatch(context.Background(), batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				c.flushBatch(context.Background(), batch)
				batch = batch[:0]
			}
		}
	}
}

func (c *lokiClient) flushBatch(ctx context.Context, batch []alertGroupWithParams) {
	if len(batch) == 0 {
		return
	}

	start := time.Now()
	logrus.Debugf("Flushing batch of %d alert groups", len(batch))

	mergedStreams := c.mergeBatchStreams(batch)
	if len(mergedStreams) == 0 {
		return
	}

	payload := payload{Streams: mergedStreams}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.Batch.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := c.cfg.Batch.RetryDelay * time.Duration(attempt)
			time.Sleep(delay)
			logrus.Warnf("Retrying batch flush, attempt %d/%d", attempt, c.cfg.Batch.MaxRetries)
		}

		ctx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
		err := c.pushPayload(ctx, payload)
		cancel()

		if err == nil {
			duration := time.Since(start)
			logrus.Debugf("Successfully flushed batch of %d alert groups in %v", len(batch), duration)
			return
		}

		lastErr = err
		logrus.Errorf("Failed to flush batch (attempt %d/%d): %v", attempt+1, c.cfg.Batch.MaxRetries+1, err)
	}

	logrus.Errorf("Failed to flush batch after %d attempts: %v", c.cfg.Batch.MaxRetries+1, lastErr)
}

func (c *lokiClient) mergeBatchStreams(batch []alertGroupWithParams) []stream {
	streamMap := make(map[string]*stream)

	for _, item := range batch {
		// Use the preserved query parameters instead of creating an empty map
		streams, err := c.dataToStream(item.AlertGroup, item.QueryParams)
		if err != nil {
			logrus.Errorf("Error converting data to stream: %v", err)
			continue
		}

		for _, s := range streams {
			key := c.getStreamKey(s.Stream)
			if existing, exists := streamMap[key]; exists {
				existing.Values = append(existing.Values, s.Values...)
			} else {
				streamCopy := stream{
					Stream: s.Stream,
					Values: make([]row, len(s.Values)),
				}
				copy(streamCopy.Values, s.Values)
				streamMap[key] = &streamCopy
			}
		}
	}

	result := make([]stream, 0, len(streamMap))
	for _, s := range streamMap {
		result = append(result, *s)
	}

	return result
}

func (c *lokiClient) getStreamKey(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}

	// Sort keys to ensure deterministic output
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}

	// Use a simple sort for deterministic ordering
	for i := 0; i < len(keys)-1; i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	// Build deterministic string representation
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(k)
		buf.WriteByte(':')
		buf.WriteString(labels[k])
	}
	buf.WriteByte('}')

	return buf.String()
}

func (c *lokiClient) pushPayload(ctx context.Context, payload payload) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("error marshalling Loki request: %w", err)
	}

	// Compress payload with gzip
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	if _, err := gzipWriter.Write(payloadBytes); err != nil {
		return fmt.Errorf("error compressing payload: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("error closing gzip writer: %w", err)
	}

	uri := c.cfg.Url.JoinPath(lokiAPIPath, "push")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri.String(), &buf)
	if err != nil {
		return fmt.Errorf("error creating Loki push request: %w", err)
	}

	req.Header.Set("Content-Encoding", "gzip")
	c.setAuthAndTenantHeaders(req)

	res, err := c.client.Do(req)
	if res != nil {
		defer func() {
			if err := res.Body.Close(); err != nil {
				logrus.Errorf("failed to close response body: %s", err)
			}
		}()
	}
	if err != nil {
		return fmt.Errorf("error pushing data to Loki: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		limitedReader := io.LimitReader(res.Body, maxErrorBodySize)
		byt, readErr := io.ReadAll(limitedReader)
		if readErr != nil {
			logrus.Errorf("Failed to read error response body: %v", readErr)
			return fmt.Errorf("received non-2XX response from Loki (status: %d) and failed to read response body: %w", res.StatusCode, readErr)
		}

		if len(byt) > 0 {
			logrus.Errorf("Loki error response - Status: %d, Body: %s", res.StatusCode, string(byt))
			return fmt.Errorf("Loki returned error (status: %d): %s", res.StatusCode, string(byt))
		} else {
			logrus.Errorf("Loki error response - Status: %d, Empty body", res.StatusCode)
			return fmt.Errorf("Loki returned error with empty body (status: %d)", res.StatusCode)
		}
	}

	return nil
}

func (c *lokiClient) Ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.cfg.RequestTimeout)
	defer cancel()

	if err := c.pingContext(ctx); err != nil {
		metrics.DatabaseUp.Set(0)
		logrus.Debugf("Failed to ping Loki: %s", err)
		return err
	}

	metrics.DatabaseUp.Set(1)
	logrus.Debugf("Pinged Loki successfully")
	return nil
}

func (c *lokiClient) pingContext(ctx context.Context) error {
	uri := c.cfg.Url.JoinPath(lokiAPIPath, "/labels")
	req, err := http.NewRequestWithContext(ctx, "GET", uri.String(), nil)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	c.setAuthAndTenantHeaders(req)

	res, err := c.client.Do(req)
	if res != nil {
		defer func() {
			if err := res.Body.Close(); err != nil {
				logrus.Errorf("failed to close response body: %s", err)
			}
		}()
	}
	if err != nil {
		return fmt.Errorf("failed to ping Loki endpoint: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("Loki ping returned non-2XX status code: %d", res.StatusCode)
	}

	return nil
}

func (c *lokiClient) setAuthAndTenantHeaders(req *http.Request) {
	if c.cfg.Auth.TenantID != "" {
		req.Header.Add("X-Scope-OrgID", c.cfg.Auth.TenantID)
		logrus.Debugf("Setting tenant ID: %s", c.cfg.Auth.TenantID)
	}

	if c.cfg.Auth.BasicAuthUser != "" && c.cfg.Auth.BasicAuthPassword != "" {
		req.SetBasicAuth(c.cfg.Auth.BasicAuthUser, c.cfg.Auth.BasicAuthPassword)
		logrus.Debugf("Setting basic auth for user: %s", c.cfg.Auth.BasicAuthUser)
	}

	req.Header.Set("Content-Type", "application/json")
}

func (c *lokiClient) Save(ctx context.Context, data *internal.AlertGroup) error {
	if c.batchEnabled {
		// Extract query parameters from context before sending to batch channel
		queryParams := middleware.GetQueryParameters(ctx)
		alertWithParams := alertGroupWithParams{
			AlertGroup:  data,
			QueryParams: queryParams,
		}

		select {
		case c.alertCh <- alertWithParams:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			// If queue is full, wait and retry after some time
			select {
			case c.alertCh <- alertWithParams:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			default:
				logrus.Warn("Alert channel is full, dropping alert")
				return fmt.Errorf("alert queue is full")
			}
		}
	}

	queryParams := middleware.GetQueryParameters(ctx)
	logrus.Debugf("Query parameters: %v", queryParams)

	streams, err := c.dataToStream(data, queryParams)
	if err != nil {
		return fmt.Errorf("error converting data to stream: %w", err)
	}

	payload := payload{
		Streams: streams,
	}

	return c.pushPayload(ctx, payload)
}

func (c *lokiClient) CheckModel() error {
	return nil
}

func (c *lokiClient) Close() error {
	if c.batchEnabled {
		c.stopBatchProcessor()
	}
	return nil
}

func cloneLabels(labels map[string]string) map[string]string {
	clone := make(map[string]string, len(labels))
	for k, v := range labels {
		clone[k] = v
	}
	return clone
}

func groupAlertsByStatus(alerts []internal.Alert) map[string][]internal.Alert {
	alertsByStatus := make(map[string][]internal.Alert)
	for _, alert := range alerts {
		alertsByStatus[alert.Status] = append(alertsByStatus[alert.Status], alert)
	}
	return alertsByStatus
}

func createStreamForStatus(status string, alerts []internal.Alert, data *internal.AlertGroup, baseLabels map[string]string) (stream, error) {
	streamLabels := cloneLabels(baseLabels)
	streamLabels["alert_status"] = status

	s := stream{
		Stream: streamLabels,
		Values: make([]row, 0, len(alerts)),
	}

	for _, alert := range alerts {
		// Use alert's actual timestamp instead of time.Now()
		// For firing alerts: use StartsAt
		// For resolved alerts: use EndsAt if valid, otherwise StartsAt
		timestamp := alert.StartsAt
		if status == "resolved" && !alert.EndsAt.IsZero() && alert.EndsAt.After(alert.StartsAt) {
			timestamp = alert.EndsAt
		}
		// Fallback to now if timestamp is zero (should not happen)
		if timestamp.IsZero() {
			timestamp = time.Now()
		}

		flattenGroup := internal.FlattenAlertGroup{
			Version:           data.Version,
			GroupKey:          data.GroupKey,
			Receiver:          data.Receiver,
			Status:            data.Status,
			Alert:             alert,
			GroupLabels:       data.GroupLabels,
			CommonLabels:      data.CommonLabels,
			CommonAnnotations: data.CommonAnnotations,
			ExternalURL:       data.ExternalURL,
		}

		jsonData, err := json.Marshal(flattenGroup)
		if err != nil {
			return stream{}, fmt.Errorf("error marshalling FlattenAlertGroup: %w", err)
		}

		s.Values = append(s.Values, row{
			At:  timestamp,
			Val: string(jsonData),
		})
	}

	return s, nil
}

func (c *lokiClient) dataToStream(data *internal.AlertGroup, extraLabels map[string]string) ([]stream, error) {
	if len(data.Alerts) == 0 {
		return nil, fmt.Errorf("no alerts to process")
	}

	alertsByStatus := groupAlertsByStatus(data.Alerts)
	baseLabels := c.buildStreamLabels(data, extraLabels)

	streams := make([]stream, 0, len(alertsByStatus))

	for status, alerts := range alertsByStatus {
		s, err := createStreamForStatus(status, alerts, data, baseLabels)
		if err != nil {
			return nil, err
		}
		streams = append(streams, s)
	}

	return streams, nil
}

func (c *lokiClient) buildStreamLabels(data *internal.AlertGroup, extraLabels map[string]string) map[string]string {
	streamLabels := make(map[string]string, len(extraLabels)+len(data.CommonLabels)+len(data.GroupLabels)+4)

	for key, value := range extraLabels {
		streamLabels[key] = value
	}

	for commonLabel, commonValue := range data.CommonLabels {
		if c.allowedLabels[commonLabel] {
			streamLabels[commonLabel] = commonValue
		}
	}

	for groupLabel, groupValue := range data.GroupLabels {
		if c.allowedLabels[groupLabel] {
			streamLabels[groupLabel] = groupValue
		}
	}

	streamLabels["service_name"] = "alertsnitch"
	streamLabels["receiver"] = data.Receiver
	streamLabels["status"] = data.Status
	streamLabels["alert_name"] = data.CommonLabels["alertname"]

	return streamLabels
}

func buildTLSConfig(tlsCfg TLSConfig) (*tls.Config, error) {
	config := &tls.Config{
		InsecureSkipVerify: tlsCfg.InsecureSkipVerify,
	}

	if tlsCfg.CACertPath != "" {
		caCert, err := os.ReadFile(tlsCfg.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate from %s: %w", tlsCfg.CACertPath, err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", tlsCfg.CACertPath)
		}
		config.RootCAs = caCertPool
		logrus.Infof("Loaded custom CA certificate from: %s", tlsCfg.CACertPath)
	}

	if tlsCfg.ClientCertPath != "" && tlsCfg.ClientKeyPath != "" {
		cert, err := tls.LoadX509KeyPair(tlsCfg.ClientCertPath, tlsCfg.ClientKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate from %s and %s: %w",
				tlsCfg.ClientCertPath, tlsCfg.ClientKeyPath, err)
		}
		config.Certificates = []tls.Certificate{cert}
		logrus.Infof("Loaded client certificate from: %s", tlsCfg.ClientCertPath)
	}

	return config, nil
}
