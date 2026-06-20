package store

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS games (
	id                TEXT PRIMARY KEY,
	title             TEXT NOT NULL,
	title_en          TEXT,
	release_year      INTEGER,
	platforms         TEXT,
	image_url         TEXT,
	store_url         TEXT,
	active            INTEGER NOT NULL DEFAULT 1,
	metacritic_score  INTEGER,
	opencritic_score  INTEGER,
	average_score     REAL,
	hltb_main_extra   INTEGER,   -- время прохождения Main + Sides, в секундах
	hltb_rating       INTEGER,   -- пользовательский рейтинг HLTB (0–100)
	mc_checked_at     TIMESTAMP,
	oc_checked_at     TIMESTAMP,
	hltb_checked_at   TIMESTAMP
);
CREATE TABLE IF NOT EXISTS game_genres (
	game_id TEXT NOT NULL,
	genre   TEXT NOT NULL,
	PRIMARY KEY (game_id, genre)
);
CREATE INDEX IF NOT EXISTS idx_game_genres_genre ON game_genres(genre);
`

// migrations добавляет недостающие колонки в уже существующую БД (idempotent).
var migrations = []string{
	`ALTER TABLE games ADD COLUMN hltb_main_extra INTEGER`,
	`ALTER TABLE games ADD COLUMN hltb_rating INTEGER`,
	`ALTER TABLE games ADD COLUMN hltb_checked_at TIMESTAMP`,
	`ALTER TABLE games ADD COLUMN active INTEGER NOT NULL DEFAULT 1`,
}

// Open открывает базу SQLite по указанному пути и применяет миграции.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate создаёт таблицы и индексы и добавляет недостающие колонки в уже
// существующую БД (ошибки «duplicate column name» игнорируются).
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	return nil
}
