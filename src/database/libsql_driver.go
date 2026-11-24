package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	libsql "github.com/tursodatabase/libsql-client-go/libsql"
)

const mappedLibSQLDriver = "citylist-libsql"

func init() {
	sql.Register(mappedLibSQLDriver, prefixDriver{base: libsql.Driver{}})
}

type prefixDriver struct{ base driver.Driver }

func (d prefixDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return prefixConn{Conn: conn}, nil
}

type prefixConn struct{ driver.Conn }

func (c prefixConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(prefixDatabaseSQL(query))
	if err != nil {
		return nil, err
	}
	return prefixStmt{Stmt: stmt}, nil
}

func (c prefixConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if conn, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err := conn.PrepareContext(ctx, prefixDatabaseSQL(query))
		if err != nil {
			return nil, err
		}
		return prefixStmt{Stmt: stmt}, nil
	}
	return c.Prepare(query)
}

func (c prefixConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if conn, ok := c.Conn.(driver.ConnBeginTx); ok {
		return conn.BeginTx(ctx, opts)
	}
	if opts.ReadOnly || opts.Isolation != driver.IsolationLevel(sql.LevelDefault) {
		return nil, errors.New("libSQL driver does not support requested transaction options")
	}
	return nil, errors.New("libSQL connection does not support transaction options")
}

func (c prefixConn) Ping(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.Pinger); ok {
		return conn.Ping(ctx)
	}
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return errors.New("libSQL connection does not support ping or context queries")
	}
	rows, err := queryer.QueryContext(ctx, "SELECT 1", nil)
	if err != nil {
		return err
	}
	return rows.Close()
}

func (c prefixConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if conn, ok := c.Conn.(driver.ExecerContext); ok {
		return conn.ExecContext(ctx, prefixDatabaseSQL(query), args)
	}
	return nil, driver.ErrSkip
}

func (c prefixConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if conn, ok := c.Conn.(driver.QueryerContext); ok {
		return conn.QueryContext(ctx, prefixDatabaseSQL(query), args)
	}
	return nil, driver.ErrSkip
}

type prefixStmt struct{ driver.Stmt }

func (prefixStmt) Exec([]driver.Value) (driver.Result, error) { return nil, driver.ErrSkip }
func (prefixStmt) Query([]driver.Value) (driver.Rows, error)  { return nil, driver.ErrSkip }

func (s prefixStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if stmt, ok := s.Stmt.(driver.StmtExecContext); ok {
		return stmt.ExecContext(ctx, args)
	}
	return nil, driver.ErrSkip
}

func (s prefixStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if stmt, ok := s.Stmt.(driver.StmtQueryContext); ok {
		return stmt.QueryContext(ctx, args)
	}
	return nil, driver.ErrSkip
}

func openLibSQLPool(rawURL, token string, maxOpen, maxIdle int, lifetime, idleTime time.Duration) (*sql.DB, error) {
	dsn, err := libSQLDSN(rawURL, token)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(mappedLibSQLDriver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open libSQL database: %w", err)
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(min(maxOpen, maxIdle))
	db.SetConnMaxLifetime(lifetime)
	db.SetConnMaxIdleTime(idleTime)
	return db, nil
}

func libSQLDSN(rawURL, token string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", errors.New("libSQL database URL is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return "", errors.New("libSQL database URL is invalid")
	}
	if parsed.Scheme != "libsql" && parsed.Scheme != "https" {
		return "", errors.New("libSQL database URL must use libsql:// or https://")
	}
	if _, exists := parsed.Query()["authToken"]; exists {
		if parsed.Query().Get("authToken") == "" {
			return "", errors.New("libSQL URL authToken must not be empty")
		}
		return rawURL, nil
	}
	if token == "" {
		return rawURL, nil
	}
	query := parsed.Query()
	query.Set("authToken", token)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

var _ driver.Driver = prefixDriver{}
var _ driver.ConnPrepareContext = prefixConn{}
var _ driver.ConnBeginTx = prefixConn{}
var _ driver.Pinger = prefixConn{}
var _ driver.ExecerContext = prefixConn{}
var _ driver.QueryerContext = prefixConn{}
var _ driver.StmtExecContext = prefixStmt{}
var _ driver.StmtQueryContext = prefixStmt{}
