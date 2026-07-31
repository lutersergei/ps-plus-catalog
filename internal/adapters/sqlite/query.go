package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

// sortColumns — белый список SQL-выражений сортировки. Пользовательское
// значение никогда не подставляется в запрос напрямую.
var sortColumns = map[string]string{
	"year":     "release_year",
	"average":  "average_score",
	"critic":   "critic_average_score",
	"player":   "player_average_score",
	"title":    "title COLLATE NOCASE",
	"hltbmain": "hltb_main_extra",
	"reviews":  reviewCountExpr,
	"added":    currentCatalogAddedExpr,
}

const reviewCountExpr = "(COALESCE(metacritic_user_count, 0) + COALESCE(opencritic_player_count, 0))"

const (
	currentCatalogAddedExpr = `(SELECT cm.added_on FROM catalog_memberships cm
		WHERE cm.game_id = games.id AND cm.removed_on IS NULL LIMIT 1)`
	currentCatalogAddedSourceExpr = `(SELECT cm.added_on_source FROM catalog_memberships cm
		WHERE cm.game_id = games.id AND cm.removed_on IS NULL LIMIT 1)`
	currentCatalogSourceURLExpr = `(SELECT cm.source_url FROM catalog_memberships cm
		WHERE cm.game_id = games.id AND cm.removed_on IS NULL LIMIT 1)`
)

// likeEscape экранирует специальные символы LIKE во введённой строке.
func likeEscape(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

// buildListWhere строит общее WHERE-условие для списка и индексных бакетов.
// Все значения передаются через placeholders.
func buildListWhere(params domain.ListParams) (string, []any) {
	where := []string{"active = 1"}
	var args []any
	if params.FavoritesOnly {
		if params.ViewerUserID <= 0 {
			where = append(where, "0 = 1")
		} else {
			where = append(where, `EXISTS (
				SELECT 1 FROM user_favorites favorite
				WHERE favorite.user_id = ? AND favorite.game_id = games.id
			)`)
			args = append(args, params.ViewerUserID)
		}
	}

	if search := strings.TrimSpace(params.Search); search != "" {
		like := "%" + likeEscape(search) + "%"
		where = append(where, `(title LIKE ? ESCAPE '\' OR COALESCE(title_en,'') LIKE ? ESCAPE '\')`)
		args = append(args, like, like)
	}
	if params.YearFrom > 0 {
		where = append(where, "release_year >= ?")
		args = append(args, params.YearFrom)
	}
	if params.YearTo > 0 {
		where = append(where, "release_year <= ?")
		args = append(args, params.YearTo)
	}
	if len(params.Genres) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(params.Genres)), ",")
		where = append(where, "id IN (SELECT game_id FROM game_genres WHERE genre IN ("+placeholders+"))")
		for _, genre := range params.Genres {
			args = append(args, genre)
		}
	}
	if params.AvgFrom > 0 {
		where = append(where, "average_score >= ?")
		args = append(args, params.AvgFrom)
	}
	if params.AvgTo > 0 {
		where = append(where, "average_score <= ?")
		args = append(args, params.AvgTo)
	}
	if params.CriticFrom > 0 {
		where = append(where, "critic_average_score >= ?")
		args = append(args, params.CriticFrom)
	}
	if params.CriticTo > 0 {
		where = append(where, "critic_average_score <= ?")
		args = append(args, params.CriticTo)
	}
	if params.PlayerFrom > 0 {
		where = append(where, "player_average_score >= ?")
		args = append(args, params.PlayerFrom)
	}
	if params.PlayerTo > 0 {
		where = append(where, "player_average_score <= ?")
		args = append(args, params.PlayerTo)
	}
	if params.ReviewsFrom > 0 {
		where = append(where, reviewCountExpr+" >= ?")
		args = append(args, params.ReviewsFrom)
	}
	if params.ReviewsTo > 0 {
		where = append(where, reviewCountExpr+" <= ?")
		args = append(args, params.ReviewsTo)
	}
	if params.HLTBFromHours > 0 {
		where = append(where, "hltb_main_extra >= ?")
		args = append(args, params.HLTBFromHours*3600)
	}
	if params.HLTBToHours > 0 {
		where = append(where, "hltb_main_extra <= ?")
		args = append(args, params.HLTBToHours*3600)
	}
	if params.RuSubtitles {
		where = append(where, `screen_langs LIKE '%"ru"%'`)
	}
	if params.RuVoice {
		where = append(where, `spoken_langs LIKE '%"ru"%'`)
	}
	return "WHERE " + strings.Join(where, " AND "), args
}

