package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"

	"github.com/mikehsu0618/alertsnitch/internal"
	"github.com/mikehsu0618/alertsnitch/internal/server"
	"github.com/mikehsu0618/alertsnitch/internal/storage"
	"github.com/mikehsu0618/alertsnitch/internal/storage/loki"
	"github.com/mikehsu0618/alertsnitch/internal/storage/sqlstore"
	"github.com/mikehsu0618/alertsnitch/pkg/env"
	"github.com/mikehsu0618/alertsnitch/version"
)

const shutdownTimeout = 30 * time.Second

// Args are the arguments that can be passed to alertsnitch
type Args struct {
	Address                string
	DBBackend              string
	DSN                    string
	MaxIdleConns           int
	MaxOpenConns           int
	MaxConnLifetimeSeconds int

	LokiTenantID          string
	LokiBasicAuthUser     string
	LokiBasicAuthPassword string

	LokiTLSInsecureSkipVerify bool
	LokiTLSCACertPath         string
	LokiTLSClientCertPath     string
	LokiTLSClientKeyPath      string

	LokiBatchEnabled      bool
	LokiBatchSize         int
	LokiBatchFlushTimeout string
	LokiBatchMaxRetries   int

	LokiAllowedLabels string

	Debug   bool
	Version bool
}

func main() {
	if err := godotenv.Load(); err != nil {
		logrus.Debug("No .env file found")
	}

	args := parseArgs()

	if args.Version {
		fmt.Println(version.GetVersion())
		os.Exit(0)
	}
	if args.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	cfg, err := buildConfig(args)
	if err != nil {
		logrus.Fatalf("invalid configuration: %s", err)
	}

	driver, err := storage.Connect(cfg)
	if err != nil {
		logrus.Fatalf("failed to connect to backend: %s", err)
	}
	logrus.Info("Connected to backend")

	s := server.New(driver, args.Debug)
	run(s, driver, args.Address)
}

func parseArgs() Args {
	args := Args{}

	flag.BoolVar(&args.Version, "version", false, "print the version and exit")
	flag.StringVar(&args.Address, "listen.address", env.GetEnv("ALERTSNITCH_ADDR", ":9567"), "address in which to listen for http requests")
	flag.BoolVar(&args.Debug, "debug", env.GetEnvAsBool("ALERTSNITCH_DEBUG", false), "enable debug mode, which dumps alerts payloads to the log as they arrive")

	flag.StringVar(&args.DBBackend, "database-backend", env.GetEnv("ALERTSNITCH_BACKEND", "mysql"), "storage backend: "+strings.Join(storage.SupportedBackends(), ", "))
	flag.StringVar(&args.DSN, "dsn", env.GetEnv(internal.DSNVar, ""), "backend connection endpoint (DSN or Loki URL)")

	flag.IntVar(&args.MaxOpenConns, "max-open-connections", env.GetEnvAsInt("ALERTSNITCH_MAX_OPEN_CONNS", 2), "maximum number of connections in the pool")
	flag.IntVar(&args.MaxIdleConns, "max-idle-connections", env.GetEnvAsInt("ALERTSNITCH_MAX_IDLE_CONNS", 1), "maximum number of idle connections in the pool")
	flag.IntVar(&args.MaxConnLifetimeSeconds, "max-connection-lifetime-seconds", env.GetEnvAsInt("ALERTSNITCH_MAX_CONN_LIFETIME", 600), "maximum number of seconds a connection is kept alive in the pool")

	flag.StringVar(&args.LokiTenantID, "tenant-id", env.GetEnv("ALERTSNITCH_LOKI_TENANT_ID", ""), "Loki tenant ID")
	flag.StringVar(&args.LokiBasicAuthUser, "basic-auth-user", env.GetEnv("ALERTSNITCH_LOKI_BASIC_AUTH_USER", ""), "Loki basic auth user")
	flag.StringVar(&args.LokiBasicAuthPassword, "basic-auth-password", env.GetEnv("ALERTSNITCH_LOKI_BASIC_AUTH_PASSWORD", ""), "Loki basic auth password")

	flag.BoolVar(&args.LokiTLSInsecureSkipVerify, "tls-insecure-skip-verify", env.GetEnvAsBool("ALERTSNITCH_LOKI_TLS_INSECURE_SKIP_VERIFY", false), "skip TLS certificate verification (only for testing)")
	flag.StringVar(&args.LokiTLSCACertPath, "tls-ca-cert-path", env.GetEnv("ALERTSNITCH_LOKI_TLS_CA_CERT_PATH", ""), "custom CA certificate file path")
	flag.StringVar(&args.LokiTLSClientCertPath, "tls-client-cert-path", env.GetEnv("ALERTSNITCH_LOKI_TLS_CLIENT_CERT_PATH", ""), "client TLS certificate file path")
	flag.StringVar(&args.LokiTLSClientKeyPath, "tls-client-key-path", env.GetEnv("ALERTSNITCH_LOKI_TLS_CLIENT_KEY_PATH", ""), "client TLS private key file path")

	flag.BoolVar(&args.LokiBatchEnabled, "loki-batch-enabled", env.GetEnvAsBool("ALERTSNITCH_LOKI_BATCH_ENABLED", false), "enable Loki batch processing")
	flag.IntVar(&args.LokiBatchSize, "loki-batch-size", env.GetEnvAsInt("ALERTSNITCH_LOKI_BATCH_SIZE", 100), "Loki batch size")
	flag.StringVar(&args.LokiBatchFlushTimeout, "loki-batch-flush-timeout", env.GetEnv("ALERTSNITCH_LOKI_BATCH_FLUSH_TIMEOUT", "5s"), "Loki batch flush timeout")
	flag.IntVar(&args.LokiBatchMaxRetries, "loki-batch-max-retries", env.GetEnvAsInt("ALERTSNITCH_LOKI_BATCH_MAX_RETRIES", 3), "Loki batch max retries")

	flag.StringVar(&args.LokiAllowedLabels, "loki-allowed-labels", env.GetEnv("ALERTSNITCH_LOKI_ALLOWED_LABELS", ""), "comma-separated list of labels to extract as stream labels (e.g., severity,priority,env)")

	flag.Parse()
	return args
}

