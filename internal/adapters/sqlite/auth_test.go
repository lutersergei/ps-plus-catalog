package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

func TestAuthRepositoryPersistsSessionAndUserProfile(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repository := NewRepository(db)

	user, err := repository.UpsertGoogleUser(ctx, domain.GoogleIdentity{
		Subject: "subject-1", Email: "old@example.com", Name: "Old", AvatarURL: "",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	updated, err := repository.UpsertGoogleUser(ctx, domain.GoogleIdentity{
		Subject: "subject-1", Email: "new@example.com", Name: "New", AvatarURL: "https://lh3.googleusercontent.com/avatar",
	})
	if err != nil {
		t.Fatalf("update user: %v", err)
	}
	if updated.ID != user.ID || updated.Email != "new@example.com" || updated.Name != "New" {
		t.Fatalf("updated user=%+v, original=%+v", updated, user)
	}

	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	hash := strings.Repeat("a", 64)
	if err := repository.CreateUserSession(ctx, hash, user.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	got, found, err := repository.UserBySessionHash(ctx, hash, now)
	if err != nil || !found || got.ID != user.ID || got.Email != "new@example.com" {
		t.Fatalf("session user=%+v found=%v err=%v", got, found, err)
	}
	if _, found, err := repository.UserBySessionHash(ctx, hash, now.Add(2*time.Hour)); err != nil || found {
		t.Fatalf("expired session found=%v err=%v", found, err)
	}
	if err := repository.DeleteExpiredUserSessions(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if _, found, err := repository.UserBySessionHash(ctx, hash, now); err != nil || found {
		t.Fatalf("deleted session found=%v err=%v", found, err)
	}
}

func TestAuthRepositoryFavoritesArePerUserAndFilterCatalog(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "favorites.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repository := NewRepository(db)
	for _, game := range []domain.CatalogGame{{ID: "g1", Title: "Alpha"}, {ID: "g2", Title: "Beta"}} {
		if err := upsertGame(ctx, db, game); err != nil {
			t.Fatalf("upsert %s: %v", game.ID, err)
		}
	}
	first, err := repository.UpsertGoogleUser(ctx, domain.GoogleIdentity{Subject: "first", Email: "first@example.com", Name: "First"})
	if err != nil {
		t.Fatalf("first user: %v", err)
	}
	second, err := repository.UpsertGoogleUser(ctx, domain.GoogleIdentity{Subject: "second", Email: "second@example.com", Name: "Second"})
	if err != nil {
		t.Fatalf("second user: %v", err)
	}
	if err := repository.SetFavorite(ctx, first.ID, "g1", true); err != nil {
		t.Fatalf("favorite g1: %v", err)
	}
	if err := repository.SetFavorite(ctx, first.ID, "g1", true); err != nil {
		t.Fatalf("idempotent favorite g1: %v", err)
	}
	if err := repository.SetFavorite(ctx, second.ID, "g2", true); err != nil {
		t.Fatalf("second favorite: %v", err)
	}

	all, err := repository.ListGames(ctx, domain.ListParams{ViewerUserID: first.ID, PageSize: 25})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all.Games) != 2 || !all.Games[0].Favorite || all.Games[1].Favorite {
		t.Fatalf("first user favorites=%+v", all.Games)
	}
	favorites, err := repository.ListGames(ctx, domain.ListParams{
		ViewerUserID: first.ID, FavoritesOnly: true, PageSize: 25,
	})
	if err != nil {
		t.Fatalf("list favorites: %v", err)
	}
	if favorites.Total != 1 || len(favorites.Games) != 1 || favorites.Games[0].ID != "g1" || !favorites.Games[0].Favorite {
		t.Fatalf("favorites result=%+v", favorites)
	}
	secondFavorites, err := repository.ListGames(ctx, domain.ListParams{
		ViewerUserID: second.ID, FavoritesOnly: true, PageSize: 25,
	})
	if err != nil || secondFavorites.Total != 1 || secondFavorites.Games[0].ID != "g2" {
		t.Fatalf("second favorites=%+v err=%v", secondFavorites, err)
	}

	if err := repository.SetFavorite(ctx, first.ID, "g1", false); err != nil {
		t.Fatalf("remove favorite: %v", err)
	}
	favorites, err = repository.ListGames(ctx, domain.ListParams{
		ViewerUserID: first.ID, FavoritesOnly: true, PageSize: 25,
	})
	if err != nil || favorites.Total != 0 {
		t.Fatalf("favorites after remove=%+v err=%v", favorites, err)
	}
	if err := repository.SetFavorite(ctx, first.ID, "missing", true); !errors.Is(err, domain.ErrGameNotFound) {
		t.Fatalf("missing game error=%v", err)
	}
}

func TestAuthRepositoryCascadesFavoritesAndSessions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "cascade.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repository := NewRepository(db)
	if err := upsertGame(ctx, db, domain.CatalogGame{ID: "g1", Title: "Game"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	user, err := repository.UpsertGoogleUser(ctx, domain.GoogleIdentity{Subject: "subject", Email: "user@example.com", Name: "User"})
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := repository.SetFavorite(ctx, user.ID, "g1", true); err != nil {
		t.Fatalf("favorite: %v", err)
	}
	if err := repository.CreateUserSession(ctx, strings.Repeat("b", 64), user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	for _, table := range []string{"user_favorites", "user_sessions"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count=%d после удаления пользователя", table, count)
		}
	}
}