// IndexBuckets возвращает бакеты быстрого перехода для активной сортировки.
func (r *Repository) IndexBuckets(ctx context.Context, params domain.ListParams) ([]domain.IndexBucket, error) {
	params.Normalize()
	switch params.Sort {
	case "title":
		return r.titleIndexBuckets(ctx, params)
	case "year":
		return r.valueIndexBuckets(ctx, params, "release_year", yearBucketLabel)
	case "critic":
		return r.valueIndexBuckets(ctx, params, decadeExpr("critic_average_score"), decadeBucketLabel)
	case "player":
		return r.valueIndexBuckets(ctx, params, decadeExpr("player_average_score"), decadeBucketLabel)
	case "hltbmain":
		return r.valueIndexBuckets(ctx, params, hltbThresholdExpr, hltbBucketLabel)
	case "reviews":
		return r.valueIndexBuckets(ctx, params, reviewCountThresholdExpr, reviewCountBucketLabel)
	case "added":
		return r.addedIndexBuckets(ctx, params)
	default:
		return nil, nil
	}
}

const reviewCountThresholdExpr = `CASE
  WHEN ` + reviewCountExpr + ` = 0 THEN 0
  WHEN ` + reviewCountExpr + ` < 100 THEN 1
  WHEN ` + reviewCountExpr + ` < 500 THEN 100
  WHEN ` + reviewCountExpr + ` < 1000 THEN 500
  WHEN ` + reviewCountExpr + ` < 5000 THEN 1000
  ELSE 5000 END`

func reviewCountBucketLabel(value int64) string {
	switch value {
	case 0:
		return "0"
	case 1:
		return "1–99"
	case 100:
		return "100–499"
	case 500:
		return "500–999"
	case 1000:
		return "1к–4.9к"
	default:
		return "5к+"
	}
}

const hltbThresholdExpr = `CASE
  WHEN hltb_main_extra IS NULL THEN NULL
  WHEN hltb_main_extra < 5*3600 THEN 0
  WHEN hltb_main_extra < 10*3600 THEN 5
  WHEN hltb_main_extra < 20*3600 THEN 10
  WHEN hltb_main_extra < 40*3600 THEN 20
  WHEN hltb_main_extra < 60*3600 THEN 40
  ELSE 60 END`

func hltbBucketLabel(value int64) string {
	switch value {
	case 0:
		return "0–5"
	case 5:
		return "5–10"
	case 10:
		return "10–20"
	case 20:
		return "20–40"
	case 40:
		return "40–60"
	default:
		return "60+"
	}
}

func decadeExpr(column string) string {
	return "CAST(" + column + "/10 AS INTEGER)*10"
}

func decadeBucketLabel(value int64) string { return strconv.FormatInt(value, 10) }

func yearBucketLabel(value int64) string {
	if value <= 0 {
		return "—"
	}
	return strconv.FormatInt(value, 10)
}

