// Package store provides SQLite access, typed models, and embedded migrations.
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/ya222/defib/internal/version"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps the SQLite database holding defib's durable state. Write
// transactions are serialized by writeMu — the data model's "single writer
// connection"; WAL lets readers proceed concurrently.
type Store struct {
	db      *sql.DB
	writeMu sync.Mutex
}

// Open opens (creating if needed) the database at path, applies pending
// migrations, and records the schema version in daemon_meta.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		path,
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database %q: %w", path, err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database %q: %w", path, err)
	}

	migrations, err := loadMigrations(migrationsFS)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database %q: load migrations: %w", path, err)
	}

	if err := migrate(db, migrations); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database %q: %w", path, err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	return nil
}

// SchemaVersion reads daemon_meta.schema_version.
func (s *Store) SchemaVersion() (int, error) {
	return currentVersion(s.db)
}

// DB exposes the underlying handle for the CRUD layer (M2-T2).
func (s *Store) DB() *sql.DB {
	return s.db
}

// migration is a single embedded SQL migration file, identified by the
// numeric prefix of its filename (e.g. "0001_init.sql" has version 1).
type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads every "migrations/*.sql" file from fsys and returns
// them ordered by ascending version. fsys is an fs.FS rather than the
// concrete embed.FS so tests can exercise the parser with a fake filesystem.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		verStr, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %q: filename missing version prefix", name)
		}
		ver, err := strconv.Atoi(verStr)
		if err != nil {
			return nil, fmt.Errorf("migration %q: invalid version prefix: %w", name, err)
		}

		contents, err := fs.ReadFile(fsys, "migrations/"+name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}

		migrations = append(migrations, migration{version: ver, name: name, sql: string(contents)})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })

	return migrations, nil
}

// migrate applies every migration whose version exceeds the database's
// current schema version, in order, each within its own transaction that
// also records the new schema_version. It fails if the database's recorded
// version is newer than version.SchemaVersion, which means the database was
// written by a newer binary.
func migrate(db *sql.DB, migrations []migration) error {
	current, err := currentVersion(db)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if current > version.SchemaVersion {
		return fmt.Errorf(
			"schema version %d is newer than this binary's supported version %d: written by a newer defib binary",
			current, version.SchemaVersion,
		)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := applyMigration(db, m); err != nil {
			return fmt.Errorf("apply migration %q: %w", m.name, err)
		}
	}

	return nil
}

// applyMigration runs a single migration's SQL and bumps daemon_meta's
// schema_version in one transaction, so a failure leaves neither applied.
func applyMigration(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed

	if _, err := tx.Exec(m.sql); err != nil {
		return fmt.Errorf("execute migration sql: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT INTO daemon_meta (key, value) VALUES ('schema_version', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		strconv.Itoa(m.version),
	); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// currentVersion returns the schema version recorded in daemon_meta, or 0 if
// the table or row does not exist yet (a brand-new database).
func currentVersion(db *sql.DB) (int, error) {
	var tableCount int
	err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'daemon_meta'`,
	).Scan(&tableCount)
	if err != nil {
		return 0, fmt.Errorf("check daemon_meta table: %w", err)
	}
	if tableCount == 0 {
		return 0, nil
	}

	var raw string
	err = db.QueryRow(`SELECT value FROM daemon_meta WHERE key = 'schema_version'`).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("query schema_version: %w", err)
	}

	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse schema_version %q: %w", raw, err)
	}

	return v, nil
}
