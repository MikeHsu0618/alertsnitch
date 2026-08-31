// Package sqlstore implements the MySQL and Postgres storage backends. The two
// dialects share connection, transaction, model-check, health and close logic
// via the embedded base type; only the dialect-specific INSERT statements
// differ (placeholder style and how the inserted id is retrieved).
package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/mikehsu0618/alertsnitch/internal"
)

// SupportedModel is the database schema version this application understands.
const SupportedModel = "0.3.0"

// Config holds connection-pool settings shared by the SQL backends.
type Config struct {
	DSN                    string
	MaxIdleConns           int
	MaxOpenConns           int
	MaxConnLifetimeSeconds int
	MaxConnIdleTimeSeconds int
}

// base carries the shared *sql.DB plumbing for both dialects.
type base struct {
	db   *sql.DB
	name string
}

// open dials a SQL database with the given driver and applies pool settings.
func open(driver string, cfg Config) (base, error) {
	if cfg.DSN == "" {
		return base{}, fmt.Errorf("empty DSN provided, can't connect to %s database", driver)
	}

	conn, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		return base{}, fmt.Errorf("failed to open %s connection: %w", driver, err)
	}
	conn.SetMaxIdleConns(cfg.MaxIdleConns)
	conn.SetMaxOpenConns(cfg.MaxOpenConns)
	// SetConnMaxIdleTime before SetConnMaxLifetime so the pool cleaner interval
	// is configured correctly; idle recycling avoids reusing connections the
	// server or a proxy already closed (MySQL driver "bad idle connection: EOF").
	if cfg.MaxConnIdleTimeSeconds > 0 {
		conn.SetConnMaxIdleTime(time.Duration(cfg.MaxConnIdleTimeSeconds) * time.Second)
	}
	conn.SetConnMaxLifetime(time.Duration(cfg.MaxConnLifetimeSeconds) * time.Second)

	return base{db: conn, name: driver}, nil
}

// unitOfWork runs f in a transaction. MySQL deadlock (1213) is retried up to
// mysqlDeadlockMaxRetries times — concurrent LabelKV upserts can still race
// even with sorted lock order.
func (b base) unitOfWork(ctx context.Context, f func(*sql.Tx) error) error {
	var err error
	for attempt := 0; attempt < mysqlDeadlockMaxRetries; attempt++ {
		err = b.runUnitOfWork(ctx, f)
		if err == nil || ctx.Err() != nil || !isMySQLDeadlock(err) {
			return err
		}
		logrus.Warnf("MySQL deadlock detected, retrying transaction (attempt %d/%d)", attempt+1, mysqlDeadlockMaxRetries)
	}
	return err
}

func (b base) runUnitOfWork(ctx context.Context, f func(*sql.Tx) error) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := f(tx); err != nil {
		// The driver may already have rolled back when ctx was canceled.
		_ = tx.Rollback()
		return fmt.Errorf("failed execution: %w", err)
	}
	if err := tx.Commit(); err != nil {
		// Deadlocks can surface on commit; wrap so isMySQLDeadlock still matches
		// and the outer retry loop treats it like an execution failure.
		if isMySQLDeadlock(err) {
			return fmt.Errorf("failed execution: %w", err)
		}
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// CheckLiveness reports reachability only — a cheap ping with no schema query,
// suitable for a frequently-hit liveness probe.
func (b base) CheckLiveness(ctx context.Context) internal.Health {
	if err := b.db.PingContext(ctx); err != nil {
		return internal.Health{Ready: false, Healthy: false, Detail: err.Error()}
	}
	return internal.Health{Ready: true, Healthy: true}
}

// CheckReadiness reports reachability (Ready) and schema compatibility (Healthy).
func (b base) CheckReadiness(ctx context.Context) internal.Health {
	if err := b.db.PingContext(ctx); err != nil {
		return internal.Health{Ready: false, Healthy: false, Detail: err.Error()}
	}
	if err := b.checkModel(ctx); err != nil {
		return internal.Health{Ready: true, Healthy: false, Detail: err.Error()}
	}
	return internal.Health{Ready: true, Healthy: true}
}

func (b base) checkModel(ctx context.Context) error {
	var model string
	err := b.db.QueryRowContext(ctx, "SELECT version FROM Model").Scan(&model)
	switch {
	case err == sql.ErrNoRows:
		return fmt.Errorf("failed to read model version from the database: empty resultset")
	case err != nil:
		return fmt.Errorf("failed to fetch model version from the database: %w", err)
	}
	if model != SupportedModel {
		return fmt.Errorf("database model %q is not supported by this application (%s)", model, SupportedModel)
	}
	return nil
}

// Close releases the connection pool. The context bounds graceful shutdown;
// sql.DB.Close is effectively immediate, so ctx is accepted for interface
// uniformity.
func (b base) Close(_ context.Context) error {
	if b.db != nil {
		return b.db.Close()
	}
	return nil
}

// verifyOnConnect pings and checks the model immediately after connecting,
// preserving the original startup contract (refuse to start on a bad model).
func (b base) verifyOnConnect() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h := b.CheckReadiness(ctx)
	if !h.Ready {
		return fmt.Errorf("%s database is not reachable: %s", b.name, h.Detail)
	}
	if !h.Healthy {
		return fmt.Errorf("%s database model check failed: %s", b.name, h.Detail)
	}
	logrus.Debugf("Connected to %s database", b.name)
	return nil
}
