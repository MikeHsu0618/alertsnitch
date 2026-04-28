package db

import (
	"context"
	"fmt"
	"time"

	"database/sql"

	"github.com/sirupsen/logrus"
	"gitlab.com/yakshaving.art/alertsnitch/internal"
	"gitlab.com/yakshaving.art/alertsnitch/internal/metrics"
)

// PostgresDB A database that does nothing
type PostgresDB struct {
	db *sql.DB
}

// ConnectPG connect to a Postgres database using the provided data source name
func connectPG(args ConnectionArgs) (*PostgresDB, error) {
	if args.DSN == "" {
		return nil, fmt.Errorf("Empty DSN provided, can't connect to Postgres database")
	}

	logrus.Debugf("Connecting to Postgres database with DSN: %s", args.DSN)

	connection, err := sql.Open("postgres", args.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open Postgres connection: %w", err)
	}

	connection.SetMaxIdleConns(args.MaxIdleConns)
	connection.SetMaxOpenConns(args.MaxOpenConns)
	connection.SetConnMaxLifetime(time.Duration(args.MaxConnLifetimeSeconds) * time.Second)

	database := &PostgresDB{
		db: connection,
	}

	err = database.Ping()
	if err != nil {
		return nil, err
	}
	logrus.Debugf("Connected to Postgres database")

	return database, database.CheckModel()
}

// Save implements Storer interface
func (d PostgresDB) Save(ctx context.Context, data *internal.AlertGroup) error {
	return d.unitOfWork(func(tx *sql.Tx) error {
		receiverID, err := postgresGetReceiverID(tx, data.Receiver)
		if err != nil {
			return fmt.Errorf("failed to resolve AlertGroup AlertGroupReceiver: %w", err)
		}
		externalURLID, err := postgresGetExternalURLID(tx, data.ExternalURL)
		if err != nil {
			return fmt.Errorf("failed to resolve AlertGroup AlertGroupExternalURL: %w", err)
		}
		groupKeyID, err := postgresGetKeyID(tx, data.GroupKey)
		if err != nil {
			return fmt.Errorf("failed to resolve AlertGroup AlertGroupKey: %w", err)
		}

		r := tx.QueryRow(`
			INSERT INTO AlertGroup (time, status, ReceiverID, ExternalURLID, KeyID)
			VALUES (current_timestamp, $1, $2, $3, $4) RETURNING ID`,
			data.Status, receiverID, externalURLID, groupKeyID)

		var alertGroupID int64
		err = r.Scan(&alertGroupID)
		if err != nil {
			return fmt.Errorf("failed to insert into AlertGroups: %w", err)
		}

		for k, v := range data.GroupLabels {
			kvID, err := postgresGetLabelKVID(tx, k, v)
			if err != nil {
				return fmt.Errorf("failed to resolve GroupLabel LabelKV: %w", err)
			}
			_, err = tx.Exec(`
				INSERT INTO GroupLabel (alertGroupID, LabelKVID)
				VALUES ($1, $2)`, alertGroupID, kvID)
			if err != nil {
				return fmt.Errorf("failed to insert into GroupLabel: %w", err)
			}
		}
		for k, v := range data.CommonLabels {
			kvID, err := postgresGetLabelKVID(tx, k, v)
			if err != nil {
				return fmt.Errorf("failed to resolve CommonLabel LabelKV: %w", err)
			}
			_, err = tx.Exec(`
				INSERT INTO CommonLabel (alertGroupID, LabelKVID)
				VALUES ($1, $2)`, alertGroupID, kvID)
			if err != nil {
				return fmt.Errorf("failed to insert into CommonLabel: %w", err)
			}
		}
		for k, v := range data.CommonAnnotations {
			kvID, err := postgresGetAnnotationKVID(tx, k, v)
			if err != nil {
				return fmt.Errorf("failed to resolve CommonAnnotation AnnotationKV: %w", err)
			}
			_, err = tx.Exec(`
				INSERT INTO CommonAnnotation (alertGroupID, AnnotationKVID)
				VALUES ($1, $2)`, alertGroupID, kvID)
			if err != nil {
				return fmt.Errorf("failed to insert into CommonAnnotation: %w", err)
			}
		}

		for _, alert := range data.Alerts {
			if alert.EndsAt.Before(alert.StartsAt) {
				r = tx.QueryRow(`
				INSERT INTO Alert (alertGroupID, status, startsAt, generatorURL, fingerprint)
				VALUES ($1, $2, $3, $4, $5) RETURNING ID`,
					alertGroupID, alert.Status, alert.StartsAt, alert.GeneratorURL, alert.Fingerprint)
			} else {
				r = tx.QueryRow(`
				INSERT INTO Alert (alertGroupID, status, startsAt, endsAt, generatorURL, fingerprint)
				VALUES ($1, $2, $3, $4, $5, $6) RETURNING ID`,
					alertGroupID, alert.Status, alert.StartsAt, alert.EndsAt, alert.GeneratorURL, alert.Fingerprint)
			}
			var alertID int64
			if err := r.Scan(&alertID); err != nil {
				return fmt.Errorf("failed to insert into Alert: %w", err)
			}

			for k, v := range alert.Labels {
				kvID, err := postgresGetLabelKVID(tx, k, v)
				if err != nil {
					return fmt.Errorf("failed to resolve AlertLabel LabelKV: %w", err)
				}
				_, err = tx.Exec(`
					INSERT INTO AlertLabel (AlertID, LabelKVID)
					VALUES ($1, $2)`, alertID, kvID)
				if err != nil {
					return fmt.Errorf("failed to insert into AlertLabel: %w", err)
				}
			}
			for k, v := range alert.Annotations {
				kvID, err := postgresGetAnnotationKVID(tx, k, v)
				if err != nil {
					return fmt.Errorf("failed to resolve AlertAnnotation AnnotationKV: %w", err)
				}
				_, err = tx.Exec(`
					INSERT INTO AlertAnnotation (AlertID, AnnotationKVID)
					VALUES ($1, $2)`, alertID, kvID)
				if err != nil {
					return fmt.Errorf("failed to insert into AlertAnnotation: %w", err)
				}
			}
		}

		return nil
	})
}

func (d PostgresDB) unitOfWork(f func(*sql.Tx) error) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	err = f(tx)

	if err != nil {
		e := tx.Rollback()
		if e != nil {
			return fmt.Errorf("failed to rollback transaction (%w) after failing execution: %w", e, err)
		}
		return fmt.Errorf("failed execution: %w", err)
	}
	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// Ping implements Storer interface
func (d PostgresDB) Ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := d.db.PingContext(ctx); err != nil {
		metrics.DatabaseUp.Set(0)
		logrus.Debugf("Failed to ping database: %s", err)

		return err
	}
	metrics.DatabaseUp.Set(1)
	logrus.Debugf("Pinged database...")
	return nil
}

// CheckModel implements Storer interface
func (d PostgresDB) CheckModel() error {
	rows, err := d.db.Query("SELECT version FROM Model")
	if err != nil {
		return fmt.Errorf("failed to fetch model version from the database: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return fmt.Errorf("failed to read model version from the database: empty resultset")
	}

	var model string
	if err := rows.Scan(&model); err != nil {
		return fmt.Errorf("failed to read model version from the database: %w", err)
	}

	if model != SupportedModel {
		return fmt.Errorf("model '%s' is not supported by this application (%s)", model, SupportedModel)
	}

	return nil
}

// Close implements Storer interface
func (d PostgresDB) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

func (PostgresDB) String() string {
	return "postgres database driver"
}
