package loki

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	defaultTimeout   = 5 * time.Second
	minTimeout       = 1 * time.Second
	maxTimeout       = 30 * time.Second
	maxErrorBodySize = 64 * 1024
	lokiAPIPath      = "loki/api/v1"

	defaultBatchSize    = 100
	defaultFlushTimeout = 5 * time.Second
	defaultMaxRetries   = 3
	defaultRetryDelay   = time.Second
)

// Config is the typed configuration for the Loki backend. It is built directly
// from command-line/env arguments — no stringly-typed intermediate map.
type Config struct {
	URL            *url.URL
	Auth           AuthConfig
	TLS            TLSConfig
	Batch          BatchConfig
	RequestTimeout time.Duration

	// AllowedLabels lists which alert labels are promoted to Loki stream
	// labels. When empty, a built-in default set is used (see labels.go).
	AllowedLabels []string
}

// AuthConfig holds Loki multi-tenancy and basic-auth settings.
type AuthConfig struct {
	TenantID          string
	BasicAuthUser     string
	BasicAuthPassword string
}

// TLSConfig holds optional TLS / mTLS settings for HTTPS Loki endpoints.
type TLSConfig struct {
	InsecureSkipVerify bool
	CACertPath         string
	ClientCertPath     string
	ClientKeyPath      string
}

// BatchConfig controls asynchronous batch shipping.
type BatchConfig struct {
	Enabled      bool
	Size         int
	FlushTimeout time.Duration
	MaxRetries   int
	RetryDelay   time.Duration
}

// DefaultBatchConfig returns the batch defaults (disabled by default).
func DefaultBatchConfig() BatchConfig {
	return BatchConfig{
		Enabled:      false,
		Size:         defaultBatchSize,
		FlushTimeout: defaultFlushTimeout,
		MaxRetries:   defaultMaxRetries,
		RetryDelay:   defaultRetryDelay,
	}
}

// Validate checks the configuration and fills in defaults. It mutates the
// receiver only to apply the default request timeout.
func (c *Config) Validate() error {
	if c.URL == nil {
		return errors.New("url is required")
	}
	if c.URL.Scheme != "http" && c.URL.Scheme != "https" {
		return fmt.Errorf("unsupported url scheme %q, only http and https are supported", c.URL.Scheme)
	}

	switch {
	case c.RequestTimeout == 0:
		c.RequestTimeout = defaultTimeout
	case c.RequestTimeout < minTimeout:
		return fmt.Errorf("request timeout too short: %v, minimum is %v", c.RequestTimeout, minTimeout)
	case c.RequestTimeout > maxTimeout:
		return fmt.Errorf("request timeout too long: %v, maximum is %v", c.RequestTimeout, maxTimeout)
	}

	if err := c.Auth.validate(); err != nil {
		return fmt.Errorf("auth config: %w", err)
	}
	if err := c.TLS.validate(); err != nil {
		return fmt.Errorf("tls config: %w", err)
	}
	return nil
}

func (a AuthConfig) validate() error {
	if a.BasicAuthUser != "" && a.BasicAuthPassword == "" {
		return errors.New("basic auth password is required when basic auth user is set")
	}
	if a.BasicAuthPassword != "" && a.BasicAuthUser == "" {
		return errors.New("basic auth user is required when basic auth password is set")
	}
	return nil
}

func (t TLSConfig) validate() error {
	if (t.ClientCertPath != "") != (t.ClientKeyPath != "") {
		return errors.New("both client certificate path and key path must be provided together")
	}
	for _, p := range []struct {
		path, what string
	}{
		{t.CACertPath, "CA certificate"},
		{t.ClientCertPath, "client certificate"},
		{t.ClientKeyPath, "client key"},
	} {
		if p.path == "" {
			continue
		}
		if _, err := os.Stat(p.path); os.IsNotExist(err) {
			return fmt.Errorf("%s file not found: %s", p.what, p.path)
		}
	}
	if t.InsecureSkipVerify {
		logrus.Warn("TLS certificate verification is disabled - this should only be used in testing environments")
	}
	return nil
}

func buildTLSConfig(tlsCfg TLSConfig) (*tls.Config, error) {
	config := &tls.Config{
		InsecureSkipVerify: tlsCfg.InsecureSkipVerify, //nolint:gosec // opt-in, validated + warned in TLSConfig.validate
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