// valueIndexBuckets группирует числовое выражение и рассчитывает кумулятивные
// смещения в том же порядке, что и основная выдача. NULL-хвост чипа не получает.
func (r *Repository) valueIndexBuckets(
	ctx context.Context,
	params domain.ListParams,
	expression string,
	label func(int64) string,
) ([]domain.IndexBucket, error) {
	whereSQL, args := buildListWhere(params)
	rows, err := r.db.QueryContext(
		ctx,
		"SELECT "+expression+", COUNT(*) FROM games "+whereSQL+" GROUP BY 1",
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query value index buckets: %w", err)
	}
	defer rows.Close()

	type group struct {
		value sql.NullInt64
		count int
	}
	var groups []group
	for rows.Next() {
		var item group
		if err := rows.Scan(&item.value, &item.count); err != nil {
			return nil, fmt.Errorf("scan value index bucket: %w", err)
		}
		groups = append(groups, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query value index buckets: %w", err)
	}

	descending := params.Order == "desc"
	sort.Slice(groups, func(i, j int) bool {
		left, right := groups[i], groups[j]
		if left.value.Valid != right.value.Valid {
			return left.value.Valid
		}
		if descending {
			return left.value.Int64 > right.value.Int64
		}
		return left.value.Int64 < right.value.Int64
	})

	var buckets []domain.IndexBucket
	offset := 0
	for _, group := range groups {
		if !group.value.Valid {
			break
		}
		buckets = append(buckets, domain.IndexBucket{
			Label:  label(group.value.Int64),
			Offset: offset,
		})
		offset += group.count
	}
	return buckets, nil
}

func (r *Repository) titleIndexBuckets(ctx context.Context, params domain.ListParams) ([]domain.IndexBucket, error) {
	whereSQL, args := buildListWhere(params)
	rows, err := r.db.QueryContext(
		ctx,
		"SELECT UPPER(SUBSTR(title,1,1)), COUNT(*) FROM games "+whereSQL+" GROUP BY 1",
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query title index buckets: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var label string
		var count int
		if err := rows.Scan(&label, &count); err != nil {
			return nil, fmt.Errorf("scan title index bucket: %w", err)
		}
		if len(label) != 1 || label[0] < 'A' || label[0] > 'Z' {
			label = "#"
		}
		counts[label] += count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query title index buckets: %w", err)
	}

	order := []string{"#"}
	for char := byte('A'); char <= 'Z'; char++ {
		order = append(order, string(char))
	}
	if params.Order == "desc" {
		for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
			order[i], order[j] = order[j], order[i]
		}
	}

	var buckets []domain.IndexBucket
	offset := 0
	for _, label := range order {
		count := counts[label]
		if count == 0 {
			continue
		}
		buckets = append(buckets, domain.IndexBucket{Label: label, Offset: offset})
		offset += count
	}
	return buckets, nil
}

var monthNames = []string{"Янв", "Фев", "Мар", "Апр", "Май", "Июн", "Июл", "Авг", "Сен", "Окт", "Ноя", "Дек"}

