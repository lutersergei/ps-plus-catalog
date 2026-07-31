package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

const (
	defaultSessionTTL = 30 * 24 * time.Hour
	sessionTokenBytes = 32
	maxGameIDLength   = 256
)

var (
	// ErrInvalidIdentity означает неполный или некорректный профиль Google.
	ErrInvalidIdentity = errors.New("invalid google identity")
	// ErrInvalidGameID означает некорректный идентификатор игры в HTTP-запросе.
	ErrInvalidGameID = errors.New("invalid game id")
)

// AuthStore описывает хранение пользователей, сессий и избранного.
type AuthStore interface {
	UpsertGoogleUser(context.Context, domain.GoogleIdentity) (domain.User, error)
	CreateUserSession(context.Context, string, int64, time.Time) error
	UserBySessionHash(context.Context, string, time.Time) (domain.User, bool, error)
	DeleteUserSession(context.Context, string) error
	DeleteExpiredUserSessions(context.Context, time.Time) error
	SetFavorite(context.Context, int64, string, bool) error
}

// AuthOptions позволяет тестам подменить часы и источник случайности.
type AuthOptions struct {
	SessionTTL time.Duration
	Random     io.Reader
	Now        func() time.Time
}

// LoginSession — новая серверная сессия после успешного входа через Google.
type LoginSession struct {
	User      domain.User
	Token     string
	ExpiresAt time.Time
}

// AuthService управляет локальными пользователями и непрозрачными сессиями.
type AuthService struct {
	store      AuthStore
	sessionTTL time.Duration
	random     io.Reader
	now        func() time.Time
}

// NewAuthService создаёт сервис авторизации с сессиями на 30 дней.
func NewAuthService(store AuthStore, options AuthOptions) *AuthService {
	ttl := options.SessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	randomSource := options.Random
	if randomSource == nil {
		randomSource = rand.Reader
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &AuthService{store: store, sessionTTL: ttl, random: randomSource, now: now}
}

// NewOAuthState создаёт одноразовый state для защиты OAuth callback от CSRF.
func (s *AuthService) NewOAuthState() (string, error) {
	return s.randomToken()
}

// SignIn обновляет локальный профиль и создаёт новую серверную сессию.
func (s *AuthService) SignIn(
	ctx context.Context,
	identity domain.GoogleIdentity,
) (LoginSession, error) {
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.Email = strings.TrimSpace(identity.Email)
	identity.Name = strings.TrimSpace(identity.Name)
	identity.AvatarURL = strings.TrimSpace(identity.AvatarURL)
	if identity.Subject == "" || len(identity.Subject) > 255 ||
		identity.Email == "" || len(identity.Email) > 320 || !strings.Contains(identity.Email, "@") ||
		len(identity.Name) > 200 || len(identity.AvatarURL) > 2048 {
		return LoginSession{}, ErrInvalidIdentity
	}
	if identity.Name == "" {
		identity.Name = identity.Email
	}
	user, err := s.store.UpsertGoogleUser(ctx, identity)
	if err != nil {
		return LoginSession{}, fmt.Errorf("сохранить пользователя Google: %w", err)
	}
	token, err := s.randomToken()
	if err != nil {
		return LoginSession{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.sessionTTL)
	if err := s.store.CreateUserSession(ctx, hashToken(token), user.ID, expiresAt); err != nil {
		return LoginSession{}, fmt.Errorf("создать сессию пользователя: %w", err)
	}
	// Очистка не влияет на только что созданную сессию и не должна отменять вход.
	_ = s.store.DeleteExpiredUserSessions(ctx, now)
	return LoginSession{User: user, Token: token, ExpiresAt: expiresAt}, nil
}

// CurrentUser проверяет session token и возвращает владельца активной сессии.
func (s *AuthService) CurrentUser(
	ctx context.Context,
	token string,
) (domain.User, bool, error) {
	if !validToken(token) {
		return domain.User{}, false, nil
	}
	return s.store.UserBySessionHash(ctx, hashToken(token), s.now().UTC())
}

// SignOut удаляет серверную сессию. Некорректная cookie считается уже вышедшей.
func (s *AuthService) SignOut(ctx context.Context, token string) error {
	if !validToken(token) {
		return nil
	}
	return s.store.DeleteUserSession(ctx, hashToken(token))
}

// CSRFToken детерминированно выводится из секретного session token и безопасен
// для передачи в HTML: восстановить исходную cookie по нему нельзя.
func (s *AuthService) CSRFToken(sessionToken string) string {
	digest := sha256.Sum256([]byte("ps-extra-csrf-v1:" + sessionToken))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// VerifyCSRF сравнивает CSRF-токен за постоянное время.
func (s *AuthService) VerifyCSRF(sessionToken, candidate string) bool {
	if !validToken(sessionToken) || len(candidate) != 43 {
		return false
	}
	expected := s.CSRFToken(sessionToken)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(candidate)) == 1
}

// SetFavorite изменяет избранное только от имени уже проверенного пользователя.
func (s *AuthService) SetFavorite(
	ctx context.Context,
	userID int64,
	gameID string,
	favorite bool,
) error {
	gameID = strings.TrimSpace(gameID)
	if userID <= 0 || gameID == "" || len(gameID) > maxGameIDLength || strings.ContainsAny(gameID, "\x00\r\n") {
		return ErrInvalidGameID
	}
	return s.store.SetFavorite(ctx, userID, gameID, favorite)
}

func (s *AuthService) randomToken() (string, error) {
	buffer := make([]byte, sessionTokenBytes)
	if _, err := io.ReadFull(s.random, buffer); err != nil {
		return "", fmt.Errorf("получить криптографическую случайность: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func validToken(token string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == sessionTokenBytes
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
