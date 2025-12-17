package db

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/yakshaving.art/alertsnitch/internal"
)

// readRequestBody reads and decompresses gzip body from HTTP request
func readRequestBody(t *testing.T, r *http.Request) []byte {
	t.Helper()

	var reader io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(r.Body)
		require.NoError(t, err)
		defer gzipReader.Close()
		reader = gzipReader
	}

	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	return body
}

// TestLokiConfig_Validate tests configuration validation
func TestLokiConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  LokiConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid_http_config",
			config: LokiConfig{
				Url:            mustParseURL("http://localhost:3100"),
				RequestTimeout: 5 * time.Second,
				Auth:           AuthConfig{},
				TLS:            TLSConfig{},
			},
			wantErr: false,
		},
		{
			name: "valid_https_config",
			config: LokiConfig{
				Url:            mustParseURL("https://loki.example.com"),
				RequestTimeout: 10 * time.Second,
				Auth:           AuthConfig{},
				TLS:            TLSConfig{},
			},
			wantErr: false,
		},
		{
			name: "nil_url",
			config: LokiConfig{
				Url: nil,
			},
			wantErr: true,
			errMsg:  "URL is required",
		},
		{
			name: "unsupported_scheme",
			config: LokiConfig{
				Url: mustParseURL("ftp://localhost:3100"),
			},
			wantErr: true,
			errMsg:  "unsupported URL scheme",
		},
		{
			name: "timeout_too_short",
			config: LokiConfig{
				Url:            mustParseURL("http://localhost:3100"),
				RequestTimeout: 500 * time.Millisecond,
			},
			wantErr: true,
			errMsg:  "request timeout too short",
		},
		{
			name: "timeout_too_long",
			config: LokiConfig{
				Url:            mustParseURL("http://localhost:3100"),
				RequestTimeout: 35 * time.Second,
			},
			wantErr: true,
			errMsg:  "request timeout too long",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
				// Verify default value is set
				if tt.config.RequestTimeout == 0 {
					assert.Equal(t, defaultTimeout, tt.config.RequestTimeout)
				}
			}
		})
	}
}

// TestAuthConfig_Validate tests authentication configuration validation
func TestAuthConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  AuthConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid_empty_auth",
			config:  AuthConfig{},
			wantErr: false,
		},
		{
			name: "valid_complete_basic_auth",
			config: AuthConfig{
				BasicAuthUser:     "user",
				BasicAuthPassword: "pass",
			},
			wantErr: false,
		},
		{
			name: "valid_with_tenant",
			config: AuthConfig{
				TenantID: "tenant1",
			},
			wantErr: false,
		},
		{
			name: "user_without_password",
			config: AuthConfig{
				BasicAuthUser: "user",
			},
			wantErr: true,
			errMsg:  "basic auth password is required",
		},
		{
			name: "password_without_user",
			config: AuthConfig{
				BasicAuthPassword: "pass",
			},
			wantErr: true,
			errMsg:  "basic auth user is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestTLSConfig_Validate tests TLS configuration validation
func TestTLSConfig_Validate(t *testing.T) {
	// Create temporary certificate files for testing
	tempDir := t.TempDir()
	validCertPath := filepath.Join(tempDir, "cert.pem")
	validKeyPath := filepath.Join(tempDir, "key.pem")

	// Create test files
	require.NoError(t, os.WriteFile(validCertPath, []byte("test cert"), 0644))
	require.NoError(t, os.WriteFile(validKeyPath, []byte("test key"), 0644))

	tests := []struct {
		name    string
		config  TLSConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid_empty_config",
			config:  TLSConfig{},
			wantErr: false,
		},
		{
			name: "valid_insecure_skip_verify",
			config: TLSConfig{
				InsecureSkipVerify: true,
			},
			wantErr: false,
		},
		{
			name: "valid_complete_client_cert",
			config: TLSConfig{
				ClientCertPath: validCertPath,
				ClientKeyPath:  validKeyPath,
			},
			wantErr: false,
		},
		{
			name: "cert_without_key",
			config: TLSConfig{
				ClientCertPath: validCertPath,
			},
			wantErr: true,
			errMsg:  "both client certificate path and key path must be provided together",
		},
		{
			name: "key_without_cert",
			config: TLSConfig{
				ClientKeyPath: validKeyPath,
			},
			wantErr: true,
			errMsg:  "both client certificate path and key path must be provided together",
		},
		{
			name: "nonexistent_ca_cert",
			config: TLSConfig{
				CACertPath: "/nonexistent/ca.pem",
			},
			wantErr: true,
			errMsg:  "CA certificate file not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestConnectLoki tests Loki connection
func TestConnectLoki(t *testing.T) {
	// Create test HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/labels" || r.URL.Path == "/loki/api/v1/push" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","data":[]}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tests := []struct {
		name    string
		args    ConnectionArgs
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid_connection",
			args: ConnectionArgs{
				DSN:     server.URL,
				Options: map[string]string{},
			},
			wantErr: false,
		},
		{
			name: "empty_dsn",
			args: ConnectionArgs{
				DSN: "",
			},
			wantErr: true,
			errMsg:  "empty Loki endpoint provided",
		},
		{
			name: "invalid_url",
			args: ConnectionArgs{
				DSN: "://invalid-url",
			},
			wantErr: true,
			errMsg:  "failed to parse Loki endpoint",
		},
		{
			name: "with_batch_enabled",
			args: ConnectionArgs{
				DSN: server.URL,
				Options: map[string]string{
					"batch_enabled": "true",
					"batch_size":    "50",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := connectLoki(tt.args)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, client)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, client)

				// Clean up resources
				if client != nil {
					client.Close()
				}
			}
		})
	}
}

