package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

// Этот файл сохраняет компактный тестовый язык старых интеграционных тестов.
// Production-код использует только контекстный Repository API.
type GameRow = domain.CatalogGame
type ListParams = domain.ListParams
type IndexBucket = domain.IndexBucket
type CatalogDateMatch = domain.CatalogDateMatch
type CatalogDateBackfillMatch = domain.CatalogDateBackfillMatch

const (
	maxSearchLen = 200
	maxGenres    = 50
)

type SourceGenre struct {
	Genre         string
	SourceGenreID sql.NullInt64
}

type CatalogDateTarget struct {
	MembershipID      int64
	GameID            string
	Title             string
	TitleEn           string
	FirstSeenAt       time.Time
	AddedOn           sql.NullTime
	AddedOnSource     string
	PreviousRemovedOn sql.NullTime
	Initial           bool
}

type GameView struct {
	ID                    string
	Title                 string
	TitleEn               string
	ReleaseYear           int
	Genres                []string
	Platforms             string
	ImageURL              string
	StoreURL              string
	Metacritic            sql.NullInt64
	MetacriticPageURL     sql.NullString
	MetacriticUser        sql.NullInt64
	MetacriticUserCount   sql.NullInt64
	OpenCritic            sql.NullInt64
	OpenCriticPlayer      sql.NullInt64
	OpenCriticPlayerCount sql.NullInt64
	OpenCriticPageURL     sql.NullString
	Average               sql.NullFloat64
	CriticAverage         sql.NullFloat64
	PlayerAverage         sql.NullFloat64
	HLTBMainSec           sql.NullInt64
	HLTBRating            sql.NullInt64
	HLTBPageURL           sql.NullString
	CatalogAddedOn        sql.NullTime
	CatalogAddedSource    sql.NullString
	CatalogSourceURL      sql.NullString
	RuSub                 bool
	RuVoice               bool
}

