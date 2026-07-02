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
	metacritic_url    TEXT,
	metacritic_user_score INTEGER,
	metacritic_user_count INTEGER,
	opencritic_score  INTEGER,
	opencritic_id     INTEGER,
	opencritic_url    TEXT,
	opencritic_player_score INTEGER,
	opencritic_player_count INTEGER,
	average_score     REAL,
	critic_average_score REAL,
	player_average_score REAL,
	hltb_main_extra   INTEGER,   -- время прохождения Main + Sides, в секундах
	hltb_rating       INTEGER,   -- пользовательский рейтинг HLTB (0–100)
	hltb_id           INTEGER,
	hltb_url          TEXT,
	mc_checked_at     TIMESTAMP,
	oc_checked_at     TIMESTAMP,
	hltb_checked_at   TIMESTAMP,
	spoken_langs      TEXT,      -- JSON-массив кодов языков озвучки (["en","ru",...])
	screen_langs      TEXT,      -- JSON-массив кодов языков субтитров/интерфейса
	langs_checked_at  TIMESTAMP
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
	`ALTER TABLE games ADD COLUMN hltb_id INTEGER`,
	`ALTER TABLE games ADD COLUMN hltb_url TEXT`,
	`ALTER TABLE games ADD COLUMN hltb_checked_at TIMESTAMP`,
	`ALTER TABLE games ADD COLUMN active INTEGER NOT NULL DEFAULT 1`,
	`ALTER TABLE games ADD COLUMN spoken_langs TEXT`,
	`ALTER TABLE games ADD COLUMN screen_langs TEXT`,
	`ALTER TABLE games ADD COLUMN langs_checked_at TIMESTAMP`,
	`ALTER TABLE games ADD COLUMN opencritic_url TEXT`,
	`ALTER TABLE games ADD COLUMN metacritic_user_score INTEGER`,
	`ALTER TABLE games ADD COLUMN metacritic_user_count INTEGER`,
	`ALTER TABLE games ADD COLUMN opencritic_id INTEGER`,
	`ALTER TABLE games ADD COLUMN opencritic_player_score INTEGER`,
	`ALTER TABLE games ADD COLUMN opencritic_player_count INTEGER`,
	`ALTER TABLE games ADD COLUMN critic_average_score REAL`,
	`ALTER TABLE games ADD COLUMN player_average_score REAL`,
	`ALTER TABLE games ADD COLUMN metacritic_url TEXT`,
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
	if err := RecomputeAllAverages(db); err != nil {
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