// TestLokiClient_Ping tests Ping functionality
func TestLokiClient_Ping(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse func(w http.ResponseWriter, r *http.Request)
		wantErr        bool
	}{
		{
			name: "successful_ping",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/loki/api/v1/labels", r.URL.Path)
				w.WriteHeader(http.StatusOK)
			},
			wantErr: false,
		},
		{
			name: "server_error",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantErr: true,
		},
		{
			name: "not_found",
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			// Create client directly without calling connectLoki (avoid automatic Ping)
			endpoint, err := url.Parse(server.URL)
			require.NoError(t, err)

			cfg := LokiConfig{
				Url:            endpoint,
				RequestTimeout: defaultTimeout,
			}

			httpClient := &http.Client{
				Timeout: cfg.RequestTimeout,
			}

			client := &lokiClient{
				client: httpClient,
				cfg:    cfg,
			}

			err = client.Ping()

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestLokiClient_Save tests save functionality
func TestLokiClient_Save(t *testing.T) {
	tests := []struct {
		name           string
		batchEnabled   bool
		alertGroup     *internal.AlertGroup
		serverResponse func(w http.ResponseWriter, r *http.Request)
		wantErr        bool
	}{
		{
			name:         "successful_save_without_batch",
			batchEnabled: false,
			alertGroup:   createTestAlertGroup(),
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/loki/api/v1/push" {
					assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
					w.WriteHeader(http.StatusNoContent)
				} else if r.URL.Path == "/loki/api/v1/labels" {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`{"status":"success","data":[]}`))
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			},
			wantErr: false,
		},
		{
			name:         "successful_save_with_batch",
			batchEnabled: true,
			alertGroup:   createTestAlertGroup(),
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/loki/api/v1/push" {
					w.WriteHeader(http.StatusNoContent)
				} else if r.URL.Path == "/loki/api/v1/labels" {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`{"status":"success","data":[]}`))
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			},
			wantErr: false,
		},
		{
			name:         "server_error",
			batchEnabled: false,
			alertGroup:   createTestAlertGroup(),
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/loki/api/v1/push" {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte("bad request"))
				} else if r.URL.Path == "/loki/api/v1/labels" {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`{"status":"success","data":[]}`))
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			var client *lokiClient
			if tt.batchEnabled {
				client = createTestClientWithBatch(t, server.URL)
			} else {
				client = createTestClient(t, server.URL)
			}
			defer client.Close()

			ctx := context.Background()
			err := client.Save(ctx, tt.alertGroup)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// If batch processing is enabled, wait for batch processing to complete
			if tt.batchEnabled && !tt.wantErr {
				time.Sleep(100 * time.Millisecond)
			}
		})
	}
}

