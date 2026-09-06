package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationFileRE matches "NNN_name.sql" migration file names.
var migrationFileRE = regexp.MustCompile(`^(\d+)_(.+)\.sql$`)

// migration is one parsed, embedded migration file.
type migration struct {
	version int
	name    string
	script  string
}

// loadMigrations reads and parses every embedded migrations/*.sql file, sorted
// by version ascending.
func loadMigrations() ([]migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("store: read embedded migrations: %w", err)
	}

	migs := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		m := migrationFileRE.FindStringSubmatch(entry.Name())
		if m == nil {
			return nil, fmt.Errorf("store: migration file %q does not match NNN_name.sql", entry.Name())
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("store: migration file %q: invalid version: %w", entry.Name(), err)
		}
		b, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("store: read migration %q: %w", entry.Name(), err)
		}
		migs = append(migs, migration{version: version, name: m[2], script: string(b)})
	}

	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })

	for i := 1; i < len(migs); i++ {
		if migs[i].version == migs[i-1].version {
			return nil, fmt.Errorf("store: duplicate migration version %d (%s and %s)",
				migs[i].version, migs[i-1].name, migs[i].name)
		}
	}

	return migs, nil
}

// Migrate applies every pending embedded migration, in order, each inside its
// own transaction, recording it in schema_migrations as it commits. It is
// idempotent: calling it again once every migration is applied does nothing.
func (db *DB) Migrate(ctx context.Context) error {
	// schema_migrations itself is bootstrapped here (not as migration 001)
	// so Migrate can consult it before any migration file has run.
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version    INTEGER PRIMARY KEY,
				name       TEXT NOT NULL,
				applied_at TEXT NOT NULL
			)`)
		return err
	}); err != nil {
		return fmt.Errorf("store: bootstrap schema_migrations: %w", err)
	}

	migs, err := loadMigrations()
	if err != nil {
		return err
	}

	applied, err := db.appliedVersions(ctx)
	if err != nil {
		return err
	}

	for _, m := range migs {
		if applied[m.version] {
			continue
		}
		version, script := m.version, m.script
		name := m.name
		if err := db.Write(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, script); err != nil {
				return fmt.Errorf("store: apply migration %03d_%s: %w", version, name, err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
				version, name, time.Now().UTC().Format(time.RFC3339),
			); err != nil {
				return fmt.Errorf("store: record migration %03d_%s: %w", version, name, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}

	return nil
}

// appliedVersions returns the set of migration versions already recorded in
// schema_migrations.
func (db *DB) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := db.sqlDB.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("store: query schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate schema_migrations: %w", err)
	}
	return applied, nil
}

// Version returns the highest applied migration version, or 0 if none has
// been applied yet (or schema_migrations does not exist yet).
func (db *DB) Version(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := db.sqlDB.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("store: query version: %w", err)
	}
	return int(v.Int64), nil
}
