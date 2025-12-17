package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"gitlab.com/yakshaving.art/alertsnitch/pkg/env"

	"github.com/sirupsen/logrus"

	"gitlab.com/yakshaving.art/alertsnitch/internal"
	"gitlab.com/yakshaving.art/alertsnitch/internal/db"
	"gitlab.com/yakshaving.art/alertsnitch/internal/server"
	"gitlab.com/yakshaving.art/alertsnitch/version"
)

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

	LokiTLSInsecureSkipVerify string
	LokiTLSCACertPath         string
	LokiTLSClientCertPath     string
	LokiTLSClientKeyPath      string

	LokiBatchEnabled      bool
	LokiBatchSize         int
	LokiBatchFlushTimeout string
	LokiBatchMaxRetries   int

	LokiAllowedLabels string

	Debug   bool
	DryRun  bool
	Version bool
}

func main() {
	if err := godotenv.Load(); err != nil {
		logrus.Debug("No .env file found")
	}

	args := Args{}

	flag.BoolVar(&args.Version, "version", false, "print the version and exit")
	flag.StringVar(&args.Address, "listen.address", env.GetEnv("ALERTSNITCH_ADDR", ":9567"), "address in which to listen for http requests")
	flag.BoolVar(&args.Debug, "debug", env.GetEnvAsBool("ALERTSNITCH_DEBUG", false), "enable debug mode, which dumps alerts payloads to the log as they arrive")

	flag.StringVar(&args.DBBackend, "database-backend", env.GetEnv("ALERTSNITCH_BACKEND", "mysql"), "database backend, allowed are mysql, postgres, loki, and null")
	flag.StringVar(&args.DSN, "dsn", env.GetEnv(internal.DSNVar, ""), "Database DSN")

	flag.IntVar(&args.MaxOpenConns, "max-open-connections", env.GetEnvAsInt("ALERTSNITCH_MAX_OPEN_CONNS", 2), "maximum number of connections in the pool")
	flag.IntVar(&args.MaxIdleConns, "max-idle-connections", env.GetEnvAsInt("ALERTSNITCH_MAX_IDLE_CONNS", 1), "maximum number of idle connections in the pool")
	flag.IntVar(&args.MaxConnLifetimeSeconds, "max-connection-lifetyme-seconds", env.GetEnvAsInt("ALERTSNITCH_MAX_CONN_LIFETIME", 600), "maximum number of seconds a connection is kept alive in the pool")

	flag.StringVar(&args.LokiTenantID, "tenant-id", env.GetEnv("ALERTSNITCH_LOKI_TENANT_ID", ""), "Loki tenant ID")
	flag.StringVar(&args.LokiBasicAuthUser, "basic-auth-user", env.GetEnv("ALERTSNITCH_LOKI_BASIC_AUTH_USER", ""), "Loki basic auth user")
	flag.StringVar(&args.LokiBasicAuthPassword, "basic-auth-password", env.GetEnv("ALERTSNITCH_LOKI_BASIC_AUTH_PASSWORD", ""), "Loki basic auth password")

	flag.StringVar(&args.LokiTLSInsecureSkipVerify, "tls-insecure-skip-verify", env.GetEnv("ALERTSNITCH_LOKI_TLS_INSECURE_SKIP_VERIFY", "false"), "skip TLS certificate verification (only for testing)")
	flag.StringVar(&args.LokiTLSCACertPath, "tls-ca-cert-path", env.GetEnv("ALERTSNITCH_LOKI_TLS_CA_CERT_PATH", ""), "custom CA certificate file path")
	flag.StringVar(&args.LokiTLSClientCertPath, "tls-client-cert-path", env.GetEnv("ALERTSNITCH_LOKI_TLS_CLIENT_CERT_PATH", ""), "client TLS certificate file path")
	flag.StringVar(&args.LokiTLSClientKeyPath, "tls-client-key-path", env.GetEnv("ALERTSNITCH_LOKI_TLS_CLIENT_KEY_PATH", ""), "client TLS private key file path")

	flag.BoolVar(&args.LokiBatchEnabled, "loki-batch-enabled", env.GetEnvAsBool("ALERTSNITCH_LOKI_BATCH_ENABLED", false), "enable Loki batch processing")
	flag.IntVar(&args.LokiBatchSize, "loki-batch-size", env.GetEnvAsInt("ALERTSNITCH_LOKI_BATCH_SIZE", 100), "Loki batch size")
	flag.StringVar(&args.LokiBatchFlushTimeout, "loki-batch-flush-timeout", env.GetEnv("ALERTSNITCH_LOKI_BATCH_FLUSH_TIMEOUT", "5s"), "Loki batch flush timeout")
	flag.IntVar(&args.LokiBatchMaxRetries, "loki-batch-max-retries", env.GetEnvAsInt("ALERTSNITCH_LOKI_BATCH_MAX_RETRIES", 3), "Loki batch max retries")

	flag.StringVar(&args.LokiAllowedLabels, "loki-allowed-labels", env.GetEnv("ALERTSNITCH_LOKI_ALLOWED_LABELS", ""), "comma-separated list of labels to extract as stream labels (e.g., severity,priority,env)")

	flag.Parse()

	if args.Version {
		fmt.Println(version.GetVersion())
		os.Exit(0)
	}

	if args.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	options := map[string]string{
		"tenant_id":                args.LokiTenantID,
		"basic_auth_user":          args.LokiBasicAuthUser,
		"basic_auth_password":      args.LokiBasicAuthPassword,
		"tls_insecure_skip_verify": args.LokiTLSInsecureSkipVerify,
		"tls_ca_cert_path":         args.LokiTLSCACertPath,
		"tls_client_cert_path":     args.LokiTLSClientCertPath,
		"tls_client_key_path":      args.LokiTLSClientKeyPath,
		"allowed_labels":           args.LokiAllowedLabels,
	}

	if args.LokiBatchEnabled {
		options["batch_enabled"] = "true"
		options["batch_size"] = fmt.Sprintf("%d", args.LokiBatchSize)
		options["batch_flush_timeout"] = args.LokiBatchFlushTimeout
		options["batch_max_retries"] = fmt.Sprintf("%d", args.LokiBatchMaxRetries)
	}

	driver, err := db.Connect(args.DBBackend, db.ConnectionArgs{
		DSN:                    args.DSN,
		MaxIdleConns:           args.MaxIdleConns,
		MaxOpenConns:           args.MaxOpenConns,
		MaxConnLifetimeSeconds: args.MaxConnLifetimeSeconds,
		Options:                options,
	})
	if err != nil {
		logrus.Fatalf("failed to connect to database: %s", err)
	}
	defer func() {
		if err := driver.Close(); err != nil {
			logrus.Errorf("failed to close database connection: %s", err)
		}
	}()

	logrus.Info("Connected to database")

	s := server.New(driver, args.Debug)

	// Handle graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		logrus.Infof("Received signal %s, initiating graceful shutdown...", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := s.Shutdown(ctx); err != nil {
			logrus.Errorf("Server shutdown error: %s", err)
		}
	}()

	if err := s.Start(args.Address); err != nil {
		logrus.Fatalf("Server error: %s", err)
	}

	logrus.Info("Server stopped gracefully")
}
