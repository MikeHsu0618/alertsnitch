package db

import (
	"context"

	"github.com/sirupsen/logrus"
	"gitlab.com/yakshaving.art/alertsnitch/internal"
)

// NullDB A database that does nothing
type NullDB struct{}

// Save implements Storer interface
func (NullDB) Save(ctx context.Context, data *internal.AlertGroup) error {
	logrus.Debugf("save alert %#v", data)
	return nil
}

// Ping implements Storer interface
func (NullDB) Ping() error {
	logrus.Debug("pong")
	return nil
}

// CheckModel implements Storer interface
func (NullDB) CheckModel() error {
	logrus.Debug("check model")
	return nil
}

// Close implements Storer interface
func (NullDB) Close() error {
	return nil
}

func (NullDB) String() string {
	return "null database driver"
}
