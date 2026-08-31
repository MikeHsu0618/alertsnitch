package sqlstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mikehsu0618/alertsnitch/internal"
	"github.com/mikehsu0618/alertsnitch/internal/metrics"
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
	labelMaps := make([]map[string]string, 0, 2+len(data.Alerts))
	labelMaps = append(labelMaps, data.GroupLabels, data.CommonLabels)
	annotationMaps := make([]map[string]string, 0, 1+len(data.Alerts))
	annotationMaps = append(annotationMaps, data.CommonAnnotations)
	for _, alert := range data.Alerts {
		labelMaps = append(labelMaps, alert.Labels)
		annotationMaps = append(annotationMaps, alert.Annotations)
	}

	err := d.unitOfWork(ctx, func(tx *sql.Tx) error {
		receiverID, err := mysqlGetReceiverID(ctx, tx, data.Receiver)
		if err != nil {
			return fmt.Errorf("failed to resolve AlertGroup AlertGroupReceiver: %w", err)
		}
		externalURLID, err := mysqlGetExternalURLID(ctx, tx, data.ExternalURL)
		if err != nil {
			return fmt.Errorf("failed to resolve AlertGroup AlertGroupExternalURL: %w", err)
		}
		groupKeyID, err := mysqlGetKeyID(ctx, tx, data.GroupKey)
		if err != nil {
			return fmt.Errorf("failed to resolve AlertGroup AlertGroupKey: %w", err)
		}

		// Resolve all unique label/annotation pairs once, in KvHash order, so
		// concurrent Saves lock LabelKV/AnnotationKV rows consistently.
		labelKVCache, err := resolveLabelKVCache(ctx, tx, labelMaps...)
		if err != nil {
			return fmt.Errorf("failed to resolve LabelKV: %w", err)
		}
		annotationKVCache, err := resolveAnnotationKVCache(ctx, tx, annotationMaps...)
		if err != nil {
			return fmt.Errorf("failed to resolve AnnotationKV: %w", err)
		}

		r, err := tx.ExecContext(ctx, `
			INSERT INTO AlertGroup (time, status, ReceiverID, ExternalURLID, KeyID)
			VALUES (now(), ?, ?, ?, ?)`, data.Status, receiverID, externalURLID, groupKeyID)
		if err != nil {
			return fmt.Errorf("failed to insert into AlertGroups: %w", err)
		}
		alertGroupID, err := r.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get AlertGroups inserted id: %w", err)
		}

		if err := insertGroupLabelsMySQL(ctx, tx, alertGroupID, data.GroupLabels, labelKVCache); err != nil {
			return err
		}
		if err := insertCommonLabelsMySQL(ctx, tx, alertGroupID, data.CommonLabels, labelKVCache); err != nil {
			return err
		}
		if err := insertCommonAnnotationsMySQL(ctx, tx, alertGroupID, data.CommonAnnotations, annotationKVCache); err != nil {
			return err
		}

		return insertAlertsMySQL(ctx, tx, alertGroupID, data.Alerts, labelKVCache, annotationKVCache)
	})
	metrics.RecordSaveOutcome(data.Receiver, data.Status, len(data.Alerts), err)
	return err
}

func (*MySQL) String() string { return "mysql database driver" }

func insertAlertsMySQL(ctx context.Context, tx *sql.Tx, alertGroupID int64, alerts []internal.Alert, labelKV, annotationKV map[string]int64) error {
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

		if err := insertAlertLabelsMySQL(ctx, tx, alertID, alert.Labels, labelKV); err != nil {
			return err
		}
		if err := insertAlertAnnotationsMySQL(ctx, tx, alertID, alert.Annotations, annotationKV); err != nil {
			return err
		}
	}
	return nil
}

func insertGroupLabelsMySQL(ctx context.Context, tx *sql.Tx, alertGroupID int64, kv map[string]string, cache map[string]int64) error {
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO GroupLabel (alertGroupID, LabelKVID)
			VALUES (?, ?)`, alertGroupID, kvIDFromCache(cache, k, v)); err != nil {
			return fmt.Errorf("failed to insert into GroupLabel: %w", err)
		}
	}
	return nil
}

func insertCommonLabelsMySQL(ctx context.Context, tx *sql.Tx, alertGroupID int64, kv map[string]string, cache map[string]int64) error {
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO CommonLabel (alertGroupID, LabelKVID)
			VALUES (?, ?)`, alertGroupID, kvIDFromCache(cache, k, v)); err != nil {
			return fmt.Errorf("failed to insert into CommonLabel: %w", err)
		}
	}
	return nil
}

func insertCommonAnnotationsMySQL(ctx context.Context, tx *sql.Tx, alertGroupID int64, kv map[string]string, cache map[string]int64) error {
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO CommonAnnotation (alertGroupID, AnnotationKVID)
			VALUES (?, ?)`, alertGroupID, kvIDFromCache(cache, k, v)); err != nil {
			return fmt.Errorf("failed to insert into CommonAnnotation: %w", err)
		}
	}
	return nil
}

func insertAlertLabelsMySQL(ctx context.Context, tx *sql.Tx, alertID int64, kv map[string]string, cache map[string]int64) error {
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO AlertLabel (AlertID, LabelKVID)
			VALUES (?, ?)`, alertID, kvIDFromCache(cache, k, v)); err != nil {
			return fmt.Errorf("failed to insert into AlertLabel: %w", err)
		}
	}
	return nil
}

func insertAlertAnnotationsMySQL(ctx context.Context, tx *sql.Tx, alertID int64, kv map[string]string, cache map[string]int64) error {
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO AlertAnnotation (AlertID, AnnotationKVID)
			VALUES (?, ?)`, alertID, kvIDFromCache(cache, k, v)); err != nil {
			return fmt.Errorf("failed to insert into AlertAnnotation: %w", err)
		}
	}
	return nil
}
