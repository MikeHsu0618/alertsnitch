package db

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
)

// kvPairHash is a fixed-width digest of key || ASCII SOH || value for unique indexes (MySQL index size limit; PG rejects NUL in text).
func kvPairHash(k, v string) string {
	sum := md5.Sum([]byte(k + "\x01" + v))
	return hex.EncodeToString(sum[:])
}

func postgresGetLabelKVID(tx *sql.Tx, k, v string) (int64, error) {
	h := kvPairHash(k, v)
	var id int64
	err := tx.QueryRow(`
		INSERT INTO LabelKV (LabelKey, Value, KvHash) VALUES ($1, $2, $3)
		ON CONFLICT (KvHash) DO UPDATE SET LabelKey = LabelKV.LabelKey
		RETURNING ID`, k, v, h).Scan(&id)
	return id, err
}

func postgresGetAnnotationKVID(tx *sql.Tx, k, v string) (int64, error) {
	h := kvPairHash(k, v)
	var id int64
	err := tx.QueryRow(`
		INSERT INTO AnnotationKV (AnnotationKey, Value, KvHash) VALUES ($1, $2, $3)
		ON CONFLICT (KvHash) DO UPDATE SET AnnotationKey = AnnotationKV.AnnotationKey
		RETURNING ID`, k, v, h).Scan(&id)
	return id, err
}

func mysqlGetLabelKVID(tx *sql.Tx, k, v string) (int64, error) {
	h := kvPairHash(k, v)
	if _, err := tx.Exec(`INSERT IGNORE INTO LabelKV (LabelKey, Value, KvHash) VALUES (?, ?, ?)`, k, v, h); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRow(`SELECT ID FROM LabelKV WHERE KvHash = ?`, h).Scan(&id)
	return id, err
}

func mysqlGetAnnotationKVID(tx *sql.Tx, k, v string) (int64, error) {
	h := kvPairHash(k, v)
	if _, err := tx.Exec(`INSERT IGNORE INTO AnnotationKV (AnnotationKey, Value, KvHash) VALUES (?, ?, ?)`, k, v, h); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRow(`SELECT ID FROM AnnotationKV WHERE KvHash = ?`, h).Scan(&id)
	return id, err
}