// TestLokiClient_BatchProcessing tests batch processing
func TestLokiClient_BatchProcessing(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			requestCount.Add(1)

			// Verify request body (handle gzip)
			body := readRequestBody(t, r)

			var payload payload
			err := json.Unmarshal(body, &payload)
			assert.NoError(t, err)
			assert.NotEmpty(t, payload.Streams)

			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","data":[]}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := createTestClientWithBatch(t, server.URL)
	defer client.Close()

	// Send multiple alerts, should be batch processed
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		alertGroup := createTestAlertGroup()
		alertGroup.GroupKey = fmt.Sprintf("group-%d", i)

		err := client.Save(ctx, alertGroup)
		assert.NoError(t, err)
	}

	// Wait for batch processing to complete
	time.Sleep(200 * time.Millisecond)

	// Should only have one HTTP request (batch processing)
	assert.Equal(t, int32(1), requestCount.Load())
}

// TestLokiClient_AuthHeaders tests authentication headers
func TestLokiClient_AuthHeaders(t *testing.T) {
	tests := []struct {
		name            string
		auth            AuthConfig
		expectedHeaders map[string]string
	}{
		{
			name: "with_tenant_id",
			auth: AuthConfig{
				TenantID: "tenant123",
			},
			expectedHeaders: map[string]string{
				"X-Scope-OrgID": "tenant123",
			},
		},
		{
			name: "with_basic_auth",
			auth: AuthConfig{
				BasicAuthUser:     "user",
				BasicAuthPassword: "pass",
			},
			expectedHeaders: map[string]string{
				"Authorization": "Basic dXNlcjpwYXNz", // base64(user:pass)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify headers
				for key, expectedValue := range tt.expectedHeaders {
					actualValue := r.Header.Get(key)
					assert.Equal(t, expectedValue, actualValue, "Header %s mismatch", key)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			args := ConnectionArgs{
				DSN: server.URL,
				Options: map[string]string{
					"tenant_id":           tt.auth.TenantID,
					"basic_auth_user":     tt.auth.BasicAuthUser,
					"basic_auth_password": tt.auth.BasicAuthPassword,
				},
			}

			client, err := connectLoki(args)
			require.NoError(t, err)
			defer client.Close()

			err = client.Ping()
			assert.NoError(t, err)
		})
	}
}

// TestBuildTLSConfig tests TLS configuration building
func TestBuildTLSConfig(t *testing.T) {
	tests := []struct {
		name      string
		tlsCfg    TLSConfig
		wantErr   bool
		validator func(*testing.T, *tls.Config)
	}{
		{
			name:    "default_config",
			tlsCfg:  TLSConfig{},
			wantErr: false,
			validator: func(t *testing.T, config *tls.Config) {
				assert.False(t, config.InsecureSkipVerify)
				assert.Nil(t, config.RootCAs)
				assert.Empty(t, config.Certificates)
			},
		},
		{
			name: "insecure_skip_verify",
			tlsCfg: TLSConfig{
				InsecureSkipVerify: true,
			},
			wantErr: false,
			validator: func(t *testing.T, config *tls.Config) {
				assert.True(t, config.InsecureSkipVerify)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := buildTLSConfig(tt.tlsCfg)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, config)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, config)
				if tt.validator != nil {
					tt.validator(t, config)
				}
			}
		})
	}
}

