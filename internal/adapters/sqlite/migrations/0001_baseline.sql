-- Базовая схема на момент перехода проекта к версионированным миграциям.
-- Конструкции IF NOT EXISTS позволяют принять уже существующую production-БД;
-- недостающие столбцы ранних выпусков добавляет обработчик миграции №1.

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

CREATE TABLE IF NOT EXISTS game_source_genres (
	game_id         TEXT NOT NULL,
	source          TEXT NOT NULL,
	genre           TEXT NOT NULL,
	source_genre_id INTEGER,
	PRIMARY KEY (game_id, source, genre)
);
CREATE INDEX IF NOT EXISTS idx_game_source_genres_source_genre ON game_source_genres(source, genre);

CREATE TABLE IF NOT EXISTS catalog_memberships (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	game_id         TEXT NOT NULL,
	added_on        DATE,
	removed_on      DATE,
	first_seen_at   TIMESTAMP NOT NULL,
	last_seen_at    TIMESTAMP NOT NULL,
	added_on_source TEXT,
	source_url      TEXT,
	FOREIGN KEY (game_id) REFERENCES games(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_catalog_memberships_open
	ON catalog_memberships(game_id) WHERE removed_on IS NULL;
CREATE INDEX IF NOT EXISTS idx_catalog_memberships_game
	ON catalog_memberships(game_id, first_seen_at);

CREATE TABLE IF NOT EXISTS catalog_sync_lock (
	id          INTEGER PRIMARY KEY CHECK (id = 1),
	acquired_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT OR IGNORE INTO catalog_sync_lock (id) VALUES (1);

CREATE TABLE IF NOT EXISTS catalog_announcements (
	url             TEXT PRIMARY KEY,
	last_modified   TEXT NOT NULL,
	parser_version  INTEGER NOT NULL DEFAULT 0,
	published_on    DATE NOT NULL,
	fetched_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS catalog_announcement_games (
	announcement_url TEXT NOT NULL,
	game_title       TEXT NOT NULL,
	added_on         DATE NOT NULL,
	PRIMARY KEY (announcement_url, game_title),
	FOREIGN KEY (announcement_url) REFERENCES catalog_announcements(url) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_catalog_announcement_games_added
	ON catalog_announcement_games(added_on);
