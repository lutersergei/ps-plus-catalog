package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const migrationTableSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY CHECK (version > 0),
	name       TEXT NOT NULL UNIQUE,
	checksum   TEXT NOT NULL,
	applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

type appliedMigration struct {
	Name     string
	Checksum string
}

// migrationExecutor — общий контракт закреплённого соединения и БД для
// выполнения миграций и проверки схемы.
type migrationExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// baselineCompatibilityColumns перечисляет столбцы, которые появлялись до
// введения журнала версий. Обработчик baseline-миграции добавляет их старым БД и
// после успеха навсегда фиксирует версию 1; новые миграции должны быть обычными SQL.
var baselineCompatibilityColumns = []struct {
	table      string
	column     string
	definition string
}{
	{"games", "hltb_main_extra", "INTEGER"},
	{"games", "hltb_rating", "INTEGER"},
	{"games", "hltb_id", "INTEGER"},
	{"games", "hltb_url", "TEXT"},
	{"games", "hltb_checked_at", "TIMESTAMP"},
	{"games", "active", "INTEGER NOT NULL DEFAULT 1"},
	{"games", "spoken_langs", "TEXT"},
	{"games", "screen_langs", "TEXT"},
	{"games", "langs_checked_at", "TIMESTAMP"},
	{"games", "opencritic_url", "TEXT"},
	{"games", "metacritic_user_score", "INTEGER"},
	{"games", "metacritic_user_count", "INTEGER"},
	{"games", "opencritic_id", "INTEGER"},
	{"games", "opencritic_player_score", "INTEGER"},
	{"games", "opencritic_player_count", "INTEGER"},
	{"games", "critic_average_score", "REAL"},
	{"games", "player_average_score", "REAL"},
	{"games", "metacritic_url", "TEXT"},
	{"catalog_announcements", "parser_version", "INTEGER NOT NULL DEFAULT 0"},
}

var migrationHooks = map[int]func(context.Context, migrationExecutor) error{
	1: ensureBaselineCompatibility,
}

// Repository — SQLite-реализация хранилища для прикладных сервисов.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Open открывает SQLite и применяет ещё не выполненные миграции. Средние оценки
// здесь не пересчитываются: запуск веб-процесса не должен менять всю таблицу.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}
	return db, nil
}

func sqliteDSN(path string) string {
	const pragmas = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + pragmas
}

// Migrate атомарно применяет нумерованные SQL-файлы. BEGIN IMMEDIATE не даёт
// двум процессам одновременно принять решение о следующей версии. Неизвестная
// версия, разрыв истории или изменённый checksum считаются ошибкой.
func Migrate(ctx context.Context, db *sql.DB) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("получить соединение для миграций: %w", err)
	}
	defer connection.Close()

	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("начать транзакцию миграций: %w", err)
	}
	transactionOpen := true
	defer func() {
		if !transactionOpen {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = connection.ExecContext(rollbackContext, "ROLLBACK")
	}()

	if _, err := connection.ExecContext(ctx, migrationTableSQL); err != nil {
		return fmt.Errorf("создать журнал миграций: %w", err)
	}
	applied, err := readAppliedMigrations(ctx, connection)
	if err != nil {
		return err
	}
	if err := validateAppliedMigrations(migrations, applied); err != nil {
		return err
	}

	for _, item := range migrations {
		if _, exists := applied[item.Version]; exists {
			continue
		}
		if _, err := connection.ExecContext(ctx, item.SQL); err != nil {
			return fmt.Errorf("применить миграцию %04d_%s: %w", item.Version, item.Name, err)
		}
		if hook := migrationHooks[item.Version]; hook != nil {
			if err := hook(ctx, connection); err != nil {
				return fmt.Errorf("завершить миграцию %04d_%s: %w", item.Version, item.Name, err)
			}
		}
		if _, err := connection.ExecContext(ctx, `
INSERT INTO schema_migrations (version, name, checksum)
VALUES (?, ?, ?)`, item.Version, item.Name, item.Checksum); err != nil {
			return fmt.Errorf("записать миграцию %04d_%s: %w", item.Version, item.Name, err)
		}
	}

	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("зафиксировать миграции: %w", err)
	}
	transactionOpen = false
	return nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("прочитать встроенные миграции: %w", err)
	}
	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, name, err := parseMigrationFilename(entry.Name())
		if err != nil {
			return nil, err
		}
		contents, err := fs.ReadFile(migrationFiles, "migrations/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("прочитать миграцию %s: %w", entry.Name(), err)
		}
		if strings.TrimSpace(string(contents)) == "" {
			return nil, fmt.Errorf("миграция %s пуста", entry.Name())
		}
		digest := sha256.Sum256(contents)
		migrations = append(migrations, migration{
			Version:  version,
			Name:     name,
			SQL:      string(contents),
			Checksum: hex.EncodeToString(digest[:]),
		})
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("встроенные миграции не найдены")
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	for index, item := range migrations {
		if index == 0 && item.Version != 1 {
			return nil, fmt.Errorf("первая миграция должна иметь версию 1, получена %d", item.Version)
		}
		if index > 0 && migrations[index-1].Version == item.Version {
			return nil, fmt.Errorf("версия миграции %d объявлена дважды", item.Version)
		}
		if index > 0 && item.Version != migrations[index-1].Version+1 {
			return nil, fmt.Errorf("между миграциями %d и %d есть разрыв", migrations[index-1].Version, item.Version)
		}
	}
	return migrations, nil
}