// TestDataToStream tests data conversion to stream
func TestDataToStream(t *testing.T) {
	client := &lokiClient{}

	tests := []struct {
		name        string
		alertGroup  *internal.AlertGroup
		extraLabels map[string]string
		wantErr     bool
		validator   func(*testing.T, []stream)
	}{
		{
			name:       "empty_alerts",
			alertGroup: &internal.AlertGroup{Alerts: []internal.Alert{}},
			wantErr:    true,
		},
		{
			name:        "single_alert",
			alertGroup:  createTestAlertGroup(),
			extraLabels: map[string]string{"env": "test"},
			wantErr:     false,
			validator: func(t *testing.T, streams []stream) {
				assert.Len(t, streams, 1) // One stream for one status
				assert.Contains(t, streams[0].Stream, "alert_status")
				assert.Contains(t, streams[0].Stream, "env")
				assert.Equal(t, "test", streams[0].Stream["env"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streams, err := client.dataToStream(tt.alertGroup, tt.extraLabels)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.validator != nil {
					tt.validator(t, streams)
				}
			}
		})
	}
}

// 🚀 Advanced tests begin

// TestLokiClient_ConcurrentAccess tests concurrent access safety
func TestLokiClient_ConcurrentAccess(t *testing.T) {
	// Optional: detect goroutine leaks
	// defer goleak.VerifyNone(t)

	var requestCount int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			atomic.AddInt64(&requestCount, 1)
			// Simulate processing delay
			time.Sleep(10 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	client := createTestClient(t, server.URL)
	defer client.Close()

	// Send multiple concurrent requests
	numGoroutines := 20
	numRequestsPerGoroutine := 5
	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines*numRequestsPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numRequestsPerGoroutine; j++ {
				alertGroup := createTestAlertGroup()
				alertGroup.GroupKey = fmt.Sprintf("worker-%d-request-%d", workerID, j)

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := client.Save(ctx, alertGroup)
				cancel()

				if err != nil {
					errChan <- err
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("Concurrent request failed: %v", err)
	}

	// Verify all requests were processed
	expectedRequests := int64(numGoroutines * numRequestsPerGoroutine)
	assert.Equal(t, expectedRequests, atomic.LoadInt64(&requestCount))
}

// TestLokiClient_BatchRetryMechanism tests batch retry mechanism
func TestLokiClient_BatchRetryMechanism(t *testing.T) {
	var attemptCount int64
	maxAttempts := 3

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			attempt := atomic.AddInt64(&attemptCount, 1)

			// Return error for first two attempts, succeed on third
			if attempt <= 2 {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("temporary server error"))
				return
			}

			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","data":[]}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Create batch client with retry configuration
	args := ConnectionArgs{
		DSN: server.URL,
		Options: map[string]string{
			"batch_enabled":       "true",
			"batch_size":          "1", // Small batch, trigger immediately
			"batch_flush_timeout": "10ms",
			"batch_max_retries":   fmt.Sprintf("%d", maxAttempts),
		},
	}

	client, err := connectLoki(args)
	require.NoError(t, err)
	defer client.Close()

	// Send one alert
	ctx := context.Background()
	err = client.Save(ctx, createTestAlertGroup())
	assert.NoError(t, err)

	// Wait for retries to complete (needs more time due to retry delays)
	time.Sleep(5 * time.Second)

	// Verify expected number of retries were made
	attempts := atomic.LoadInt64(&attemptCount)
	assert.Equal(t, int64(maxAttempts), attempts, "Expected %d attempts, got %d", maxAttempts, attempts)
}

// TestLokiClient_ContextTimeout tests timeout handling
func TestLokiClient_ContextTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","data":[]}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := createTestClient(t, server.URL)
	defer client.Close()

	// Use context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := client.Save(ctx, createTestAlertGroup())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

// TestLokiClient_LargePayload tests large payload handling
func TestLokiClient_LargePayload(t *testing.T) {
	var receivedBodySize int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			body := readRequestBody(t, r)
			atomic.StoreInt64(&receivedBodySize, int64(len(body)))
			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","data":[]}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := createTestClient(t, server.URL)
	defer client.Close()

	// Create AlertGroup with many alerts
	alertGroup := createTestAlertGroup()

	// Add 100 alerts
	for i := 0; i < 100; i++ {
		alert := internal.Alert{
			Status: "firing",
			Labels: map[string]string{
				"alertname": fmt.Sprintf("TestAlert-%d", i),
				"severity":  "warning",
				"instance":  fmt.Sprintf("server-%d.example.com", i),
			},
			Annotations: map[string]string{
				"summary":     fmt.Sprintf("Test alert summary for instance %d", i),
				"description": fmt.Sprintf("This is a detailed description for alert %d with lots of text to make the payload larger", i),
			},
		}
		alertGroup.Alerts = append(alertGroup.Alerts, alert)
	}

	ctx := context.Background()
	err := client.Save(ctx, alertGroup)
	assert.NoError(t, err)

	// Verify large payload was received
	receivedSize := atomic.LoadInt64(&receivedBodySize)
	assert.True(t, receivedSize > 1000, "Expected payload size > 1000 bytes, got %d", receivedSize)
}

// TestLokiClient_BatchFlushOnSize tests batch flush triggered by size
func TestLokiClient_BatchFlushOnSize(t *testing.T) {
	var flushCount int64
	batchSize := 3

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			atomic.AddInt64(&flushCount, 1)

			// Verify batch size (handle gzip)
			body := readRequestBody(t, r)

			var payload payload
			err := json.Unmarshal(body, &payload)
			require.NoError(t, err)

			// Check stream count (one stream per alert)
			streamCount := len(payload.Streams)
			assert.True(t, streamCount <= batchSize, "Stream count %d should be <= batch size %d", streamCount, batchSize)

			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","data":[]}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	args := ConnectionArgs{
		DSN: server.URL,
		Options: map[string]string{
			"batch_enabled":       "true",
			"batch_size":          fmt.Sprintf("%d", batchSize),
			"batch_flush_timeout": "1s", // Long timeout, ensure triggered by size
		},
	}

	client, err := connectLoki(args)
	require.NoError(t, err)
	defer client.Close()

	// Send alerts equal to batch size
	ctx := context.Background()
	for i := 0; i < batchSize; i++ {
		alertGroup := createTestAlertGroup()
		alertGroup.GroupKey = fmt.Sprintf("test-group-%d", i)

		err := client.Save(ctx, alertGroup)
		assert.NoError(t, err)
	}

	// Wait for batch processing
	time.Sleep(100 * time.Millisecond)

	// Should trigger one flush
	assert.Equal(t, int64(1), atomic.LoadInt64(&flushCount))

	// Send another alert, should not trigger flush immediately
	err = client.Save(ctx, createTestAlertGroup())
	assert.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int64(1), atomic.LoadInt64(&flushCount)) // Still 1
}

