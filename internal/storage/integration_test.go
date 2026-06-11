//go:build integration

package storage_test

import (
	"context"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mikehsu0618/alertsnitch/internal"
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

	h := checker.CheckHealth(context.Background())
	assert.True(t, h.Ready, "database should be reachable")
	assert.True(t, h.Healthy, "model should be supported: %s", h.Detail)
}

func TestSavingAnAlertWorks(t *testing.T) {
	driver := connectForTest(t)
	defer driver.Close(context.Background())

	b, err := os.ReadFile("../webhook/sample-payload.json")
	require.NoError(t, err)
	data, err := webhook.Parse(b)
	require.NoError(t, err)

	assert.NoError(t, driver.Save(context.Background(), data, nil))
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
