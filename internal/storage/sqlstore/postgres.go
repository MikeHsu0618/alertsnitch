package sqlstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mikehsu0618/alertsnitch/internal"
	"github.com/mikehsu0618/alertsnitch/internal/metrics"
)

// Postgres is the PostgreSQL storage backend.
type Postgres struct {
	base
}

// ConnectPostgres opens a Postgres backend and verifies its model.
func ConnectPostgres(cfg Config) (*Postgres, error) {
	b, err := open("postgres", cfg)
	if err != nil {
		return nil, err
	}
	db := &Postgres{base: b}
	if err := db.verifyOnConnect(); err != nil {
		return nil, err
	}
	return db, nil
}

// Save persists an alert group. extraLabels is ignored by SQL backends.
func (d *Postgres) Save(ctx context.Context, data *internal.AlertGroup, _ map[string]string) error {
	err := d.unitOfWork(ctx, func(tx *sql.Tx) error {
		var alertGroupID int64
		err := tx.QueryRowContext(ctx, `
			INSERT INTO AlertGroup (time, receiver, status, externalURL, groupKey)
			VALUES (current_timestamp, $1, $2, $3, $4) RETURNING ID`,
			data.Receiver, data.Status, data.ExternalURL, data.GroupKey).Scan(&alertGroupID)
		if err != nil {
			return fmt.Errorf("failed to insert into AlertGroups: %w", err)
		}

		if err := insertKVPG(ctx, tx, "GroupLabel (alertGroupID, GroupLabel, Value)", alertGroupID, data.GroupLabels); err != nil {
			return err
		}
		if err := insertKVPG(ctx, tx, "CommonLabel (alertGroupID, Label, Value)", alertGroupID, data.CommonLabels); err != nil {
			return err
		}
		if err := insertKVPG(ctx, tx, "CommonAnnotation (alertGroupID, Annotation, Value)", alertGroupID, data.CommonAnnotations); err != nil {
			return err
		}

		return insertAlertsPG(ctx, tx, alertGroupID, data.Alerts)
	})
	metrics.RecordSaveOutcome(data.Receiver, data.Status, len(data.Alerts), err)
	return err
}

func (*Postgres) String() string { return "postgres database driver" }

func insertAlertsPG(ctx context.Context, tx *sql.Tx, alertGroupID int64, alerts []internal.Alert) error {
	for _, alert := range alerts {
		var row *sql.Row
		if alert.EndsAt.Before(alert.StartsAt) {
			row = tx.QueryRowContext(ctx, `
			INSERT INTO Alert (alertGroupID, status, startsAt, generatorURL, fingerprint)
			VALUES ($1, $2, $3, $4, $5) RETURNING ID`,
				alertGroupID, alert.Status, alert.StartsAt, alert.GeneratorURL, alert.Fingerprint)
		} else {
			row = tx.QueryRowContext(ctx, `
			INSERT INTO Alert (alertGroupID, status, startsAt, endsAt, generatorURL, fingerprint)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING ID`,
				alertGroupID, alert.Status, alert.StartsAt, alert.EndsAt, alert.GeneratorURL, alert.Fingerprint)
		}
		var alertID int64
		if err := row.Scan(&alertID); err != nil {
			return fmt.Errorf("failed to insert into Alert: %w", err)
		}

		if err := insertKVPG(ctx, tx, "AlertLabel (AlertID, Label, Value)", alertID, alert.Labels); err != nil {
			return err
		}
		if err := insertKVPG(ctx, tx, "AlertAnnotation (AlertID, Annotation, Value)", alertID, alert.Annotations); err != nil {
			return err
		}
	}
	return nil
}

// insertKVPG inserts a map of key/value rows into a (id, key, value) table
// using Postgres placeholder syntax. table is a compile-time constant supplied
// by this package, never user input.
func insertKVPG(ctx context.Context, tx *sql.Tx, table string, id int64, kv map[string]string) error {
	for k, v := range kv {
		//nolint:gosec // table is a package-internal constant, not user input
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" VALUES ($1, $2, $3)", id, k, v); err != nil {
			return fmt.Errorf("failed to insert into %s: %w", table, err)
		}
	}
	return nil
}