// buildConfig translates command-line/env arguments into the typed storage
// configuration, validating values that were previously parsed (and silently
// discarded on error) deep inside the Loki backend.
func buildConfig(args Args) (storage.Config, error) {
	cfg := storage.Config{
		Backend: args.DBBackend,
		SQL: sqlstore.Config{
			DSN:                    args.DSN,
			MaxIdleConns:           args.MaxIdleConns,
			MaxOpenConns:           args.MaxOpenConns,
			MaxConnLifetimeSeconds: args.MaxConnLifetimeSeconds,
		},
	}

	if args.DBBackend == "loki" {
		lokiCfg, err := buildLokiConfig(args)
		if err != nil {
			return storage.Config{}, err
		}
		cfg.Loki = lokiCfg
	}
	return cfg, nil
}

func buildLokiConfig(args Args) (loki.Config, error) {
	if args.DSN == "" {
		return loki.Config{}, fmt.Errorf("empty Loki endpoint provided, can't connect to Loki")
	}
	endpoint, err := url.Parse(args.DSN)
	if err != nil {
		return loki.Config{}, fmt.Errorf("failed to parse Loki endpoint: %w", err)
	}

	batch := loki.DefaultBatchConfig()
	if args.LokiBatchEnabled {
		batch.Enabled = true
		if args.LokiBatchSize > 0 {
			batch.Size = args.LokiBatchSize
		}
		if args.LokiBatchMaxRetries >= 0 {
			batch.MaxRetries = args.LokiBatchMaxRetries
		}
		if args.LokiBatchFlushTimeout != "" {
			d, err := time.ParseDuration(args.LokiBatchFlushTimeout)
			if err != nil {
				return loki.Config{}, fmt.Errorf("invalid loki-batch-flush-timeout %q: %w", args.LokiBatchFlushTimeout, err)
			}
			batch.FlushTimeout = d
		}
	}

	var allowed []string
	for _, label := range strings.Split(args.LokiAllowedLabels, ",") {
		if l := strings.TrimSpace(label); l != "" {
			allowed = append(allowed, l)
		}
	}

	return loki.Config{
		URL: endpoint,
		Auth: loki.AuthConfig{
			TenantID:          args.LokiTenantID,
			BasicAuthUser:     args.LokiBasicAuthUser,
			BasicAuthPassword: args.LokiBasicAuthPassword,
		},
		TLS: loki.TLSConfig{
			InsecureSkipVerify: args.LokiTLSInsecureSkipVerify,
			CACertPath:         args.LokiTLSCACertPath,
			ClientCertPath:     args.LokiTLSClientCertPath,
			ClientKeyPath:      args.LokiTLSClientKeyPath,
		},
		Batch:         batch,
		AllowedLabels: allowed,
	}, nil
}

// run starts the server and coordinates graceful shutdown: on SIGINT/SIGTERM it
// drains in-flight requests and then flushes/closes the backend — all within a
// single bounded context, so buffered alerts get a real chance to flush.
func run(s *server.Server, driver internal.Storer, address string) {
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		logrus.Infof("Received signal %s, initiating graceful shutdown...", sig)

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := s.Shutdown(ctx); err != nil {
			logrus.Errorf("server shutdown error: %s", err)
		}
		if err := driver.Close(ctx); err != nil {
			logrus.Errorf("backend close error: %s", err)
		}
	}()

	if err := s.Start(address); err != nil {
		logrus.Fatalf("Server error: %s", err)
	}

	<-stopped
	logrus.Info("Server stopped gracefully")
}
