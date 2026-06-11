package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mikehsu0618/alertsnitch/internal"
)

func TestConnect_NullBackend(t *testing.T) {
	s, err := Connect(Config{Backend: "null"})
	require.NoError(t, err)
	assert.NotNil(t, s)
}

func TestConnect_UnknownBackend(t *testing.T) {
	_, err := Connect(Config{Backend: "does-not-exist"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "supported backends are")
}

func TestSupportedBackends_IncludesDefaults(t *testing.T) {
	backends := SupportedBackends()
	assert.Subset(t, backends, []string{"loki", "mysql", "null", "postgres"})
	// Sorted output.
	assert.Equal(t, "loki", backends[0])
}

func TestRegister_AddsBackend(t *testing.T) {
	Register("custom-test-backend", func(Config) (internal.Storer, error) {
		return stubStorer{}, nil
	})
	t.Cleanup(func() { delete(registry, "custom-test-backend") })

	s, err := Connect(Config{Backend: "custom-test-backend"})
	require.NoError(t, err)
	assert.NotNil(t, s)
}

type stubStorer struct{}

func (stubStorer) Save(context.Context, *internal.AlertGroup, map[string]string) error { return nil }
func (stubStorer) Close(context.Context) error                                         { return nil }