func parseMigrationFilename(filename string) (int, string, error) {
	stem := strings.TrimSuffix(filename, ".sql")
	separator := strings.IndexByte(stem, '_')
	if separator != 4 || len(stem) <= separator+1 {
		return 0, "", fmt.Errorf("имя миграции %q должно иметь вид 0001_name.sql", filename)
	}
	version, err := strconv.Atoi(stem[:separator])
	if err != nil || version <= 0 {
		return 0, "", fmt.Errorf("некорректная версия миграции %q", filename)
	}
	name := stem[separator+1:]
	for _, character := range name {
		if character != '_' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return 0, "", fmt.Errorf("некорректное имя миграции %q", filename)
		}
	}
	return version, name, nil
}

func readAppliedMigrations(ctx context.Context, db migrationExecutor) (map[int]appliedMigration, error) {
	rows, err := db.QueryContext(ctx, `
SELECT version, name, checksum
FROM schema_migrations
ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("прочитать журнал миграций: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]appliedMigration)
	for rows.Next() {
		var version int
		var item appliedMigration
		if err := rows.Scan(&version, &item.Name, &item.Checksum); err != nil {
			return nil, fmt.Errorf("прочитать запись миграции: %w", err)
		}
		applied[version] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("прочитать журнал миграций: %w", err)
	}
	return applied, nil
}

func validateAppliedMigrations(migrations []migration, applied map[int]appliedMigration) error {
	known := make(map[int]migration, len(migrations))
	for _, item := range migrations {
		known[item.Version] = item
	}
	for version, recorded := range applied {
		item, exists := known[version]
		if !exists {
			return fmt.Errorf("база данных содержит неизвестную миграцию версии %d; требуется более новый бинарник", version)
		}
		if recorded.Name != item.Name {
			return fmt.Errorf("имя миграции версии %d изменилось: БД=%q, бинарник=%q", version, recorded.Name, item.Name)
		}
		if recorded.Checksum != item.Checksum {
			return fmt.Errorf("checksum миграции версии %d не совпадает", version)
		}
	}

	pendingSeen := false
	for _, item := range migrations {
		_, exists := applied[item.Version]
		if !exists {
			pendingSeen = true
			continue
		}
		if pendingSeen {
			return fmt.Errorf("в истории миграций пропущена версия перед %d", item.Version)
		}
	}
	return nil
}

func ensureBaselineCompatibility(ctx context.Context, db migrationExecutor) error {
	columnsByTable := make(map[string]map[string]struct{})
	for _, column := range baselineCompatibilityColumns {
		columns, ok := columnsByTable[column.table]
		if !ok {
			var err error
			columns, err = tableColumns(ctx, db, column.table)
			if err != nil {
				return err
			}
			columnsByTable[column.table] = columns
		}
		if _, exists := columns[column.column]; exists {
			continue
		}
		// Имена и определения берутся только из закрытого списка выше; данные
		// пользователя в DDL не подставляются.
		query := "ALTER TABLE " + column.table + " ADD COLUMN " + column.column + " " + column.definition
		if _, err := db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("добавить %s.%s: %w", column.table, column.column, err)
		}
		columns[column.column] = struct{}{}
	}
	return nil
}

func tableColumns(ctx context.Context, db migrationExecutor, table string) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return nil, fmt.Errorf("проверить таблицу %s: %w", table, err)
	}
	defer rows.Close()

	columns := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("проверить таблицу %s: %w", table, err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("проверить таблицу %s: %w", table, err)
	}
	return columns, nil
}
