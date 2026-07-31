-- Пользователи Google, серверные сессии и персональное избранное.
-- OAuth access token не сохраняется: после входа нужен только стабильный sub.

CREATE TABLE users (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	google_subject TEXT NOT NULL UNIQUE,
	email          TEXT NOT NULL,
	display_name   TEXT NOT NULL,
	avatar_url     TEXT NOT NULL DEFAULT '',
	created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	last_login_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_users_email ON users(email COLLATE NOCASE);

CREATE TABLE user_sessions (
	token_hash TEXT PRIMARY KEY CHECK (length(token_hash) = 64),
	user_id    INTEGER NOT NULL,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	expires_at TIMESTAMP NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_user_sessions_user ON user_sessions(user_id);
CREATE INDEX idx_user_sessions_expires ON user_sessions(expires_at);

CREATE TABLE user_favorites (
	user_id    INTEGER NOT NULL,
	game_id    TEXT NOT NULL,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (user_id, game_id),
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
	FOREIGN KEY (game_id) REFERENCES games(id) ON DELETE CASCADE
);
CREATE INDEX idx_user_favorites_game ON user_favorites(game_id);
