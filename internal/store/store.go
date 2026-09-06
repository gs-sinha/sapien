// Package store is Sapien's SQLite access layer (PLAN.md §15). It owns opening
// the per-workspace database, running embedded schema migrations, and
// providing the single-writer/many-reader access pattern the rest of the
// engine builds on:
//
//   - DB.Write serializes every write through one in-process mutex, so a lone
//     process is trivially single-writer and SQLITE_BUSY never surfaces.
//   - DB.Read and DB.SQL are for read-only access; they go through the pooled
//     *sql.DB directly and may run concurrently with each other.
//
// Everything else (query helpers, row<->domain mapping) belongs in the
// packages that own those tables, not here.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// DB is a handle to one workspace's SQLite database.
type DB struct {
	sqlDB    *sql.DB
	path     string
	inMemory bool

	writeMu sync.Mutex
}

// options holds Open's configuration, built up by Option funcs.
type options struct {
	maxOpenConns int
	skipMigrate  bool
}

// Option configures Open.
type Option func(*options)

// WithMaxOpenConns sets the maximum number of pooled connections used for
// on-disk databases. It is ignored for ":memory:" databases, which always use
// exactly one connection (see Open). The default is 8.
func WithMaxOpenConns(n int) Option {
	return func(o *options) { o.maxOpenConns = n }
}

// WithoutMigration skips the automatic Migrate call Open otherwise makes,
// letting the caller invoke DB.Migrate explicitly (useful in tests that want
// to exercise Migrate's idempotency or observe intermediate versions).
func WithoutMigration() Option {
	return func(o *options) { o.skipMigrate = true }
}

const defaultMaxOpenConns = 8

// Open opens (creating if necessary) the SQLite database at path, sets the
// pragmas Sapien relies on (WAL, synchronous=NORMAL, foreign_keys=ON,
// busy_timeout=5000, temp_store=MEMORY), creates path's parent directory if
// needed, and runs pending migrations. path may be the literal string
// ":memory:" for an ephemeral, process-local database (used by tests); such
// databases are restricted to a single pooled connection so every query
// observes the same in-memory database rather than each getting its own.
func Open(path string, opts ...Option) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("store: open: path is empty")
	}

	cfg := options{maxOpenConns: defaultMaxOpenConns}
	for _, opt := range opts {
		opt(&cfg)
	}

	inMemory := path == ":memory:"

	var dsn string
	if inMemory {
		// A single connection (enforced below via SetMaxOpenConns(1)) makes a
		// shared-cache URI unnecessary: every query goes through the same
		// physical connection, so every query sees the same database. WAL is
		// not requested here since SQLite ignores journal_mode=WAL for
		// in-memory databases (it silently stays "memory").
		dsn = "file::memory:" +
			"?_foreign_keys=on" +
			"&_synchronous=NORMAL" +
			"&_busy_timeout=5000" +
			"&_pragma=temp_store(MEMORY)"
	} else {
		if dir := filepath.Dir(path); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("store: create dir %q: %w", dir, err)
			}
		}
		dsn = "file:" + path +
			"?_journal_mode=WAL" +
			"&_foreign_keys=on" +
			"&_synchronous=NORMAL" +
			"&_busy_timeout=5000" +
			"&_pragma=temp_store(MEMORY)"
	}

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %q: %w", path, err)
	}

	if inMemory {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	} else {
		n := cfg.maxOpenConns
		if n <= 0 {
			n = defaultMaxOpenConns
		}
		sqlDB.SetMaxOpenConns(n)
		sqlDB.SetMaxIdleConns(n)
	}
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("store: ping %q: %w", path, err)
	}

	db := &DB{sqlDB: sqlDB, path: path, inMemory: inMemory}

	if !cfg.skipMigrate {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := db.Migrate(ctx); err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("store: migrate %q: %w", path, err)
		}
	}

	return db, nil
}

// SQL returns the underlying *sql.DB for read-only query helpers written by
// other packages. Do not use it to write; use Write instead so writes stay
// serialized.
func (db *DB) SQL() *sql.DB { return db.sqlDB }

// Path returns the path Open was called with.
func (db *DB) Path() string { return db.path }

// Close closes the underlying database.
func (db *DB) Close() error { return db.sqlDB.Close() }

// Write runs fn inside a transaction, serialized against every other Write
// call on this DB via an in-process mutex. This is what makes a lone Sapien
// process trivially single-writer: the daemon funnels every write through a
// DB.Write call, so two writers can never race for SQLite's single-writer
// lock and SQLITE_BUSY never surfaces. fn's transaction is committed if fn
// returns nil, and rolled back otherwise (fn's error is returned unchanged).
func (db *DB) Write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	tx, err := db.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin write tx: %w", err)
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit write tx: %w", err)
	}
	return nil
}

// Read runs fn with a single pooled connection checked out from the DB. Reads
// are not serialized against each other and may run concurrently; they only
// ever contend with a Write for SQLite's file lock, which busy_timeout papers
// over.
func (db *DB) Read(ctx context.Context, fn func(conn *sql.Conn) error) error {
	conn, err := db.sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: acquire read conn: %w", err)
	}
	defer conn.Close()
	return fn(conn)
}
