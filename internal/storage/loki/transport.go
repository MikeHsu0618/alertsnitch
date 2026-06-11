package loki

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/sirupsen/logrus"

	"github.com/mikehsu0618/alertsnitch/internal"
)

// pushPayload gzip-compresses and POSTs a payload to Loki's push endpoint.
func (c *Client) pushPayload(ctx context.Context, p payload) error {
	payloadBytes, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("error marshaling loki request: %w", err)
	}

	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	if _, err := gzipWriter.Write(payloadBytes); err != nil {
		return fmt.Errorf("error compressing payload: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("error closing gzip writer: %w", err)
	}

	uri := c.cfg.URL.JoinPath(lokiAPIPath, "push")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri.String(), &buf)
	if err != nil {
		return fmt.Errorf("error creating loki push request: %w", err)
	}
	req.Header.Set("Content-Encoding", "gzip")
	c.setAuthAndTenantHeaders(req)

	res, err := c.client.Do(req)
	if res != nil {
		defer func() {
			if cerr := res.Body.Close(); cerr != nil {
				logrus.Errorf("failed to close response body: %s", cerr)
			}
		}()
	}
	if err != nil {
		return fmt.Errorf("error pushing data to loki: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(res.Body, maxErrorBodySize))
		if readErr != nil {
			return fmt.Errorf("loki returned non-2xx (status %d) and reading the body failed: %w", res.StatusCode, readErr)
		}
		if len(body) > 0 {
			return fmt.Errorf("loki returned error (status %d): %s", res.StatusCode, string(body))
		}
		return fmt.Errorf("loki returned error with empty body (status %d)", res.StatusCode)
	}
	return nil
}

func (c *Client) setAuthAndTenantHeaders(req *http.Request) {
	if c.cfg.Auth.TenantID != "" {
		req.Header.Set("X-Scope-OrgID", c.cfg.Auth.TenantID)
	}
	if c.cfg.Auth.BasicAuthUser != "" && c.cfg.Auth.BasicAuthPassword != "" {
		req.SetBasicAuth(c.cfg.Auth.BasicAuthUser, c.cfg.Auth.BasicAuthPassword)
	}
	req.Header.Set("Content-Type", "application/json")
}

// CheckHealth reports whether Loki is reachable. Loki has no schema/model, so
// Healthy is always true once reachable.
func (c *Client) CheckHealth(ctx context.Context) internal.Health {
	if err := c.ping(ctx); err != nil {
		return internal.Health{Ready: false, Healthy: true, Detail: err.Error()}
	}
	return internal.Health{Ready: true, Healthy: true}
}

func (c *Client) ping(ctx context.Context) error {
	uri := c.cfg.URL.JoinPath(lokiAPIPath, "/labels")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri.String(), http.NoBody)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}
	c.setAuthAndTenantHeaders(req)

	res, err := c.client.Do(req)
	if res != nil {
		defer func() {
			if cerr := res.Body.Close(); cerr != nil {
				logrus.Errorf("failed to close response body: %s", cerr)
			}
		}()
	}
	if err != nil {
		return fmt.Errorf("failed to ping loki endpoint: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("loki ping returned non-2xx status code: %d", res.StatusCode)
	}
	return nil
}