type ListResult struct {
	Games      []GameView
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

func openTestDB(path string) (*sql.DB, error) {
	return Open(context.Background(), path)
}

func UpsertGame(db dbHandle, game GameRow) error {
	return upsertGame(context.Background(), db, game)
}

func SetGenres(db dbHandle, gameID string, genres []string) error {
	return setGenres(context.Background(), db, gameID, genres)
}

func CountActive(db *sql.DB) (int, error) {
	return NewRepository(db).CountActive(context.Background())
}

func DeactivateMissing(db dbHandle, ids []string) (int64, error) {
	return deactivateMissing(context.Background(), db, ids)
}

func RecordCatalogSnapshot(db dbHandle, ids []string, observedAt time.Time) (domain.CatalogSnapshotResult, error) {
	return recordCatalogSnapshot(context.Background(), db, ids, observedAt)
}

func NormalizeParams(params *ListParams) { params.Normalize() }

func ListGames(db *sql.DB, params ListParams) (ListResult, error) {
	result, err := NewRepository(db).ListGames(context.Background(), params)
	if err != nil {
		return ListResult{}, err
	}
	view := ListResult{
		Total: result.Total, Page: result.Page, PageSize: result.PageSize,
		TotalPages: result.TotalPages, Games: make([]GameView, 0, len(result.Games)),
	}
	for _, game := range result.Games {
		view.Games = append(view.Games, testGameView(game))
	}
	return view, nil
}

func IndexBuckets(db *sql.DB, params ListParams) ([]IndexBucket, error) {
	return NewRepository(db).IndexBuckets(context.Background(), params)
}

func TitleIndexBuckets(db *sql.DB, params ListParams) ([]IndexBucket, error) {
	params.Sort = "title"
	return NewRepository(db).IndexBuckets(context.Background(), params)
}

func AddedIndexBuckets(db *sql.DB, params ListParams) ([]IndexBucket, error) {
	params.Sort = "added"
	return NewRepository(db).IndexBuckets(context.Background(), params)
}

func CurrentCatalogDateTargets(db *sql.DB) ([]CatalogDateTarget, error) {
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	targets, err := currentCatalogDateTargets(context.Background(), tx)
	if err != nil {
		return nil, err
	}
	result := make([]CatalogDateTarget, 0, len(targets))
	for _, target := range targets {
		result = append(result, CatalogDateTarget{
			MembershipID:      target.MembershipID,
			GameID:            target.GameID,
			Title:             target.Title,
			TitleEn:           target.TitleEn,
			FirstSeenAt:       target.FirstSeenAt,
			AddedOn:           nullTime(target.AddedOn),
			AddedOnSource:     target.AddedOnSource,
			PreviousRemovedOn: nullTime(target.PreviousRemovedOn),
			Initial:           target.Initial,
		})
	}
	return result, nil
}

func ApplyCatalogDateChanges(db *sql.DB, matches []CatalogDateMatch, resetIDs []int64) (int64, error) {
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	changed, err := applyCatalogDateChanges(context.Background(), tx, matches, resetIDs)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return changed, nil
}

func ApplyCatalogDateBackfillTx(
	tx *sql.Tx,
	matches []CatalogDateBackfillMatch,
	keepNullIDs []int64,
) (int64, error) {
	return applyCatalogDateBackfill(context.Background(), tx, matches, keepNullIDs)
}

func SetCatalogAddedDate(db *sql.DB, gameID string, addedOn time.Time, source, sourceURL string) error {
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("источник даты обязателен")
	}
	result, err := db.Exec(`
UPDATE catalog_memberships
SET added_on = ?, added_on_source = ?, source_url = ?
WHERE game_id = ? AND removed_on IS NULL`,
		addedOn.UTC().Format("2006-01-02"), source, nullableString(sourceURL), gameID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func SetSourceGenres(db *sql.DB, gameID, source string, genres []SourceGenre) error {
	converted := make([]domain.SourceGenre, 0, len(genres))
	for _, genre := range genres {
		converted = append(converted, domain.SourceGenre{
			Name:     genre.Genre,
			SourceID: nullIntPointer(genre.SourceGenreID),
		})
	}
	return NewRepository(db).SetSourceGenres(context.Background(), gameID, source, converted)
}

func SourceGenres(db *sql.DB, gameID string) (map[string][]string, error) {
	rows, err := db.Query(`
SELECT source, genre
FROM game_source_genres
WHERE game_id = ?
ORDER BY source, genre`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string][]string)
	for rows.Next() {
		var source, genre string
		if err := rows.Scan(&source, &genre); err != nil {
			return nil, err
		}
		result[source] = append(result[source], genre)
	}
	return result, rows.Err()
}

func UpdateMetacriticScores(
	db *sql.DB,
	id string,
	critic, user, userCount sql.NullInt64,
	pageURL sql.NullString,
) error {
	return NewRepository(db).UpdateMetacritic(context.Background(), id, domain.MetacriticUpdate{
		Critic: nullIntPointer(critic), User: nullIntPointer(user),
		UserCount: nullIntPointer(userCount), PageURL: nullStringPointer(pageURL),
	})
}

func UpdateOpenCriticScores(
	db *sql.DB,
	id string,
	critic sql.NullInt64,
	pageURL sql.NullString,
	openCriticID, player, playerCount sql.NullInt64,
) error {
	return NewRepository(db).UpdateOpenCritic(context.Background(), id, domain.OpenCriticUpdate{
		Critic: nullIntPointer(critic), PageURL: nullStringPointer(pageURL),
		ID: nullIntPointer(openCriticID), Player: nullIntPointer(player),
		PlayerCount: nullIntPointer(playerCount),
	})
}

func UpdateOpenCritic(db *sql.DB, id string, critic sql.NullInt64, pageURL sql.NullString) error {
	return UpdateOpenCriticScores(db, id, critic, pageURL, sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{})
}

func UpdateHLTB(
	db *sql.DB,
	id string,
	mainExtra, rating, hltbID sql.NullInt64,
	pageURL sql.NullString,
) error {
	return NewRepository(db).UpdateHLTB(context.Background(), id, domain.HLTBUpdate{
		MainExtraSeconds: nullIntPointer(mainExtra), Rating: nullIntPointer(rating),
		ID: nullIntPointer(hltbID), PageURL: nullStringPointer(pageURL),
	})
}

func GamesNeedingMetacritic(db *sql.DB, staleBefore time.Time) ([]domain.ScoreTarget, error) {
	return NewRepository(db).GamesNeedingMetacritic(context.Background(), staleBefore)
}

func GamesNeedingOpenCritic(db *sql.DB, staleBefore time.Time) ([]domain.ScoreTarget, error) {
	return NewRepository(db).GamesNeedingOpenCritic(context.Background(), staleBefore)
}

func GamesNeedingHLTB(db *sql.DB, staleBefore time.Time) ([]domain.ScoreTarget, error) {
	return NewRepository(db).GamesNeedingHLTB(context.Background(), staleBefore)
}

func testGameView(game domain.CatalogItem) GameView {
	return GameView{
		ID: game.ID, Title: game.Title, TitleEn: game.TitleEn,
		ReleaseYear: game.ReleaseYear, Genres: game.Genres,
		Platforms: game.Platforms, ImageURL: game.ImageURL, StoreURL: game.StoreURL,
		Metacritic:            sqlNullInt(game.Metacritic),
		MetacriticPageURL:     sqlNullString(game.MetacriticPageURL),
		MetacriticUser:        sqlNullInt(game.MetacriticUser),
		MetacriticUserCount:   sqlNullInt(game.MetacriticUserCount),
		OpenCritic:            sqlNullInt(game.OpenCritic),
		OpenCriticPlayer:      sqlNullInt(game.OpenCriticPlayer),
		OpenCriticPlayerCount: sqlNullInt(game.OpenCriticPlayerCount),
		OpenCriticPageURL:     sqlNullString(game.OpenCriticPageURL),
		Average:               sqlNullFloat(game.Average),
		CriticAverage:         sqlNullFloat(game.CriticAverage),
		PlayerAverage:         sqlNullFloat(game.PlayerAverage),
		HLTBMainSec:           sqlNullInt(game.HLTBMainSec),
		HLTBRating:            sqlNullInt(game.HLTBRating),
		HLTBPageURL:           sqlNullString(game.HLTBPageURL),
		CatalogAddedOn:        sqlNullTime(game.CatalogAddedOn),
		CatalogAddedSource:    sqlNullString(game.CatalogAddedSource),
		CatalogSourceURL:      sqlNullString(game.CatalogSourceURL),
		RuSub:                 game.RuSub, RuVoice: game.RuVoice,
	}
}

func nullIntPointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func nullTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func sqlNullInt(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func sqlNullFloat(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}

func sqlNullString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func sqlNullTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}
