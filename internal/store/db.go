package store

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS games (
	id                TEXT PRIMARY KEY,
	title             TEXT NOT NULL,
	release_year      INTEGER,
	platforms         TEXT,
	image_url         TEXT,
	store_url         TEXT,
	metacritic_score  INTEGER,
	opencritic_score  INTEGER,
	average_score     REAL,
	scores_updated_at TIMESTAMP
);
CREATE TABLE IF NOT EXISTS game_genres (
	game_id TEXT NOT NULL,
	genre   TEXT NOT NULL,
	PRIMARY KEY (game_id, genre)
);
CREATE INDEX IF NOT EXISTS idx_game_genres_genre ON game_genres(genre);
`

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

// Migrate создаёт таблицы и индексы, если их ещё нет.
func Migrate(db *sql.DB) error {
	_, err := db.Exec(schema)
	return err
}
