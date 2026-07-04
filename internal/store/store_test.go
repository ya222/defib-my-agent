package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/version"
)

func TestOpen_FreshDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "defib.db")

	s, err := Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	wantTables := []string{"tasks", "attempts", "events", "daemon_meta"}
	for _, table := range wantTables {
		var name string
		err := s.DB().QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name)
		require.NoError(t, err, "table %q should exist", table)
		assert.Equal(t, table, name)
	}

	got, err := s.SchemaVersion()
	require.NoError(t, err)
	assert.Equal(t, version.SchemaVersion, got)
}

func TestOpen_Reopen_NoDuplicateApplication(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "defib.db")

	s1, err := Open(dbPath)
	require.NoError(t, err)

	_, err = s1.DB().Exec(
		`INSERT INTO tasks (
			id, name, provider, mode, cwd, session_mode, config_json, status, created_at, updated_at
		) VALUES ('t1', 'task-one', 'fake', 'headless', '/tmp', 'new', '{}', 'PENDING', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	)
	require.NoError(t, err)
	require.NoError(t, s1.Close())

	s2, err := Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s2.Close() })

	got, err := s2.SchemaVersion()
	require.NoError(t, err)
	assert.Equal(t, 1, got)
	assert.Equal(t, version.SchemaVersion, got)

	var count int
	err = s2.DB().QueryRow(`SELECT count(*) FROM tasks WHERE id = 't1'`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	for _, table := range []string{"tasks", "attempts", "events", "daemon_meta"} {
		var name string
		err := s2.DB().QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name)
		require.NoError(t, err, "table %q should still exist", table)
	}
}

func TestOpen_WALAndForeignKeysOn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "defib.db")

	s, err := Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var mode string
	require.NoError(t, s.DB().QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	assert.Equal(t, "wal", mode)

	_, err = s.DB().Exec(
		`INSERT INTO attempts (
			id, task_id, attempt_no, started_at, stdout_path, stderr_path
		) VALUES ('a1', 'does-not-exist', 1, '2026-01-01T00:00:00Z', '/tmp/out', '/tmp/err')`,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FOREIGN KEY")
}

func TestOpen_NewerSchemaVersionFails(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "defib.db")

	s, err := Open(dbPath)
	require.NoError(t, err)

	_, err = s.DB().Exec(
		`UPDATE daemon_meta SET value = '999' WHERE key = 'schema_version'`,
	)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = Open(dbPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")
	assert.Contains(t, err.Error(), "newer")
}

func TestLoadMigrations_OrdersByVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/0002_second.sql": &fstest.MapFile{Data: []byte("CREATE TABLE second (id INTEGER);")},
		"migrations/0001_first.sql":  &fstest.MapFile{Data: []byte("CREATE TABLE first (id INTEGER);")},
	}

	migrations, err := loadMigrations(fsys)
	require.NoError(t, err)
	require.Len(t, migrations, 2)
	assert.Equal(t, 1, migrations[0].version)
	assert.Equal(t, "0001_first.sql", migrations[0].name)
	assert.Equal(t, 2, migrations[1].version)
	assert.Equal(t, "0002_second.sql", migrations[1].name)
}

func TestLoadMigrations_MissingVersionPrefix(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/bogus.sql": &fstest.MapFile{Data: []byte("CREATE TABLE bogus (id INTEGER);")},
	}

	_, err := loadMigrations(fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version prefix")
}

func TestMigrate_BrokenSQLRollsBackAtomically(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "defib.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())

	broken := []migration{
		{version: 1, name: "0001_broken.sql", sql: "CREATE TABLE ok_table (id INTEGER); THIS IS NOT VALID SQL;"},
	}

	err = migrate(db, broken)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "0001_broken.sql")

	var tableCount int
	require.NoError(t, db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'ok_table'`,
	).Scan(&tableCount))
	assert.Equal(t, 0, tableCount, "table created before the broken statement must be rolled back")

	got, err := currentVersion(db)
	require.NoError(t, err)
	assert.Equal(t, 0, got, "schema_version must not have been bumped")
}

func TestMigrate_AppliesOnlyPendingMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "defib.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())

	// Versions are kept within version.SchemaVersion so the second migrate
	// call below doesn't trip the "newer than supported" guard.
	all := []migration{
		{
			version: version.SchemaVersion,
			name:    "0001_first.sql",
			sql:     "CREATE TABLE first (id INTEGER); CREATE TABLE daemon_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);",
		},
	}
	require.NoError(t, migrate(db, all))

	got, err := currentVersion(db)
	require.NoError(t, err)
	assert.Equal(t, version.SchemaVersion, got)

	// Re-running with the same migration set must not re-apply migration 1,
	// which would fail with "table already exists" if it were replayed.
	require.NoError(t, migrate(db, all))

	got, err = currentVersion(db)
	require.NoError(t, err)
	assert.Equal(t, version.SchemaVersion, got)
}
