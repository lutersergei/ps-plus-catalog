package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateRecordsVersionAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if len(migrations) != 2 {
		t.Fatalf("migrations=%d, ожидали 2", len(migrations))
	}
	want := migrations[len(migrations)-1]

	var version int
	var name, checksum, appliedAt string
	if err := db.QueryRowContext(ctx, `
SELECT version, name, checksum, applied_at
FROM schema_migrations WHERE version = ?`, want.Version).Scan(&version, &name, &checksum, &appliedAt); err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if version != want.Version || name != want.Name || checksum != want.Checksum || appliedAt == "" {
		t.Fatalf("migration=%d %q %q %q", version, name, checksum, appliedAt)
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var count int
	var secondAppliedAt string
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations after rerun: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT applied_at FROM schema_migrations WHERE version = ?`, want.Version,
	).Scan(&secondAppliedAt); err != nil {
		t.Fatalf("read migration after rerun: %v", err)
	}
	if count != len(migrations) || secondAppliedAt != appliedAt {
		t.Fatalf("count=%d applied_at=%q, ожидали %d неизменных записей, latest=%q", count, secondAppliedAt, len(migrations), appliedAt)
	}
}

func TestMigrateAdoptsUnversionedLegacyDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, `
CREATE TABLE games (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  title_en TEXT,
  release_year INTEGER,
  platforms TEXT,
  image_url TEXT,
  store_url TEXT,
  metacritic_score INTEGER,
  opencritic_score INTEGER,
  average_score REAL,
  mc_checked_at TIMESTAMP,
  oc_checked_at TIMESTAMP
);
INSERT INTO games (id, title, metacritic_score) VALUES ('legacy', 'Legacy Game', 81);`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	for _, expected := range baselineCompatibilityColumns {
		columns, err := tableColumns(ctx, db, expected.table)
		if err != nil {
			t.Fatalf("table %s: %v", expected.table, err)
		}
		if _, exists := columns[expected.column]; !exists {
			t.Errorf("столбец %s.%s не добавлен", expected.table, expected.column)
		}
	}

	var title string
	var score int
	if err := db.QueryRowContext(ctx,
		`SELECT title, metacritic_score FROM games WHERE id = 'legacy'`,
	).Scan(&title, &score); err != nil {
		t.Fatalf("read preserved row: %v", err)
	}
	if title != "Legacy Game" || score != 81 {
		t.Fatalf("legacy row=%q/%d", title, score)
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 2 {
		t.Fatalf("version=%d, ожидали 2", version)
	}
}

func TestMigrateUpgradesVersionOneWithoutChangingCatalog(t *testing.T) {
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	path := filepath.Join(t.TempDir(), "version-one.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, migrationTableSQL); err != nil {
		t.Fatalf("create migration table: %v", err)
	}
	if _, err := db.ExecContext(ctx, migrations[0].SQL); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO schema_migrations (version, name, checksum) VALUES (?, ?, ?)`,
		migrations[0].Version, migrations[0].Name, migrations[0].Checksum,
	); err != nil {
		t.Fatalf("record baseline: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO games (id, title, active) VALUES ('preserved', 'Preserved Game', 1)`); err != nil {
		t.Fatalf("seed game: %v", err)
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("migrate version one: %v", err)
	}
	var title string
	if err := db.QueryRowContext(ctx, `SELECT title FROM games WHERE id = 'preserved'`).Scan(&title); err != nil {
		t.Fatalf("read preserved game: %v", err)
	}
	if title != "Preserved Game" {
		t.Fatalf("title=%q", title)
	}
	var latestVersion int
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&latestVersion); err != nil {
		t.Fatalf("read latest migration: %v", err)
	}
	if latestVersion != 2 {
		t.Fatalf("latest migration=%d, ожидали 2", latestVersion)
	}
	for _, table := range []string{"users", "user_sessions", "user_favorites"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s не создана", table)
		}
	}
}

func TestMigrateRejectsUnknownFutureVersion(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "future.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `
INSERT INTO schema_migrations (version, name, checksum)
VALUES (9999, 'future', 'future')`); err != nil {
		t.Fatalf("seed future migration: %v", err)
	}

	err = Migrate(ctx, db)
	if err == nil || !strings.Contains(err.Error(), "неизвестную миграцию") {
		t.Fatalf("error=%v, ожидали отказ для будущей версии", err)
	}
}

func TestMigrateRejectsChangedMigrationChecksum(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "checksum.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx,
		`UPDATE schema_migrations SET checksum = 'changed' WHERE version = 1`,
	); err != nil {
		t.Fatalf("change checksum: %v", err)
	}

	err = Migrate(ctx, db)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("error=%v, ожидали отказ для изменённой миграции", err)
	}
}

func TestMigrateSerializesConcurrentProcesses(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "concurrent.db")
	db1, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatalf("open first database: %v", err)
	}
	defer db1.Close()
	db2, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatalf("open second database: %v", err)
	}
	defer db2.Close()
	if err := db1.PingContext(ctx); err != nil {
		t.Fatalf("ping first database: %v", err)
	}
	if err := db2.PingContext(ctx); err != nil {
		t.Fatalf("ping second database: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, db := range []*sql.DB{db1, db2} {
		go func(database *sql.DB) {
			<-start
			results <- Migrate(ctx, database)
		}(db)
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}

	var count int
	if err := db1.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 2 {
		t.Fatalf("migration count=%d, ожидали 2", count)
	}
}

func TestValidateAppliedMigrationsRejectsHistoryGap(t *testing.T) {
	migrations := []migration{
		{Version: 1, Name: "one", Checksum: "one"},
		{Version: 2, Name: "two", Checksum: "two"},
	}
	applied := map[int]appliedMigration{2: {Name: "two", Checksum: "two"}}
	if err := validateAppliedMigrations(migrations, applied); err == nil || !strings.Contains(err.Error(), "пропущена версия") {
		t.Fatalf("error=%v, ожидали ошибку разрыва истории", err)
	}
}

func TestParseMigrationFilename(t *testing.T) {
	for _, test := range []struct {
		filename string
		version  int
		name     string
		valid    bool
	}{
		{filename: "0001_baseline.sql", version: 1, name: "baseline", valid: true},
		{filename: "0042_add_score_index.sql", version: 42, name: "add_score_index", valid: true},
		{filename: "1_short.sql"},
		{filename: "0000_zero.sql"},
		{filename: "0002_Invalid.sql"},
		{filename: "0002_bad-name.sql"},
		{filename: "0002_.sql"},
	} {
		t.Run(test.filename, func(t *testing.T) {
			version, name, err := parseMigrationFilename(test.filename)
			if !test.valid {
				if err == nil {
					t.Fatalf("parse=%d/%q, ожидали ошибку", version, name)
				}
				return
			}
			if err != nil || version != test.version || name != test.name {
				t.Fatalf("parse=%d/%q err=%v, ожидали %d/%q", version, name, err, test.version, test.name)
			}
		})
	}
}
