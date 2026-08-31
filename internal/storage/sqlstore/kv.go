package sqlstore

import (
	"cmp"
	"context"
	"crypto/md5" //nolint:gosec // KvHash dedup digest; must match database/*/0.2.0-labelkv.sql
	"database/sql"
	"encoding/hex"
	"errors"
	"slices"

	"github.com/go-sql-driver/mysql"
)

const mysqlDeadlockMaxRetries = 5

// kvPairHash is a fixed-width digest of key || ASCII SOH || value for unique indexes (MySQL index size limit; PG rejects NUL in text).
func kvPairHash(k, v string) string {
	sum := md5.Sum([]byte(k + "\x01" + v)) //nolint:gosec // not for security; fixed-width unique-index key
	return hex.EncodeToString(sum[:])
}

func isMySQLDeadlock(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1213
}

type kvPair struct {
	key   string
	value string
	hash  string
}

// uniqueSortedKVPairs deduplicates (k,v) pairs across maps and returns them
// sorted by KvHash so concurrent transactions acquire LabelKV/AnnotationKV
// locks in a stable order (avoids MySQL deadlock 1213 from Go map iteration).
func uniqueSortedKVPairs(maps ...map[string]string) []kvPair {
	seen := make(map[string]struct{})
	var pairs []kvPair
	for _, m := range maps {
		for k, v := range m {
			h := kvPairHash(k, v)
			if _, ok := seen[h]; ok {
				continue
			}
			seen[h] = struct{}{}
			pairs = append(pairs, kvPair{key: k, value: v, hash: h})
		}
	}
	slices.SortFunc(pairs, func(a, b kvPair) int {
		return cmp.Compare(a.hash, b.hash)
	})
	return pairs
}

func resolveLabelKVCache(ctx context.Context, tx *sql.Tx, maps ...map[string]string) (map[string]int64, error) {
	pairs := uniqueSortedKVPairs(maps...)
	cache := make(map[string]int64, len(pairs))
	for _, p := range pairs {
		id, err := mysqlGetLabelKVID(ctx, tx, p.key, p.value)
		if err != nil {
			return nil, err
		}
		cache[p.hash] = id
	}
	return cache, nil
}

func resolveAnnotationKVCache(ctx context.Context, tx *sql.Tx, maps ...map[string]string) (map[string]int64, error) {
	pairs := uniqueSortedKVPairs(maps...)
	cache := make(map[string]int64, len(pairs))
	for _, p := range pairs {
		id, err := mysqlGetAnnotationKVID(ctx, tx, p.key, p.value)
		if err != nil {
			return nil, err
		}
		cache[p.hash] = id
	}
	return cache, nil
}

func kvIDFromCache(cache map[string]int64, k, v string) int64 {
	return cache[kvPairHash(k, v)]
}

func postgresGetLabelKVID(ctx context.Context, tx *sql.Tx, k, v string) (int64, error) {
	h := kvPairHash(k, v)
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO LabelKV (LabelKey, Value, KvHash) VALUES ($1, $2, $3)
		ON CONFLICT (KvHash) DO UPDATE SET LabelKey = LabelKV.LabelKey
		RETURNING ID`, k, v, h).Scan(&id)
	return id, err
}

func postgresGetAnnotationKVID(ctx context.Context, tx *sql.Tx, k, v string) (int64, error) {
	h := kvPairHash(k, v)
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO AnnotationKV (AnnotationKey, Value, KvHash) VALUES ($1, $2, $3)
		ON CONFLICT (KvHash) DO UPDATE SET AnnotationKey = AnnotationKV.AnnotationKey
		RETURNING ID`, k, v, h).Scan(&id)
	return id, err
}

// mysqlUpsertID inserts a row and returns its ID even on unique-key conflict.
// INSERT IGNORE + SELECT under REPEATABLE READ can miss a row committed by a
// concurrent transaction (sql.ErrNoRows); LAST_INSERT_ID sidesteps that.
func mysqlUpsertID(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	r, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func mysqlGetLabelKVID(ctx context.Context, tx *sql.Tx, k, v string) (int64, error) {
	h := kvPairHash(k, v)
	return mysqlUpsertID(ctx, tx, `
		INSERT INTO LabelKV (LabelKey, Value, KvHash) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE ID = LAST_INSERT_ID(ID)`, k, v, h)
}

func mysqlGetAnnotationKVID(ctx context.Context, tx *sql.Tx, k, v string) (int64, error) {
	h := kvPairHash(k, v)
	return mysqlUpsertID(ctx, tx, `
		INSERT INTO AnnotationKV (AnnotationKey, Value, KvHash) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE ID = LAST_INSERT_ID(ID)`, k, v, h)
}

func postgresGetReceiverID(ctx context.Context, tx *sql.Tx, receiver string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO AlertGroupReceiver (Receiver) VALUES ($1)
		ON CONFLICT (Receiver) DO UPDATE SET Receiver = AlertGroupReceiver.Receiver
		RETURNING ID`, receiver).Scan(&id)
	return id, err
}

func postgresGetExternalURLID(ctx context.Context, tx *sql.Tx, externalURL string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO AlertGroupExternalURL (ExternalURL) VALUES ($1)
		ON CONFLICT (ExternalURL) DO UPDATE SET ExternalURL = AlertGroupExternalURL.ExternalURL
		RETURNING ID`, externalURL).Scan(&id)
	return id, err
}

func postgresGetKeyID(ctx context.Context, tx *sql.Tx, groupKey string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO AlertGroupKey (GroupKey) VALUES ($1)
		ON CONFLICT (GroupKey) DO UPDATE SET GroupKey = AlertGroupKey.GroupKey
		RETURNING ID`, groupKey).Scan(&id)
	return id, err
}

func mysqlGetReceiverID(ctx context.Context, tx *sql.Tx, receiver string) (int64, error) {
	return mysqlUpsertID(ctx, tx, `
		INSERT INTO AlertGroupReceiver (Receiver) VALUES (?)
		ON DUPLICATE KEY UPDATE ID = LAST_INSERT_ID(ID)`, receiver)
}

func mysqlGetExternalURLID(ctx context.Context, tx *sql.Tx, externalURL string) (int64, error) {
	return mysqlUpsertID(ctx, tx, `
		INSERT INTO AlertGroupExternalURL (ExternalURL) VALUES (?)
		ON DUPLICATE KEY UPDATE ID = LAST_INSERT_ID(ID)`, externalURL)
}

func mysqlGetKeyID(ctx context.Context, tx *sql.Tx, groupKey string) (int64, error) {
	return mysqlUpsertID(ctx, tx, `
		INSERT INTO AlertGroupKey (GroupKey) VALUES (?)
		ON DUPLICATE KEY UPDATE ID = LAST_INSERT_ID(ID)`, groupKey)
}
