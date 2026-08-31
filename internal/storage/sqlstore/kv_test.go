package sqlstore

import (
	"fmt"
	"slices"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsMySQLDeadlock(t *testing.T) {
	a := assert.New(t)
	a.True(isMySQLDeadlock(&mysql.MySQLError{Number: 1213}))
	a.False(isMySQLDeadlock(&mysql.MySQLError{Number: 1062}))
	a.False(isMySQLDeadlock(fmt.Errorf("other")))
	a.True(isMySQLDeadlock(fmt.Errorf("failed execution: %w", &mysql.MySQLError{Number: 1213})))
}

func TestUniqueSortedKVPairs(t *testing.T) {
	pairs := uniqueSortedKVPairs(
		map[string]string{"b": "2", "a": "1"},
		map[string]string{"a": "1", "c": "3"},
	)
	require.Len(t, pairs, 3)

	hashes := make([]string, len(pairs))
	for i, p := range pairs {
		hashes[i] = p.hash
		assert.Equal(t, kvPairHash(p.key, p.value), p.hash)
	}
	assert.True(t, slices.IsSorted(hashes))
}

func TestUniqueSortedKVPairs_Empty(t *testing.T) {
	assert.Empty(t, uniqueSortedKVPairs())
	assert.Empty(t, uniqueSortedKVPairs(map[string]string{}))
	assert.Empty(t, uniqueSortedKVPairs(nil, map[string]string{}))
}

func TestUniqueSortedKVPairs_DeduplicatesAcrossMaps(t *testing.T) {
	group := map[string]string{"alertname": "DiskFull", "severity": "critical"}
	common := map[string]string{"alertname": "DiskFull", "instance": "10.0.0.1:9100"}
	pairs := uniqueSortedKVPairs(group, common)
	require.Len(t, pairs, 3)

	byKey := make(map[string]string, len(pairs))
	for _, p := range pairs {
		byKey[p.key] = p.value
	}
	assert.Equal(t, "DiskFull", byKey["alertname"])
	assert.Equal(t, "critical", byKey["severity"])
	assert.Equal(t, "10.0.0.1:9100", byKey["instance"])
}

func TestKvIDFromCache(t *testing.T) {
	cache := map[string]int64{kvPairHash("severity", "critical"): 42}
	assert.Equal(t, int64(42), kvIDFromCache(cache, "severity", "critical"))
	assert.Equal(t, int64(0), kvIDFromCache(cache, "missing", "x"))
}