// TestLokiClient_BatchFlushOnTimeout tests batch flush triggered by timeout
func TestLokiClient_BatchFlushOnTimeout(t *testing.T) {
	var flushCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			atomic.AddInt64(&flushCount, 1)
			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	args := ConnectionArgs{
		DSN: server.URL,
		Options: map[string]string{
			"batch_enabled":       "true",
			"batch_size":          "100",   // Large batch size, won't be triggered
			"batch_flush_timeout": "100ms", // Short timeout
		},
	}

	client, err := connectLoki(args)
	require.NoError(t, err)
	defer client.Close()

	// Send one alert
	ctx := context.Background()
	err = client.Save(ctx, createTestAlertGroup())
	assert.NoError(t, err)

	// Wait for timeout trigger
	time.Sleep(200 * time.Millisecond)

	// Should trigger flush by timeout
	assert.Equal(t, int64(1), atomic.LoadInt64(&flushCount))
}

// TestLokiClient_ErrorResponseParsing tests error response parsing
func TestLokiClient_ErrorResponseParsing(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		responseBody   string
		expectedErrMsg string
	}{
		{
			name:           "bad_request_with_body",
			statusCode:     400,
			responseBody:   `{"error":"invalid stream format"}`,
			expectedErrMsg: "invalid stream format",
		},
		{
			name:           "internal_error_with_body",
			statusCode:     500,
			responseBody:   "internal server error occurred",
			expectedErrMsg: "internal server error occurred",
		},
		{
			name:           "error_with_empty_body",
			statusCode:     503,
			responseBody:   "",
			expectedErrMsg: "status: 503",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/loki/api/v1/push" {
					w.WriteHeader(tt.statusCode)
					w.Write([]byte(tt.responseBody))
				} else if r.URL.Path == "/loki/api/v1/labels" {
					w.WriteHeader(http.StatusOK)
				}
			}))
			defer server.Close()

			client := createTestClient(t, server.URL)
			defer client.Close()

			ctx := context.Background()
			err := client.Save(ctx, createTestAlertGroup())

			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErrMsg)
		})
	}
}

