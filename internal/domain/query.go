package domain

import (
	"strings"
	"time"
)

const (
	defaultPageSize = 24
	maxSearchRunes  = 200
	maxGenres       = 50
)

// ListParams задаёт фильтрацию, сортировку и пагинацию каталога.
type ListParams struct {
	Search        string
	Genres        []string
	YearFrom      int
	YearTo        int
	AvgFrom       float64
	AvgTo         float64
	CriticFrom    float64
	CriticTo      float64
	PlayerFrom    float64
	PlayerTo      float64
	ReviewsFrom   int
	ReviewsTo     int
	HLTBFromHours float64
	HLTBToHours   float64
	Sort          string
	Order         string
	Page          int
	PageSize      int
	RuSubtitles   bool
	RuVoice       bool
	// ViewerUserID задаётся сервером после проверки сессии и используется только
	// для отметок избранного. Значение никогда не читается из query string.
	ViewerUserID int64
	// FavoritesOnly ограничивает выдачу избранным текущего пользователя.
	FavoritesOnly bool
}

// Normalize приводит параметры к безопасному и однозначному виду. Метод
// намеренно идемпотентен: его могут вызывать и сервис, и адаптер.
func (p *ListParams) Normalize() {
	p.Search = strings.TrimSpace(p.Search)
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = defaultPageSize
	}
	if runes := []rune(p.Search); len(runes) > maxSearchRunes {
		p.Search = string(runes[:maxSearchRunes])
	}

	genres := make([]string, 0, min(len(p.Genres), maxGenres))
	seenGenres := make(map[string]struct{}, min(len(p.Genres), maxGenres))
	for _, genre := range p.Genres {
		genre = strings.TrimSpace(genre)
		if genre == "" {
			continue
		}
		if _, exists := seenGenres[genre]; exists {
			continue
		}
		seenGenres[genre] = struct{}{}
		genres = append(genres, genre)
		if len(genres) == maxGenres {
			break
		}
	}
	p.Genres = genres

	normalizeIntRange(&p.YearFrom, &p.YearTo)
	normalizeFloatRange(&p.AvgFrom, &p.AvgTo)
	normalizeFloatRange(&p.CriticFrom, &p.CriticTo)
	normalizeFloatRange(&p.PlayerFrom, &p.PlayerTo)
	normalizeIntRange(&p.ReviewsFrom, &p.ReviewsTo)
	normalizeFloatRange(&p.HLTBFromHours, &p.HLTBToHours)

	switch p.Sort {
	case "year", "average", "critic", "player", "title", "hltbmain", "reviews", "added":
	default:
		p.Sort = "title"
	}
	if !strings.EqualFold(p.Order, "desc") {
		p.Order = "asc"
	} else {
		p.Order = "desc"
	}
}

func normalizeIntRange(from, to *int) {
	if *from > 0 && *to > 0 && *to < *from {
		*from, *to = 0, 0
	}
}

func normalizeFloatRange(from, to *float64) {
	if *from > 0 && *to > 0 && *to < *from {
		*from, *to = 0, 0
	}
}

// CatalogItem — игра из сценария чтения каталога. Для необязательных значений
// используются указатели, чтобы доменная модель не зависела от database/sql.
type CatalogItem struct {
	ID                    string
	Title                 string
	TitleEn               string
	ReleaseYear           int
	Genres                []string
	Platforms             string
	ImageURL              string
	StoreURL              string
	Metacritic            *int64
	MetacriticPageURL     *string
	MetacriticUser        *int64
	MetacriticUserCount   *int64
	OpenCritic            *int64
	OpenCriticPlayer      *int64
	OpenCriticPlayerCount *int64
	OpenCriticPageURL     *string
	Average               *float64
	CriticAverage         *float64
	PlayerAverage         *float64
	HLTBMainSec           *int64
	HLTBRating            *int64
	HLTBPageURL           *string
	CatalogAddedOn        *time.Time
	CatalogAddedSource    *string
	CatalogSourceURL      *string
	RuSub                 bool
	RuVoice               bool
	Favorite              bool
}

// ListResult содержит одну страницу каталога и параметры пагинации.
type ListResult struct {
	Games      []CatalogItem
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

// IndexBucket содержит метку быстрого перехода и абсолютное смещение строки.
type IndexBucket struct {
	Label  string
	Offset int
}

// BrowseResult содержит данные, необходимые для полной страницы каталога.
// Для запроса фрагмента используется только Result.
type BrowseResult struct {
	Result  ListResult
	Years   []int
	Genres  []string
	Buckets []IndexBucket
}
