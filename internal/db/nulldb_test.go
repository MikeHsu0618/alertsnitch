package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.com/yakshaving.art/alertsnitch/internal/db"
)

func TestNullDBObject(t *testing.T) {
	a := assert.New(t)

	n := db.NullDB{}
	a.Equal(n.String(), "null database driver")

	a.Nil(n.Save(context.Background(), nil))
	a.NoError(n.Ping())
	a.NoError(n.CheckModel())
}
