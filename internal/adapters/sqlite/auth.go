package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

// UpsertGoogleUser создаёт пользователя или обновляет его профиль при входе.
// Стабильным ключом служит Google subject, а не изменяемый email.
func (r *Repository) UpsertGoogleUser(
	ctx context.Context,
	identity domain.GoogleIdentity,
) (domain.User, error) {
	var user domain.User
	err := r.db.QueryRowContext(ctx, `
INSERT INTO users (google_subject, email, display_name, avatar_url)
VALUES (?, ?, ?, ?)
ON CONFLICT(google_subject) DO UPDATE SET
  email = excluded.email,
  display_name = excluded.display_name,
  avatar_url = excluded.avatar_url,
  updated_at = CURRENT_TIMESTAMP,
  last_login_at = CURRENT_TIMESTAMP
RETURNING id, email, display_name, avatar_url`,
		identity.Subject,
		identity.Email,
		identity.Name,
		identity.AvatarURL,
	).Scan(&user.ID, &user.Email, &user.Name, &user.AvatarURL)
	if err != nil {
		return domain.User{}, fmt.Errorf("upsert google user: %w", err)
	}
	return user, nil
}

// CreateUserSession сохраняет только SHA-256 случайного session token.
func (r *Repository) CreateUserSession(
	ctx context.Context,
	tokenHash string,
	userID int64,
	expiresAt time.Time,
) error {
	if _, err := r.db.ExecContext(ctx, `
INSERT INTO user_sessions (token_hash, user_id, expires_at)
VALUES (?, ?, ?)`, tokenHash, userID, expiresAt.UTC()); err != nil {
		return fmt.Errorf("create user session: %w", err)
	}
	return nil
}

// UserBySessionHash возвращает пользователя только для непросроченной сессии.
func (r *Repository) UserBySessionHash(
	ctx context.Context,
	tokenHash string,
	now time.Time,
) (domain.User, bool, error) {
	var user domain.User
	err := r.db.QueryRowContext(ctx, `
SELECT u.id, u.email, u.display_name, u.avatar_url
FROM user_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, now.UTC()).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.AvatarURL,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, false, nil
	}
	if err != nil {
		return domain.User{}, false, fmt.Errorf("find user session: %w", err)
	}
	return user, true, nil
}

// DeleteUserSession завершает одну сессию пользователя.
func (r *Repository) DeleteUserSession(ctx context.Context, tokenHash string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM user_sessions WHERE token_hash = ?`, tokenHash,
	); err != nil {
		return fmt.Errorf("delete user session: %w", err)
	}
	return nil
}

// DeleteExpiredUserSessions удаляет истёкшие серверные сессии.
func (r *Repository) DeleteExpiredUserSessions(ctx context.Context, now time.Time) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM user_sessions WHERE expires_at <= ?`, now.UTC(),
	); err != nil {
		return fmt.Errorf("delete expired user sessions: %w", err)
	}
	return nil
}

// SetFavorite идемпотентно добавляет игру в избранное или удаляет её оттуда.
func (r *Repository) SetFavorite(
	ctx context.Context,
	userID int64,
	gameID string,
	favorite bool,
) error {
	var gameExists bool
	if err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM games WHERE id = ?)`, gameID,
	).Scan(&gameExists); err != nil {
		return fmt.Errorf("check favorite game: %w", err)
	}
	if !gameExists {
		return domain.ErrGameNotFound
	}

	if favorite {
		if _, err := r.db.ExecContext(ctx, `
INSERT INTO user_favorites (user_id, game_id)
VALUES (?, ?)
ON CONFLICT(user_id, game_id) DO NOTHING`, userID, gameID); err != nil {
			return fmt.Errorf("add favorite: %w", err)
		}
		return nil
	}
	if _, err := r.db.ExecContext(ctx, `
DELETE FROM user_favorites WHERE user_id = ? AND game_id = ?`, userID, gameID); err != nil {
		return fmt.Errorf("remove favorite: %w", err)
	}
	return nil
}