// TestLokiClient_ChannelOverflow tests channel overflow handling
func TestLokiClient_ChannelOverflow(t *testing.T) {
	// Create a normal server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loki/api/v1/push" {
			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/loki/api/v1/labels" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	args := ConnectionArgs{
		DSN: server.URL,
		Options: map[string]string{
			"batch_enabled":       "true",
			"batch_size":          "2",    // Small batch
			"batch_flush_timeout": "10ms", // Short timeout
		},
	}

	client, err := connectLoki(args)
	require.NoError(t, err)

	// Stop batch processor to let queue fill up
	client.stopBatchProcessor()

	// Manually set a small channel to simulate overflow
	client.alertCh = make(chan alertGroupWithParams, 2) // Only 2 slots
	defer client.Close()

	ctx := context.Background()

	// Quickly send alerts concurrently to fill the channel
	var saveErrors []error
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ { // Increase number and use concurrency
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			alertGroup := createTestAlertGroup()
			alertGroup.GroupKey = fmt.Sprintf("overflow-test-%d", id)

			err := client.Save(ctx, alertGroup)
			if err != nil {
				mu.Lock()
				saveErrors = append(saveErrors, err)
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// Should have some save operations fail due to channel overflow
	assert.NotEmpty(t, saveErrors, "Expected some save operations to fail due to channel overflow")

	// Check error messages
	for _, err := range saveErrors {
		assert.Contains(t, err.Error(), "alert queue is full")
	}
}

// TestGetStreamKey tests determinism of stream key generation
func TestGetStreamKey(t *testing.T) {
	client := &lokiClient{}

	// Same label mappings should generate the same key
	labels1 := map[string]string{
		"severity": "warning",
		"env":      "prod",
		"service":  "api",
	}

	labels2 := map[string]string{
		"env":      "prod",
		"service":  "api",
		"severity": "warning", // Different order
	}

	key1 := client.getStreamKey(labels1)
	key2 := client.getStreamKey(labels2)

	assert.Equal(t, key1, key2, "Stream keys should be deterministic regardless of map iteration order")
	assert.NotEmpty(t, key1)

	// Test empty labels
	emptyKey := client.getStreamKey(map[string]string{})
	assert.Equal(t, "{}", emptyKey)
}

// TestMultipleAlertStatuses tests handling of multiple alert statuses
func TestMultipleAlertStatuses(t *testing.T) {
	client := &lokiClient{}

	// Create AlertGroup with different statuses
	alertGroup := &internal.AlertGroup{
		Version:  "4",
		GroupKey: "mixed-status-group",
		Receiver: "test-receiver",
		Status:   "mixed",
		Alerts: []internal.Alert{
			{Status: "firing", Labels: map[string]string{"alertname": "Alert1"}},
			{Status: "firing", Labels: map[string]string{"alertname": "Alert2"}},
			{Status: "resolved", Labels: map[string]string{"alertname": "Alert3"}},
			{Status: "resolved", Labels: map[string]string{"alertname": "Alert4"}},
		},
		CommonLabels: map[string]string{"alertname": "TestAlert"},
	}

	streams, err := client.dataToStream(alertGroup, map[string]string{})
	require.NoError(t, err)

	// Should have 2 streams: one firing, one resolved
	assert.Len(t, streams, 2)

	// Check stream status
	statusCounts := make(map[string]int)
	for _, stream := range streams {
		status := stream.Stream["alert_status"]
		statusCounts[status] = len(stream.Values)
	}

	assert.Equal(t, 2, statusCounts["firing"])
	assert.Equal(t, 2, statusCounts["resolved"])
}

// Helper functions

func mustParseURL(rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return u
}

func createTestClient(t *testing.T, serverURL string) *lokiClient {
	args := ConnectionArgs{
		DSN:     serverURL,
		Options: map[string]string{},
	}

	client, err := connectLoki(args)
	require.NoError(t, err)
	return client
}

func createTestClientWithBatch(t *testing.T, serverURL string) *lokiClient {
	args := ConnectionArgs{
		DSN: serverURL,
		Options: map[string]string{
			"batch_enabled":       "true",
			"batch_size":          "10",
			"batch_flush_timeout": "100ms",
		},
	}

	client, err := connectLoki(args)
	require.NoError(t, err)
	return client
}

func createTestAlertGroup() *internal.AlertGroup {
	return &internal.AlertGroup{
		Version:  "4",
		GroupKey: "test-group",
		Receiver: "test-receiver",
		Status:   "firing",
		Alerts: []internal.Alert{
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "TestAlert",
					"severity":  "warning",
				},
				Annotations: map[string]string{
					"summary": "Test alert summary",
				},
			},
		},
		GroupLabels: map[string]string{
			"alertname": "TestAlert",
		},
		CommonLabels: map[string]string{
			"alertname": "TestAlert",
			"severity":  "warning",
		},
		CommonAnnotations: map[string]string{
			"summary": "Test alert summary",
		},
		ExternalURL: "http://alertmanager.example.com",
	}
}
