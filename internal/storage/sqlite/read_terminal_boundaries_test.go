package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	driverSQLite "modernc.org/sqlite"
)

// This observer delegates every SQL operation and error to the pinned driver.
// It cancels the real job only after the final participants iterator reaches
// EOF and its driver Close succeeds. No replacement Tx error is supplied.
type terminalRowsProbe struct {
	armed       atomic.Bool
	closesAtEOF atomic.Int32
	openConns   atomic.Int32
	closeConns  atomic.Int32
	readOnly    atomic.Int32
	cancel      context.CancelFunc
}
type terminalObservedDriver struct{ probe *terminalRowsProbe }
type terminalObservedConn struct {
	driver.Conn
	probe *terminalRowsProbe
}
type terminalObservedRows struct {
	driver.Rows
	probe *terminalRowsProbe
	eof   bool
}

var terminalDriverNumber atomic.Uint32

func (d *terminalObservedDriver) Open(name string) (driver.Conn, error) {
	c, err := new(driverSQLite.Driver).Open(name)
	if err != nil {
		return nil, err
	}
	d.probe.openConns.Add(1)
	return &terminalObservedConn{Conn: c, probe: d.probe}, nil
}
func (c *terminalObservedConn) Close() error {
	err := c.Conn.Close()
	c.probe.closeConns.Add(1)
	return err
}
func (c *terminalObservedConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if options.ReadOnly {
		c.probe.readOnly.Add(1)
	}
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
}
func (c *terminalObservedConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *terminalObservedConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
	if err == nil && q == "SELECT id,included,body_json FROM participants WHERE collection_id=? ORDER BY position" {
		return &terminalObservedRows{Rows: rows, probe: c.probe}, nil
	}
	return rows, err
}
func (c *terminalObservedConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}
func (c *terminalObservedConn) IsValid() bool {
	return c.Conn.(driver.Validator).IsValid()
}
func (r *terminalObservedRows) Next(values []driver.Value) error {
	err := r.Rows.Next(values)
	r.eof = errors.Is(err, io.EOF)
	return err
}
func (r *terminalObservedRows) Close() error {
	err := r.Rows.Close()
	if err == nil && r.eof {
		r.probe.closesAtEOF.Add(1)
		if r.probe.armed.CompareAndSwap(true, false) {
			r.probe.cancel()
		}
	}
	return err
}

func TestReadCollectionCancellationAfterSuccessfulEOFNeverPublishesBeforeCommit(t *testing.T) {
	for _, cancelAtEOF := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelAtEOF), func(t *testing.T) {
			store := migratedTestStore(t)
			service := productService(t, store, new(productEntropy), func() time.Time { return storageNow }, "session-one", nil)
			created, err := service.CreateCollection(context.Background(), productRequest(storageNow, rl.ImmediateMode))
			if err != nil {
				t.Fatal(err)
			}
			expected, err := store.ReadCollection(context.Background(), created.CollectionID)
			if err != nil {
				t.Fatal(err)
			}
			before := corruptionDurableSnapshot(t, store)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &terminalRowsProbe{cancel: cancel}
			probe.armed.Store(cancelAtEOF)
			name := fmt.Sprintf("jackpot-terminal-observer-%d", terminalDriverNumber.Add(1))
			sql.Register(name, &terminalObservedDriver{probe: probe})
			query := url.Values{"_txlock": {"immediate"}}
			for _, pragma := range []string{"foreign_keys(1)", "journal_mode(WAL)", "synchronous(FULL)", "busy_timeout(1000)"} {
				query.Add("_pragma", pragma)
			}
			store.db, err = sql.Open(name, fileURI(store.path)+"?"+query.Encode())
			if err != nil {
				t.Fatal(err)
			}
			store.db.SetMaxOpenConns(1)
			store.db.SetMaxIdleConns(1)

			actual, failure := store.ReadCollection(ctx, created.CollectionID)
			if probe.closesAtEOF.Load() != 1 || probe.readOnly.Load() != 1 {
				t.Fatal("fixture did not complete the read-only iterator", probe.closesAtEOF.Load(), probe.readOnly.Load())
			}
			if cancelAtEOF {
				if !errors.Is(failure, context.Canceled) && !errors.Is(failure, sql.ErrTxDone) {
					t.Fatal("commit lost the actual cancellation cause", failure)
				}
				if !reflect.DeepEqual(actual, rl.FrozenCollection{}) || ctx.Err() != context.Canceled {
					t.Fatal("uncommitted read published its completed payload", actual, ctx.Err())
				}
			} else if failure != nil || !reflect.DeepEqual(actual, expected) {
				t.Fatal("passive observer changed the healthy result", actual, expected, failure)
			}
			again, err := store.ReadCollection(context.Background(), created.CollectionID)
			if err != nil || !reflect.DeepEqual(again, expected) || before != corruptionDurableSnapshot(t, store) {
				t.Fatal("failed read changed persistence or prevented retry", again, err)
			}
			assertNoHandles(t, store)
			if err := store.Close(); err != nil || probe.openConns.Load() != probe.closeConns.Load() {
				t.Fatal("observer retained a native connection", err, probe.openConns.Load(), probe.closeConns.Load())
			}
			if err := os.Rename(store.path, store.path+".exclusive"); err != nil {
				t.Fatal("closed read retained the database file", err)
			}
		})
	}
}
