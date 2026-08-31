package sqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"sync/atomic"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	sql.Register("sqlstoretest", &stubDriver{})
}

type stubDriver struct{}

func (*stubDriver) Open(string) (driver.Conn, error) {
	return &stubConn{}, nil
}

type stubConn struct{}

var testCommitHook atomic.Value // stores func() error

func (c *stubConn) commitError() error {
	if v := testCommitHook.Load(); v != nil {
		if fn, ok := v.(func() error); ok && fn != nil {
			return fn()
		}
	}
	return nil
}

func (c *stubConn) Prepare(string) (driver.Stmt, error) { return stubStmt{}, nil }
func (c *stubConn) Close() error                        { return nil }
func (c *stubConn) Begin() (driver.Tx, error) {
	return &stubTx{conn: c}, nil
}

func (c *stubConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &stubTx{conn: c}, nil
}

type stubTx struct {
	conn *stubConn
}

func (tx *stubTx) Commit() error   { return tx.conn.commitError() }
func (tx *stubTx) Rollback() error { return nil }

type stubStmt struct{}

func (stubStmt) Close() error                               { return nil }
func (stubStmt) NumInput() int                              { return -1 }
func (stubStmt) Exec([]driver.Value) (driver.Result, error) { return stubResult{}, nil }
func (stubStmt) Query([]driver.Value) (driver.Rows, error)  { return nil, nil }
func (stubStmt) ExecContext(context.Context, []driver.NamedValue) (driver.Result, error) {
	return stubResult{}, nil
}

type stubResult struct{}

func (stubResult) LastInsertId() (int64, error) { return 0, nil }
func (stubResult) RowsAffected() (int64, error) { return 0, nil }

func openStubDB(t *testing.T) base {
	t.Helper()
	db, err := sql.Open("sqlstoretest", "")
	require.NoError(t, err)
	t.Cleanup(func() {
		testCommitHook.Store((func() error)(nil))
		_ = db.Close()
	})
	return base{db: db, name: "stub"}
}

func TestUnitOfWork_SuccessFirstAttempt(t *testing.T) {
	b := openStubDB(t)
	calls := 0
	err := b.unitOfWork(context.Background(), func(*sql.Tx) error {
		calls++
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestUnitOfWork_RetriesDeadlockThenSucceeds(t *testing.T) {
	b := openStubDB(t)
	attempts := 0
	err := b.unitOfWork(context.Background(), func(*sql.Tx) error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("failed execution: %w", &mysql.MySQLError{Number: 1213})
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 3, attempts)
}

func TestUnitOfWork_StopsRetryingAfterMaxAttempts(t *testing.T) {
	b := openStubDB(t)
	attempts := 0
	err := b.unitOfWork(context.Background(), func(*sql.Tx) error {
		attempts++
		return fmt.Errorf("failed execution: %w", &mysql.MySQLError{Number: 1213})
	})
	require.Error(t, err)
	assert.True(t, isMySQLDeadlock(err))
	assert.Equal(t, mysqlDeadlockMaxRetries, attempts)
}

func TestUnitOfWork_DoesNotRetryNonDeadlockError(t *testing.T) {
	b := openStubDB(t)
	attempts := 0
	err := b.unitOfWork(context.Background(), func(*sql.Tx) error {
		attempts++
		return fmt.Errorf("failed execution: %w", io.EOF)
	})
	require.Error(t, err)
	assert.Equal(t, 1, attempts)
}

func TestUnitOfWork_DoesNotRetryWhenContextCanceled(t *testing.T) {
	b := openStubDB(t)
	ctx, cancel := context.WithCancel(context.Background())

	attempts := 0
	err := b.unitOfWork(ctx, func(*sql.Tx) error {
		attempts++
		cancel()
		return fmt.Errorf("failed execution: %w", &mysql.MySQLError{Number: 1213})
	})
	require.Error(t, err)
	assert.Equal(t, 1, attempts)
}

func TestUnitOfWork_RetriesDeadlockOnCommit(t *testing.T) {
	b := openStubDB(t)

	commits := 0
	testCommitHook.Store(func() error {
		commits++
		if commits < 2 {
			return &mysql.MySQLError{Number: 1213}
		}
		return nil
	})

	err := b.unitOfWork(context.Background(), func(*sql.Tx) error { return nil })
	require.NoError(t, err)
	assert.Equal(t, 2, commits)
}

func TestOpen_RejectsEmptyDSN(t *testing.T) {
	_, err := open("sqlstoretest", Config{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty DSN")
}

func TestOpen_AppliesConnIdleTime(t *testing.T) {
	t.Run("positive idle time", func(t *testing.T) {
		b, err := open("sqlstoretest", Config{
			DSN:                    "test",
			MaxIdleConns:           1,
			MaxOpenConns:           2,
			MaxConnLifetimeSeconds: 600,
			MaxConnIdleTimeSeconds: 120,
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = b.db.Close() })
	})

	t.Run("zero disables idle timeout", func(t *testing.T) {
		b, err := open("sqlstoretest", Config{
			DSN:                    "test",
			MaxConnIdleTimeSeconds: 0,
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = b.db.Close() })
	})
}
