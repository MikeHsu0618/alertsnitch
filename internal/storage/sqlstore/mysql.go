package sqlstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mikehsu0618/alertsnitch/internal"
)

// MySQL is the MySQL storage backend.
type MySQL struct {
	base
}

// ConnectMySQL opens a MySQL backend and verifies its model.
func ConnectMySQL(cfg Config) (*MySQL, error) {
	b, err := open("mysql", cfg)
	if err != nil {
		return nil, err
	}
	db := &MySQL{base: b}
	if err := db.verifyOnConnect(); err != nil {
		return nil, err
	}
	return db, nil
}

// Save persists an alert group. extraLabels is ignored by SQL backends.
func (d *MySQL) Save(ctx context.Context, data *internal.AlertGroup, _ map[string]string) error {
	return d.unitOfWork(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `
			INSERT INTO AlertGroup (time, receiver, status, externalURL, groupKey)
			VALUES (now(), ?, ?, ?, ?)`, data.Receiver, data.Status, data.ExternalURL, data.GroupKey)
		if err != nil {
			return fmt.Errorf("failed to insert into AlertGroups: %w", err)
		}
		alertGroupID, err := r.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get AlertGroups inserted id: %w", err)
		}

		if err := insertKV(ctx, tx, "GroupLabel (alertGroupID, GroupLabel, Value)", alertGroupID, data.GroupLabels); err != nil {
			return err
		}
		if err := insertKV(ctx, tx, "CommonLabel (alertGroupID, Label, Value)", alertGroupID, data.CommonLabels); err != nil {
			return err
		}
		if err := insertKV(ctx, tx, "CommonAnnotation (alertGroupID, Annotation, Value)", alertGroupID, data.CommonAnnotations); err != nil {
			return err
		}

		return insertAlerts(ctx, tx, alertGroupID, data.Alerts)
	})
}

func (*MySQL) String() string { return "mysql database driver" }

func insertAlerts(ctx context.Context, tx *sql.Tx, alertGroupID int64, alerts []internal.Alert) error {
	for _, alert := range alerts {
		var (
			result sql.Result
			err    error
		)
		if alert.EndsAt.Before(alert.StartsAt) {
			result, err = tx.ExecContext(ctx, `
			INSERT INTO Alert (alertGroupID, status, startsAt, generatorURL, fingerprint)
			VALUES (?, ?, ?, ?, ?)`,
				alertGroupID, alert.Status, alert.StartsAt, alert.GeneratorURL, alert.Fingerprint)
		} else {
			result, err = tx.ExecContext(ctx, `
			INSERT INTO Alert (alertGroupID, status, startsAt, endsAt, generatorURL, fingerprint)
			VALUES (?, ?, ?, ?, ?, ?)`,
				alertGroupID, alert.Status, alert.StartsAt, alert.EndsAt, alert.GeneratorURL, alert.Fingerprint)
		}
		if err != nil {
			return fmt.Errorf("failed to insert into Alert: %w", err)
		}
		alertID, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get Alert inserted id: %w", err)
		}

		if err := insertKV(ctx, tx, "AlertLabel (AlertID, Label, Value)", alertID, alert.Labels); err != nil {
			return err
		}
		if err := insertKV(ctx, tx, "AlertAnnotation (AlertID, Annotation, Value)", alertID, alert.Annotations); err != nil {
			return err
		}
	}
	return nil
}

// insertKV inserts a map of key/value rows into a (id, key, value) table using
// MySQL placeholder syntax. table is a compile-time constant supplied by this
// package, never user input.
func insertKV(ctx context.Context, tx *sql.Tx, table string, id int64, kv map[string]string) error {
	for k, v := range kv {
		//nolint:gosec // table is a package-internal constant, not user input
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" VALUES (?, ?, ?)", id, k, v); err != nil {
			return fmt.Errorf("failed to insert into %s: %w", table, err)
		}
	}
	return nil
}
