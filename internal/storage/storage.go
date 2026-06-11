// Package storage wires the available storage backends behind a small registry
// and a single typed Connect entry point. Adding a backend means implementing
// internal.Storer and registering a factory — no other package needs to change.
package storage

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mikehsu0618/alertsnitch/internal"
	"github.com/mikehsu0618/alertsnitch/internal/storage/loki"
	"github.com/mikehsu0618/alertsnitch/internal/storage/null"
	"github.com/mikehsu0618/alertsnitch/internal/storage/sqlstore"
)

// Config is the aggregated, typed configuration for all backends. Only the
// sub-config matching Backend is consulted.
type Config struct {
	Backend string
	SQL     sqlstore.Config
	Loki    loki.Config
}

// Factory builds a Storer from the aggregated configuration.
type Factory func(Config) (internal.Storer, error)

var registry = map[string]Factory{
	"mysql": func(c Config) (internal.Storer, error) {
		return wrap(sqlstore.ConnectMySQL(c.SQL))
	},
	"postgres": func(c Config) (internal.Storer, error) {
		return wrap(sqlstore.ConnectPostgres(c.SQL))
	},
	"loki": func(c Config) (internal.Storer, error) {
		return wrap(loki.New(c.Loki))
	},
	"null": func(Config) (internal.Storer, error) {
		return null.Store{}, nil
	},
}

// Register adds or overrides a backend factory, allowing out-of-tree backends
// to plug in without modifying this package.
func Register(name string, f Factory) { registry[name] = f }

// SupportedBackends returns the sorted list of registered backend names.
func SupportedBackends() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Connect builds the configured storage backend.
func Connect(cfg Config) (internal.Storer, error) {
	f, ok := registry[cfg.Backend]
	if !ok {
		return nil, fmt.Errorf("invalid backend %q, supported backends are: %s",
			cfg.Backend, strings.Join(SupportedBackends(), ", "))
	}
	return f(cfg)
}

// wrap adapts a concrete (*T, error) constructor result to (internal.Storer,
// error), avoiding the typed-nil-in-interface pitfall on error.
func wrap[T internal.Storer](s T, err error) (internal.Storer, error) {
	if err != nil {
		return nil, err
	}
	return s, nil
}
