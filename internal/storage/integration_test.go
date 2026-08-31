//go:build integration

package storage_test

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mikehsu0618/alertsnitch/internal"
	"github.com/mikehsu0618/alertsnitch/internal/metrics"
	"github.com/mikehsu0618/alertsnitch/internal/storage"
	"github.com/mikehsu0618/alertsnitch/internal/storage/sqlstore"
	"github.com/mikehsu0618/alertsnitch/internal/webhook"
)

// These tests run against a real MySQL or Postgres instance selected by
// ALERTSNITCH_BACKEND / ALERTSNITCH_BACKEND_ENDPOINT (see the CI workflow and
// `make bootstrap_local_testing`). They are excluded from the default build.

func connectForTest(t *testing.T) internal.Storer {
	t.Helper()
	driver, err := storage.Connect(storage.Config{
		Backend: os.Getenv("ALERTSNITCH_BACKEND"),
		SQL: sqlstore.Config{
			DSN:                    os.Getenv(internal.DSNVar),
			MaxIdleConns:           1,
			MaxOpenConns:           2,
			MaxConnLifetimeSeconds: 600,
			MaxConnIdleTimeSeconds: 120,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, driver, "database driver is nil?")
	return driver
}

func TestHealthAndReachability(t *testing.T) {
	driver := connectForTest(t)
	defer driver.Close(context.Background())

	checker, ok := driver.(internal.HealthChecker)
	require.True(t, ok, "SQL backend should report health")

	live := checker.CheckLiveness(context.Background())
	assert.True(t, live.Ready, "database should be reachable")

	ready := checker.CheckReadiness(context.Background())
	assert.True(t, ready.Ready, "database should be reachable")
	assert.True(t, ready.Healthy, "model should be supported: %s", ready.Detail)
}

func TestSavingAnAlertWorks(t *testing.T) {
	driver := connectForTest(t)
	defer driver.Close(context.Background())

	b, err := os.ReadFile("../webhook/sample-payload.json")
	require.NoError(t, err)
	data, err := webhook.Parse(b)
	require.NoError(t, err)

	before := testutil.ToFloat64(metrics.AlertsSavedTotal.WithLabelValues(data.Receiver, data.Status))
	require.NoError(t, driver.Save(context.Background(), data, nil))
	assert.Equal(t, before+float64(len(data.Alerts)),
		testutil.ToFloat64(metrics.AlertsSavedTotal.WithLabelValues(data.Receiver, data.Status)),
		"SQL backend must record saved alerts")
}

func TestSavingAFiringAlertWorks(t *testing.T) {
	driver := connectForTest(t)
	defer driver.Close(context.Background())

	b, err := os.ReadFile("../webhook/sample-payload-invalid-ends-at.json")
	require.NoError(t, err)
	data, err := webhook.Parse(b)
	require.NoError(t, err)

	assert.NoError(t, driver.Save(context.Background(), data, nil))
}

func openSQLDBForTest(t *testing.T) *sql.DB {
	t.Helper()
	if os.Getenv("ALERTSNITCH_BACKEND") != "mysql" {
		t.Skip("MySQL-only test")
	}
	db, err := sql.Open("mysql", os.Getenv(internal.DSNVar))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func sampleAlertGroup(t *testing.T) *internal.AlertGroup {
	t.Helper()
	b, err := os.ReadFile("../webhook/sample-payload.json")
	require.NoError(t, err)
	data, err := webhook.Parse(b)
	require.NoError(t, err)
	return data
}

func countLabelKV(t *testing.T, db *sql.DB, key, value string) int {
	t.Helper()
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM LabelKV WHERE LabelKey = ? AND Value = ?`,
		key, value).Scan(&count)
	require.NoError(t, err)
	return count
}

func TestMySQLLabelKVDedup(t *testing.T) {
	db := openSQLDBForTest(t)
	driver := connectForTest(t)
	defer driver.Close(context.Background())

	data := sampleAlertGroup(t)
	const key, value = "severity", "critical"

	require.NoError(t, driver.Save(context.Background(), data, nil))
	countAfterFirst := countLabelKV(t, db, key, value)
	require.Greater(t, countAfterFirst, 0)

	for i := 0; i < 3; i++ {
		require.NoError(t, driver.Save(context.Background(), data, nil))
	}
	assert.Equal(t, countAfterFirst, countLabelKV(t, db, key, value),
		"upsert should reuse one LabelKV row per unique (key, value) pair")
}

func TestMySQLConcurrentSaveSharedLabels(t *testing.T) {
	openSQLDBForTest(t) // skip unless MySQL is configured
	driver := connectForTest(t)
	defer driver.Close(context.Background())

	data := sampleAlertGroup(t)
	const workers = 16

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- driver.Save(context.Background(), data, nil)
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		assert.NoError(t, err)
	}
}
