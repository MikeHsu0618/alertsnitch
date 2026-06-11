// Package null provides a no-op storage backend that discards alerts. It is
// useful for debugging the webhook path without a real database.
package null

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/mikehsu0618/alertsnitch/internal"
)

// Store is a backend that does nothing.
type Store struct{}

// Save logs the alert at debug level and discards it.
func (Store) Save(_ context.Context, data *internal.AlertGroup, _ map[string]string) error {
	logrus.Debugf("save alert %#v", data)
	return nil
}

// CheckHealth always reports ready and healthy.
func (Store) CheckHealth(_ context.Context) internal.Health {
	return internal.Health{Ready: true, Healthy: true}
}

// Close is a no-op.
func (Store) Close(_ context.Context) error { return nil }

func (Store) String() string { return "null database driver" }
