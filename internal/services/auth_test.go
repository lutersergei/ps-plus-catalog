package services

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

type authStoreStub struct {
	user             domain.User
	identity         domain.GoogleIdentity
	sessionHash      string
	sessionUserID    int64
	sessionExpiresAt time.Time
	lookupCalls      int
	deletedHash      string
	cleanupAt        time.Time
	favoriteUserID   int64
	favoriteGameID   string
	favoriteValue    bool
	favoriteError    error
}

func (store *authStoreStub) UpsertGoogleUser(_ context.Context, identity domain.GoogleIdentity) (domain.User, error) {
	store.identity = identity
	return store.user, nil
}

func (store *authStoreStub) CreateUserSession(_ context.Context, hash string, userID int64, expires time.Time) error {
	store.sessionHash = hash
	store.sessionUserID = userID
	store.sessionExpiresAt = expires
	return nil
}

func (store *authStoreStub) UserBySessionHash(_ context.Context, hash string, _ time.Time) (domain.User, bool, error) {
	store.lookupCalls++
	if hash != store.sessionHash {
		return domain.User{}, false, nil
	}
	return store.user, true, nil
}

func (store *authStoreStub) DeleteUserSession(_ context.Context, hash string) error {
	store.deletedHash = hash
	return nil
}

func (store *authStoreStub) DeleteExpiredUserSessions(_ context.Context, now time.Time) error {
	store.cleanupAt = now
	return nil
}

func (store *authStoreStub) SetFavorite(_ context.Context, userID int64, gameID string, favorite bool) error {
	store.favoriteUserID = userID
	store.favoriteGameID = gameID
	store.favoriteValue = favorite
	return store.favoriteError
}

func TestAuthServiceCreatesHashedSessionAndAuthenticates(t *testing.T) {
	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	store := &authStoreStub{user: domain.User{ID: 42, Email: "user@example.com", Name: "User"}}
	service := NewAuthService(store, AuthOptions{
		SessionTTL: 2 * time.Hour,
		Random:     bytes.NewReader(bytes.Repeat([]byte{7}, sessionTokenBytes)),
		Now:        func() time.Time { return now },
	})

	session, err := service.SignIn(context.Background(), domain.GoogleIdentity{
		Subject: " google-sub ", Email: " user@example.com ", Name: " User ",
	})
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}
	if session.User.ID != 42 || session.Token == "" || session.ExpiresAt != now.Add(2*time.Hour) {
		t.Fatalf("session=%+v", session)
	}
	if len(store.sessionHash) != 64 || store.sessionHash == session.Token {
		t.Fatalf("в хранилище должен передаваться только SHA-256, hash=%q token=%q", store.sessionHash, session.Token)
	}
	if store.identity.Subject != "google-sub" || store.identity.Name != "User" {
		t.Fatalf("identity не нормализован: %+v", store.identity)
	}
	if store.sessionUserID != 42 || store.cleanupAt != now {
		t.Fatalf("session user=%d cleanup=%v", store.sessionUserID, store.cleanupAt)
	}

	user, found, err := service.CurrentUser(context.Background(), session.Token)
	if err != nil || !found || user.ID != 42 {
		t.Fatalf("current user=%+v found=%v err=%v", user, found, err)
	}
	csrf := service.CSRFToken(session.Token)
	if !service.VerifyCSRF(session.Token, csrf) || service.VerifyCSRF(session.Token, csrf+"x") {
		t.Fatal("CSRF-токен должен приниматься только в точном виде")
	}
	if err := service.SignOut(context.Background(), session.Token); err != nil {
		t.Fatalf("sign out: %v", err)
	}
	if store.deletedHash != store.sessionHash {
		t.Fatalf("deleted hash=%q, ожидали %q", store.deletedHash, store.sessionHash)
	}
}

func TestAuthServiceRejectsMalformedInputBeforeStore(t *testing.T) {
	store := &authStoreStub{user: domain.User{ID: 1}}
	service := NewAuthService(store, AuthOptions{})
	if _, err := service.SignIn(context.Background(), domain.GoogleIdentity{Subject: "sub", Email: "not-an-email"}); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("invalid identity error=%v", err)
	}
	if _, found, err := service.CurrentUser(context.Background(), "not-a-token"); err != nil || found || store.lookupCalls != 0 {
		t.Fatalf("malformed token found=%v err=%v lookupCalls=%d", found, err, store.lookupCalls)
	}
	if err := service.SetFavorite(context.Background(), 0, "game", true); !errors.Is(err, ErrInvalidGameID) {
		t.Fatalf("invalid favorite error=%v", err)
	}
	if err := service.SetFavorite(context.Background(), 1, "game-1", true); err != nil {
		t.Fatalf("set favorite: %v", err)
	}
	if store.favoriteUserID != 1 || store.favoriteGameID != "game-1" || !store.favoriteValue {
		t.Fatalf("favorite call=%d/%q/%v", store.favoriteUserID, store.favoriteGameID, store.favoriteValue)
	}
}

func TestAuthServicePropagatesFavoriteError(t *testing.T) {
	store := &authStoreStub{favoriteError: domain.ErrGameNotFound}
	service := NewAuthService(store, AuthOptions{})
	if err := service.SetFavorite(context.Background(), 1, "missing", true); !errors.Is(err, domain.ErrGameNotFound) {
		t.Fatalf("favorite error=%v", err)
	}
}
