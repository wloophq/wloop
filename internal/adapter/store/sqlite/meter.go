package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	modernc "modernc.org/sqlite"

	"github.com/wloophq/wloop/internal/meter"
)

// driverName is the database/sql name of the counting driver.
const driverName = "loop-sqlite"

func init() { sql.Register(driverName, meteredDriver{}) }

type meteredDriver struct{}

func (meteredDriver) Open(dsn string) (driver.Conn, error) {
	c, err := connector(dsn)
	if err != nil {
		return nil, err
	}
	return c.Connect(context.Background())
}

func (meteredDriver) OpenConnector(dsn string) (driver.Connector, error) { return connector(dsn) }

type meteredConnector struct{ base driver.Connector }

// connector is the only way the package opens a handle: modernc's
// NewConnector wrapped so every statement adds SQL and every transaction Tx.
func connector(dsn string) (driver.Connector, error) {
	base, err := modernc.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	return meteredConnector{base}, nil
}

func (c meteredConnector) Driver() driver.Driver { return meteredDriver{} }

func (c meteredConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	inner, ok := conn.(innerConn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("sqlite: driver conn %T lacks the context interfaces", conn)
	}
	return &meteredConn{inner: inner}, nil
}

type innerConn interface {
	driver.Conn
	driver.ExecerContext
	driver.QueryerContext
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

type innerRows interface {
	driver.Rows
	driver.RowsColumnTypeDatabaseTypeName
	driver.RowsColumnTypeLength
	driver.RowsColumnTypeNullable
	driver.RowsColumnTypePrecisionScale
	driver.RowsColumnTypeScanType
}

// meteredConn is used by one goroutine at a time, as database/sql guarantees.
type meteredConn struct {
	inner innerConn
	wrote bool
}

// Unwrap returns modernc's conn, for a caller of (*sql.Conn).Raw.
func (c *meteredConn) Unwrap() driver.Conn { return c.inner }

func (c *meteredConn) Close() error                           { return c.inner.Close() }
func (c *meteredConn) Ping(ctx context.Context) error         { return c.inner.Ping(ctx) }
func (c *meteredConn) ResetSession(ctx context.Context) error { return c.inner.ResetSession(ctx) }
func (c *meteredConn) IsValid() bool                          { return c.inner.IsValid() }

func (c *meteredConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *meteredConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	s, err := c.inner.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	inner, ok := s.(innerStmt)
	if !ok {
		s.Close()
		return nil, fmt.Errorf("sqlite: driver stmt %T lacks the context interfaces", s)
	}
	return &meteredStmt{inner: inner, conn: c, query: query}, nil
}

func (c *meteredConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	start := time.Now()
	r, err := c.inner.ExecContext(ctx, query, args)
	if errors.Is(err, driver.ErrSkip) {
		return r, err
	}
	c.exec(ctx, query, start, err)
	return r, err
}

func (c *meteredConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	start := time.Now()
	r, err := c.inner.QueryContext(ctx, query, args)
	if errors.Is(err, driver.ErrSkip) {
		return r, err
	}
	return wrapRows(ctx, query, start, r, err)
}

func (c *meteredConn) exec(ctx context.Context, query string, start time.Time, err error) {
	c.wrote = true
	meter.Add(ctx, meter.Sample{
		Res:    meter.SQL,
		Op:     "exec",
		Dur:    time.Since(start),
		Err:    failed(ctx, err),
		Detail: detail(ctx, query),
	})
}

func (c *meteredConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *meteredConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.inner.BeginTx(ctx, opts)
	if err != nil {
		meter.Add(ctx, meter.Sample{Res: meter.Tx, Op: "read", Err: failed(ctx, err)})
		return nil, err
	}
	c.wrote = false
	return &meteredTx{inner: tx, conn: c, ctx: ctx, start: time.Now()}, nil
}

type meteredTx struct {
	inner driver.Tx
	conn  *meteredConn
	ctx   context.Context
	start time.Time
}

func (t *meteredTx) Commit() error   { return t.end(t.inner.Commit()) }
func (t *meteredTx) Rollback() error { return t.end(t.inner.Rollback()) }

func (t *meteredTx) end(err error) error {
	op := "read"
	if t.conn.wrote {
		op = "write"
	}
	meter.Add(t.ctx, meter.Sample{Res: meter.Tx, Op: op, Dur: time.Since(t.start), Err: failed(t.ctx, err)})
	return err
}

type innerStmt interface {
	driver.Stmt
	driver.StmtExecContext
	driver.StmtQueryContext
}

type meteredStmt struct {
	inner innerStmt
	conn  *meteredConn
	query string
}

func (s *meteredStmt) Close() error  { return s.inner.Close() }
func (s *meteredStmt) NumInput() int { return s.inner.NumInput() }

func (s *meteredStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), named(args))
}

