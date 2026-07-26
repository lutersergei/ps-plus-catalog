package handlers

import (
	"html/template"
	"net/url"
	"strings"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

type optionalInt struct {
	Int64 int64
	Valid bool
}

type optionalFloat struct {
	Float64 float64
	Valid   bool
}

type optionalString struct {
	String string
	Valid  bool
}

type optionalTime struct {
	Time  time.Time
	Valid bool
}

type gameView struct {
	ID                    string
	Title                 string
	TitleEn               string
	ReleaseYear           int
	Genres                []string
	Platforms             string
	ImageURL              string
	StoreURL              string
	Metacritic            optionalInt
	MetacriticPageURL     optionalString
	MetacriticUser        optionalInt
	MetacriticUserCount   optionalInt
	OpenCritic            optionalInt
	OpenCriticPlayer      optionalInt
	OpenCriticPlayerCount optionalInt
	OpenCriticPageURL     optionalString
	Average               optionalFloat
	CriticAverage         optionalFloat
	PlayerAverage         optionalFloat
	HLTBMainSec           optionalInt
	HLTBRating            optionalInt
	HLTBPageURL           optionalString
	CatalogAddedOn        optionalTime
	CatalogAddedSource    optionalString
	CatalogSourceURL      optionalString
	RuSub                 bool
	RuVoice               bool
}

type viewListResult struct {
	Games      []gameView
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

type pageData struct {
	Result     viewListResult
	Years      []int
	Genres     []string
	Params     domain.ListParams
	BaseQuery  template.URL
	Buckets    []domain.IndexBucket
	NextOffset int
	HasNext    bool
}

func toGameView(game domain.CatalogItem) gameView {
	return gameView{
		ID: game.ID, Title: game.Title, TitleEn: game.TitleEn,
		ReleaseYear: game.ReleaseYear, Genres: game.Genres,
		Platforms: game.Platforms, ImageURL: game.ImageURL, StoreURL: game.StoreURL,
		Metacritic:            optionalIntValue(game.Metacritic),
		MetacriticPageURL:     optionalStringValue(game.MetacriticPageURL),
		MetacriticUser:        optionalIntValue(game.MetacriticUser),
		MetacriticUserCount:   optionalIntValue(game.MetacriticUserCount),
		OpenCritic:            optionalIntValue(game.OpenCritic),
		OpenCriticPlayer:      optionalIntValue(game.OpenCriticPlayer),
		OpenCriticPlayerCount: optionalIntValue(game.OpenCriticPlayerCount),
		OpenCriticPageURL:     optionalStringValue(game.OpenCriticPageURL),
		Average:               optionalFloatValue(game.Average),
		CriticAverage:         optionalFloatValue(game.CriticAverage),
		PlayerAverage:         optionalFloatValue(game.PlayerAverage),
		HLTBMainSec:           optionalIntValue(game.HLTBMainSec),
		HLTBRating:            optionalIntValue(game.HLTBRating),
		HLTBPageURL:           optionalStringValue(game.HLTBPageURL),
		CatalogAddedOn:        optionalTimeValue(game.CatalogAddedOn),
		CatalogAddedSource:    optionalStringValue(game.CatalogAddedSource),
		CatalogSourceURL:      trustedCatalogSource(game.CatalogSourceURL),
		RuSub:                 game.RuSub, RuVoice: game.RuVoice,
	}
}

// HLTBHours возвращает длительность Main+Sides в часах.
func (game gameView) HLTBHours() float64 {
	if !game.HLTBMainSec.Valid {
		return 0
	}
	return float64(game.HLTBMainSec.Int64) / 3600
}

// CatalogAddedShortLabel форматирует дату добавления для компактного бейджа.
func (game gameView) CatalogAddedShortLabel() string {
	if !game.CatalogAddedOn.Valid {
		return ""
	}
	return game.CatalogAddedOn.Time.Format("02.01.06")
}

// CatalogAddedTitle поясняет происхождение даты добавления.
func (game gameView) CatalogAddedTitle() string {
	if !game.CatalogAddedOn.Valid {
		return ""
	}
	switch game.CatalogAddedSource.String {
	case "announcement":
		return "Дата добавления по официальному анонсу PlayStation"
	case "verified":
		return "Дата добавления подтверждена историческим источником"
	default:
		return "Дата первого наблюдения в каталоге"
	}
}

// RuStoreURL возвращает русскую страницу PS Store только для доверенного адреса.
func (game gameView) RuStoreURL() string {
	if game.StoreURL == "" {
		return ""
	}
	if !trustedHTTPSURL(game.StoreURL, "store.playstation.com") {
		return "https://store.playstation.com/ru-ua/"
	}
	return strings.Replace(game.StoreURL, "/tr-tr/", "/ru-ua/", 1)
}

// OCPlayerWeight возвращает вес пользовательской оценки OpenCritic в среднем.
func (game gameView) OCPlayerWeight() float64 {
	if !game.OpenCriticPlayer.Valid || game.OpenCriticPlayer.Int64 <= 0 {
		return 0
	}
	count := game.OpenCriticPlayerCount.Int64
	switch {
	case count < 20:
		return 0
	case count > 100:
		return 1
	default:
		return 0.5
	}
}

// OCWeightGlyph возвращает обозначение веса OpenCritic для шаблона.
func (game gameView) OCWeightGlyph() string {
	if !game.OpenCriticPlayer.Valid && !game.OpenCriticPlayerCount.Valid {
		return ""
	}
	switch game.OCPlayerWeight() {
	case 1:
		return "●"
	case 0.5:
		return "◐"
	default:
		return "○"
	}
}

// MetacriticURL возвращает доверенную прямую ссылку или безопасный поиск.
func (game gameView) MetacriticURL() string {
	if game.MetacriticPageURL.Valid && trustedHTTPSURL(game.MetacriticPageURL.String, "www.metacritic.com") {
		return game.MetacriticPageURL.String
	}
	return "https://www.metacritic.com/search/" + url.PathEscape(game.searchTerm()) + "/"
}

// OpenCriticURL возвращает доверенную прямую ссылку или безопасный поиск.
func (game gameView) OpenCriticURL() string {
	if game.OpenCriticPageURL.Valid && trustedHTTPSURL(game.OpenCriticPageURL.String, "opencritic.com") {
		return game.OpenCriticPageURL.String
	}
	return "https://opencritic.com/search?term=" + url.QueryEscape(game.searchTerm())
}

// HLTBURL возвращает доверенную прямую ссылку или безопасный поиск.
func (game gameView) HLTBURL() string {
	if game.HLTBPageURL.Valid && trustedHTTPSURL(game.HLTBPageURL.String, "howlongtobeat.com") {
		return game.HLTBPageURL.String
	}
	return "https://howlongtobeat.com/?q=" + url.QueryEscape(game.searchTerm())
}

func (game gameView) searchTerm() string {
	title := game.TitleEn
	if title == "" {
		title = game.Title
	}
	return strings.TrimSpace(strings.NewReplacer("™", "", "®", "", "’", "'").Replace(title))
}

func trustedHTTPSURL(raw string, allowedHosts ...string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return false
	}
	for _, host := range allowedHosts {
		if strings.EqualFold(parsed.Hostname(), host) {
			return true
		}
	}
	return false
}

func trustedCatalogSource(value *string) optionalString {
	if value == nil || !trustedHTTPSURL(
		*value,
		"blog.playstation.com",
		"store.playstation.com",
		"web.archive.org",
		"www.playstationlifestyle.net",
	) {
		return optionalString{}
	}
	return optionalString{String: *value, Valid: true}
}

func optionalIntValue(value *int64) optionalInt {
	if value == nil {
		return optionalInt{}
	}
	return optionalInt{Int64: *value, Valid: true}
}

func optionalFloatValue(value *float64) optionalFloat {
	if value == nil {
		return optionalFloat{}
	}
	return optionalFloat{Float64: *value, Valid: true}
}

func optionalStringValue(value *string) optionalString {
	if value == nil {
		return optionalString{}
	}
	return optionalString{String: *value, Valid: true}
}

func optionalTimeValue(value *time.Time) optionalTime {
	if value == nil {
		return optionalTime{}
	}
	return optionalTime{Time: *value, Valid: true}
}
