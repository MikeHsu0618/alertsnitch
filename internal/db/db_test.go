//go:build integration
// +build integration

package db_test

import (
	"context"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"

	"github.com/stretchr/testify/assert"

	"github.com/mikehsu0618/alertsnitch/internal"
	"github.com/mikehsu0618/alertsnitch/internal/db"
	"github.com/mikehsu0618/alertsnitch/internal/webhook"
)

func TestPingingDatabaseWorks(t *testing.T) {
	backend := os.Getenv("ALERTSNITCH_BACKEND")

	a := assert.New(t)
	driver, err := db.Connect(backend, connectionArgs())
	a.NoError(err)
	a.NotNilf(driver, "database driver is nil?")
	a.NoErrorf(driver.Ping(), "failed to ping database")
	a.NoErrorf(driver.CheckModel(), "failed to check the model")
}

func TestSavingAnAlertWorks(t *testing.T) {
	a := assert.New(t)

	b, err := os.ReadFile("../webhook/sample-payload.json")
	a.NoError(err)

	data, err := webhook.Parse(b)
	a.NoError(err)

	backend := os.Getenv("ALERTSNITCH_BACKEND")

	driver, err := db.Connect(backend, connectionArgs())
	a.NoError(err)

	a.NoError(driver.Save(context.Background(), data))
}

func TestSavingAFiringAlertWorks(t *testing.T) {
	a := assert.New(t)

	b, err := os.ReadFile("../webhook/sample-payload-invalid-ends-at.json")
	a.NoError(err)

	data, err := webhook.Parse(b)
	a.NoError(err)

	backend := os.Getenv("ALERTSNITCH_BACKEND")
	driver, err := db.Connect(backend, connectionArgs())
	a.NoError(err)

	a.NoError(driver.Save(context.Background(), data))
}

func connectionArgs() db.ConnectionArgs {
	return db.ConnectionArgs{
		DSN:                    os.Getenv(internal.DSNVar),
		MaxIdleConns:           1,
		MaxOpenConns:           2,
		MaxConnLifetimeSeconds: 600,
	}
}