func (s *meteredStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), named(args))
}

func (s *meteredStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	start := time.Now()
	r, err := s.inner.ExecContext(ctx, args)
	s.conn.exec(ctx, s.query, start, err)
	return r, err
}

func (s *meteredStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	start := time.Now()
	r, err := s.inner.QueryContext(ctx, args)
	return wrapRows(ctx, s.query, start, r, err)
}

func named(args []driver.Value) []driver.NamedValue {
	nv := make([]driver.NamedValue, len(args))
	for i, v := range args {
		nv[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return nv
}

func wrapRows(ctx context.Context, query string, start time.Time, r driver.Rows, err error) (driver.Rows, error) {
	if err != nil {
		meter.Add(ctx, meter.Sample{
			Res:    meter.SQL,
			Op:     "query",
			Dur:    time.Since(start),
			Err:    failed(ctx, err),
			Detail: detail(ctx, query),
		})
		return nil, err
	}
	inner, ok := r.(innerRows)
	if !ok {
		r.Close()
		return nil, fmt.Errorf("sqlite: driver rows %T lacks the column type interfaces", r)
	}
	return &meteredRows{inner: inner, ctx: ctx, query: query, start: start}, nil
}

// meteredRows adds its query's sample on Close: Dur from the call to Close,
// Rows the rows read.
type meteredRows struct {
	inner  innerRows
	ctx    context.Context
	query  string
	start  time.Time
	rows   int64
	err    error
	closed bool
}

func (r *meteredRows) Columns() []string { return r.inner.Columns() }

func (r *meteredRows) Next(dest []driver.Value) error {
	err := r.inner.Next(dest)
	switch {
	case err == nil:
		r.rows++
	case !errors.Is(err, io.EOF):
		r.err = err
	}
	return err
}

func (r *meteredRows) Close() error {
	err := r.inner.Close()
	if r.closed {
		return err
	}
	r.closed = true
	if r.err == nil {
		r.err = err
	}
	meter.Add(r.ctx, meter.Sample{
		Res:    meter.SQL,
		Op:     "query",
		Dur:    time.Since(r.start),
		Err:    failed(r.ctx, r.err),
		Rows:   r.rows,
		Detail: detail(r.ctx, r.query),
	})
	return err
}

func (r *meteredRows) ColumnTypeDatabaseTypeName(i int) string {
	return r.inner.ColumnTypeDatabaseTypeName(i)
}

func (r *meteredRows) ColumnTypeLength(i int) (int64, bool) { return r.inner.ColumnTypeLength(i) }

func (r *meteredRows) ColumnTypeNullable(i int) (bool, bool) { return r.inner.ColumnTypeNullable(i) }

func (r *meteredRows) ColumnTypePrecisionScale(i int) (int64, int64, bool) {
	return r.inner.ColumnTypePrecisionScale(i)
}

func (r *meteredRows) ColumnTypeScanType(i int) reflect.Type { return r.inner.ColumnTypeScanType(i) }

func failed(ctx context.Context, err error) bool { return err != nil && ctx.Err() == nil }

const detailMax = 300

func detail(ctx context.Context, query string) string {
	if !meter.Tracing(ctx) {
		return ""
	}
	d := strings.Join(strings.Fields(query), " ")
	if len(d) > detailMax {
		d = d[:detailMax]
	}
	return d
}