func (r *Repository) addedIndexBuckets(ctx context.Context, params domain.ListParams) ([]domain.IndexBucket, error) {
	whereSQL, args := buildListWhere(params)
	orderSuffix := ""
	if params.Order == "desc" {
		orderSuffix = " DESC"
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT COALESCE(strftime('%Y-%m', `+currentCatalogAddedExpr+`), ''), COUNT(*)
		FROM games
		`+whereSQL+`
		GROUP BY 1
		ORDER BY 1`+orderSuffix, args...)
	if err != nil {
		return nil, fmt.Errorf("query added-date index buckets: %w", err)
	}
	defer rows.Close()

	type group struct {
		yearMonth string
		count     int
	}
	var groups []group
	for rows.Next() {
		var item group
		if err := rows.Scan(&item.yearMonth, &item.count); err != nil {
			return nil, fmt.Errorf("scan added-date index bucket: %w", err)
		}
		groups = append(groups, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query added-date index buckets: %w", err)
	}

	var buckets []domain.IndexBucket
	offset := 0
	noDateCount := 0
	for _, group := range groups {
		if group.yearMonth == "" || len(group.yearMonth) < 7 {
			noDateCount += group.count
			continue
		}
		month, err := strconv.Atoi(group.yearMonth[5:7])
		if err != nil || month < 1 || month > 12 {
			noDateCount += group.count
			continue
		}
		buckets = append(buckets, domain.IndexBucket{
			Label:  group.yearMonth[:4] + " " + monthNames[month-1],
			Offset: offset,
		})
		offset += group.count
	}
	if noDateCount > 0 {
		buckets = append(buckets, domain.IndexBucket{Label: "Без даты", Offset: offset})
	}
	return buckets, nil
}

// ListGames возвращает одну нормализованную страницу каталога.
func (r *Repository) ListGames(ctx context.Context, params domain.ListParams) (domain.ListResult, error) {
	params.Normalize()
	whereSQL, args := buildListWhere(params)
	result := domain.ListResult{Page: params.Page, PageSize: params.PageSize}

	if err := r.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM games "+whereSQL,
		args...,
	).Scan(&result.Total); err != nil {
		return result, fmt.Errorf("count catalog games: %w", err)
	}
	result.TotalPages = (result.Total + params.PageSize - 1) / params.PageSize
	if result.TotalPages == 0 {
		params.Page = 1
	} else if params.Page > result.TotalPages {
		params.Page = result.TotalPages
	}
	result.Page = params.Page

	column, ok := sortColumns[params.Sort]
	if !ok {
		column = "title COLLATE NOCASE"
	}
	direction := "ASC"
	if params.Order == "desc" {
		direction = "DESC"
	}
	orderSQL := fmt.Sprintf(
		"ORDER BY (%s IS NULL), %s %s, title COLLATE NOCASE ASC",
		column,
		column,
		direction,
	)
	query := `
SELECT id, title, COALESCE(title_en,''), COALESCE(release_year,0), COALESCE(platforms,''), COALESCE(image_url,''),
       COALESCE(store_url,''), metacritic_score, metacritic_url, metacritic_user_score, metacritic_user_count,
       opencritic_score, opencritic_url, opencritic_player_score, opencritic_player_count,
       average_score, critic_average_score, player_average_score,
       hltb_main_extra, hltb_rating, hltb_url, COALESCE(screen_langs,''), COALESCE(spoken_langs,''),
       ` + currentCatalogAddedExpr + `, ` + currentCatalogAddedSourceExpr + `, ` + currentCatalogSourceURLExpr + `
FROM games ` + whereSQL + " " + orderSQL + " LIMIT ? OFFSET ?"
	args = append(args, params.PageSize, (params.Page-1)*params.PageSize)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("query catalog games: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var game domain.CatalogItem
		var screenLanguages, spokenLanguages string
		var metacritic, metacriticUser, metacriticUserCount sql.NullInt64
		var openCritic, openCriticPlayer, openCriticPlayerCount sql.NullInt64
		var hltbMain, hltbRating sql.NullInt64
		var metacriticURL, openCriticURL, hltbURL sql.NullString
		var average, criticAverage, playerAverage sql.NullFloat64
		var catalogAddedOn sql.NullTime
		var catalogAddedSource, catalogSourceURL sql.NullString
		if err := rows.Scan(
			&game.ID,
			&game.Title,
			&game.TitleEn,
			&game.ReleaseYear,
			&game.Platforms,
			&game.ImageURL,
			&game.StoreURL,
			&metacritic,
			&metacriticURL,
			&metacriticUser,
			&metacriticUserCount,
			&openCritic,
			&openCriticURL,
			&openCriticPlayer,
			&openCriticPlayerCount,
			&average,
			&criticAverage,
			&playerAverage,
			&hltbMain,
			&hltbRating,
			&hltbURL,
			&screenLanguages,
			&spokenLanguages,
			&catalogAddedOn,
			&catalogAddedSource,
			&catalogSourceURL,
		); err != nil {
			return result, fmt.Errorf("scan catalog game: %w", err)
		}
		game.Metacritic = optionalInt64(metacritic)
		game.MetacriticPageURL = optionalString(metacriticURL)
		game.MetacriticUser = optionalInt64(metacriticUser)
		game.MetacriticUserCount = optionalInt64(metacriticUserCount)
		game.OpenCritic = optionalInt64(openCritic)
		game.OpenCriticPageURL = optionalString(openCriticURL)
		game.OpenCriticPlayer = optionalInt64(openCriticPlayer)
		game.OpenCriticPlayerCount = optionalInt64(openCriticPlayerCount)
		game.Average = optionalFloat64(average)
		game.CriticAverage = optionalFloat64(criticAverage)
		game.PlayerAverage = optionalFloat64(playerAverage)
		game.HLTBMainSec = optionalInt64(hltbMain)
		game.HLTBRating = optionalInt64(hltbRating)
		game.HLTBPageURL = optionalString(hltbURL)
		game.CatalogAddedOn = optionalTime(catalogAddedOn)
		game.CatalogAddedSource = optionalString(catalogAddedSource)
		game.CatalogSourceURL = optionalString(catalogSourceURL)
		game.RuSub = strings.Contains(screenLanguages, `"ru"`)
		game.RuVoice = strings.Contains(spokenLanguages, `"ru"`)
		result.Games = append(result.Games, game)
		ids = append(ids, game.ID)
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("query catalog games: %w", err)
	}
	if err := attachGenres(ctx, r.db, result.Games, ids); err != nil {
		return result, err
	}
	if err := attachFavorites(ctx, r.db, result.Games, ids, params.ViewerUserID); err != nil {
		return result, err
	}
	return result, nil
}

func attachGenres(ctx context.Context, db *sql.DB, games []domain.CatalogItem, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := db.QueryContext(
		ctx,
		"SELECT game_id, genre FROM game_genres WHERE game_id IN ("+placeholders+") ORDER BY genre",
		stringsToAny(ids)...,
	)
	if err != nil {
		return fmt.Errorf("query game genres: %w", err)
	}
	defer rows.Close()

	byID := make(map[string][]string, len(games))
	for rows.Next() {
		var gameID, genre string
		if err := rows.Scan(&gameID, &genre); err != nil {
			return fmt.Errorf("scan game genre: %w", err)
		}
		byID[gameID] = append(byID[gameID], genre)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("query game genres: %w", err)
	}
	for i := range games {
		games[i].Genres = byID[games[i].ID]
	}
	return nil
}

func attachFavorites(
	ctx context.Context,
	db *sql.DB,
	games []domain.CatalogItem,
	ids []string,
	userID int64,
) error {
	if userID <= 0 || len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, userID)
	args = append(args, stringsToAny(ids)...)
	rows, err := db.QueryContext(ctx, `
SELECT game_id
FROM user_favorites
WHERE user_id = ? AND game_id IN (`+placeholders+")", args...)
	if err != nil {
		return fmt.Errorf("query user favorites: %w", err)
	}
	defer rows.Close()

	favorites := make(map[string]bool, len(ids))
	for rows.Next() {
		var gameID string
		if err := rows.Scan(&gameID); err != nil {
			return fmt.Errorf("scan user favorite: %w", err)
		}
		favorites[gameID] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("query user favorites: %w", err)
	}
	for index := range games {
		games[index].Favorite = favorites[games[index].ID]
	}
	return nil
}

func (r *Repository) DistinctYears(ctx context.Context) ([]int, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT DISTINCT release_year
FROM games
WHERE active = 1 AND release_year > 0
ORDER BY release_year DESC`)
	if err != nil {
		return nil, fmt.Errorf("query distinct years: %w", err)
	}
	defer rows.Close()

	var years []int
	for rows.Next() {
		var year int
		if err := rows.Scan(&year); err != nil {
			return nil, fmt.Errorf("scan distinct year: %w", err)
		}
		years = append(years, year)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query distinct years: %w", err)
	}
	return years, nil
}

func (r *Repository) DistinctGenres(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT DISTINCT genre
FROM game_genres
WHERE game_id IN (SELECT id FROM games WHERE active = 1)
ORDER BY genre`)
	if err != nil {
		return nil, fmt.Errorf("query distinct genres: %w", err)
	}
	defer rows.Close()

	var genres []string
	for rows.Next() {
		var genre string
		if err := rows.Scan(&genre); err != nil {
			return nil, fmt.Errorf("scan distinct genre: %w", err)
		}
		genres = append(genres, genre)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query distinct genres: %w", err)
	}
	return genres, nil
}

func optionalInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func optionalFloat64(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}

func optionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func optionalTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}
