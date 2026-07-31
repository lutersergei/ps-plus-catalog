// Package handlers содержит HTTP-представление каталога.
package handlers

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

const (
	pageSize           = 25
	hltbScaleHours     = 60
	hltbSliderMaxHours = 80
)

// CatalogBrowser описывает сценарий, необходимый HTTP-обработчику.
type CatalogBrowser interface {
	Browse(context.Context, domain.ListParams, bool) (domain.BrowseResult, error)
}

// CatalogHandler разбирает HTTP-запрос и отображает каталог встроенным шаблоном.
type CatalogHandler struct {
	browser       CatalogBrowser
	template      *template.Template
	logger        *slog.Logger
	accounts      AccountManager
	google        GoogleOAuth
	basePath      string
	secureCookies bool
}

// NewCatalogHandler проверяет шаблон и создаёт обработчик каталога.
func NewCatalogHandler(templateSource string, browser CatalogBrowser, logger *slog.Logger) (*CatalogHandler, error) {
	return NewCatalogHandlerWithAuth(templateSource, browser, AuthConfig{}, logger)
}

// NewCatalogHandlerWithAuth создаёт каталог с Google OAuth и избранным.
func NewCatalogHandlerWithAuth(
	templateSource string,
	browser CatalogBrowser,
	auth AuthConfig,
	logger *slog.Logger,
) (*CatalogHandler, error) {
	if err := validateAuthConfig(auth); err != nil {
		return nil, err
	}
	parsed, err := template.New("index").Funcs(template.FuncMap{
		"add":        func(a, b int) int { return a + b },
		"mul":        func(a, b int) int { return a * b },
		"scoreClass": scoreClass,
		"fmtCount":   formatCount,
		"hltbPct":    hltbPercent,
		"hltbOver":   hltbOver,
	}).Parse(templateSource)
	if err != nil {
		return nil, fmt.Errorf("разобрать шаблон каталога: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &CatalogHandler{
		browser: browser, template: parsed, logger: logger,
		accounts: auth.Accounts, google: auth.Google,
		basePath: auth.BasePath, secureCookies: auth.SecureCookies,
	}, nil
}

// ServeHTTP обслуживает полную страницу и фрагменты карточек для бесконечной
// ленты. Остальные пути возвращают 404.
func (h *CatalogHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(response.Header())
	switch request.URL.Path {
	case "/":
		h.handleCatalog(response, request, false)
	case "/favorites":
		if !h.authEnabled() {
			http.NotFound(response, request)
			return
		}
		h.handleCatalog(response, request, true)
	case "/auth/google/login":
		if !h.authEnabled() {
			http.NotFound(response, request)
			return
		}
		h.handleGoogleLogin(response, request)
	case "/auth/google/callback":
		if !h.authEnabled() {
			http.NotFound(response, request)
			return
		}
		h.handleGoogleCallback(response, request)
	case "/auth/logout":
		if !h.authEnabled() {
			http.NotFound(response, request)
			return
		}
		h.handleLogout(response, request)
	case "/api/favorite":
		if !h.authEnabled() {
			http.NotFound(response, request)
			return
		}
		h.handleFavorite(response, request)
	default:
		http.NotFound(response, request)
	}
}

func (h *CatalogHandler) handleCatalog(
	response http.ResponseWriter,
	request *http.Request,
	favoritesOnly bool,
) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(response, "метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	account, authenticated, err := h.currentAccount(response, request)
	if err != nil {
		http.Error(response, "внутренняя ошибка сервера", http.StatusInternalServerError)
		return
	}
	if favoritesOnly && !authenticated {
		http.Redirect(response, request, h.externalPath("/auth/google/login")+"?next=favorites", http.StatusSeeOther)
		return
	}

	params := listParams(request.URL.Query())
	if authenticated {
		params.ViewerUserID = account.User.ID
	}
	params.FavoritesOnly = favoritesOnly
	fragment := request.URL.Query().Get("fragment") == "cards"
	browse, err := h.browser.Browse(request.Context(), params, !fragment)
	if err != nil {
		h.logger.Error("не удалось получить каталог", "error", err)
		http.Error(response, "внутренняя ошибка сервера", http.StatusInternalServerError)
		return
	}

	data := newPageData(browse, params)
	data.AuthEnabled = h.authEnabled()
	data.BasePath = h.basePath
	data.CatalogPath = h.externalPath("/")
	data.FavoritesPath = h.externalPath("/favorites")
	data.LoginPath = h.externalPath("/auth/google/login")
	data.LogoutPath = h.externalPath("/auth/logout")
	data.FavoriteAPIPath = h.externalPath("/api/favorite")
	data.CurrentPath = data.CatalogPath
	data.FavoritesOnly = favoritesOnly
	if favoritesOnly {
		data.CurrentPath = data.FavoritesPath
	}
	if authenticated {
		user := toUserView(account.User)
		data.User = &user
		data.CSRFToken = account.CSRFToken
	}
	var rendered bytes.Buffer
	if fragment {
		if err := h.template.ExecuteTemplate(&rendered, "cards", data); err != nil {
			h.logger.Error("не удалось отобразить фрагмент каталога", "error", err)
			http.Error(response, "внутренняя ошибка сервера", http.StatusInternalServerError)
			return
		}
		response.Header().Set("X-Total", strconv.Itoa(browse.Result.Total))
	} else if err := h.template.Execute(&rendered, data); err != nil {
		h.logger.Error("не удалось отобразить каталог", "error", err)
		http.Error(response, "внутренняя ошибка сервера", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return
	}
	if _, err := response.Write(rendered.Bytes()); err != nil {
		h.logger.Debug("соединение закрыто во время отправки каталога", "error", err)
	}
}

func setSecurityHeaders(header http.Header) {
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}

func listParams(query url.Values) domain.ListParams {
	params := domain.ListParams{
		Search:        query.Get("q"),
		Genres:        append([]string(nil), query["genre"]...),
		YearFrom:      intValue(query.Get("year_from"), 0),
		YearTo:        intValue(query.Get("year_to"), 0),
		AvgFrom:       floatValue(query.Get("avg_from"), 0),
		AvgTo:         floatValue(query.Get("avg_to"), 0),
		CriticFrom:    floatValue(query.Get("critic_from"), 0),
		CriticTo:      floatValue(query.Get("critic_to"), 0),
		PlayerFrom:    floatValue(query.Get("player_from"), 0),
		PlayerTo:      floatValue(query.Get("player_to"), 0),
		ReviewsFrom:   intValue(query.Get("reviews_from"), 0),
		ReviewsTo:     intValue(query.Get("reviews_to"), 0),
		HLTBFromHours: floatValue(query.Get("hltb_from"), 0),
		HLTBToHours:   floatValue(query.Get("hltb_to"), 0),
		Sort:          defaultValue(query.Get("sort"), "title"),
		Order:         defaultValue(query.Get("order"), "asc"),
		Page:          intValue(query.Get("page"), 1),
		PageSize:      pageSize,
		RuSubtitles:   query.Get("ru_sub") == "1",
		RuVoice:       query.Get("ru_voice") == "1",
	}
	if offset := intValue(query.Get("offset"), -1); offset >= 0 {
		params.Page = offset/pageSize + 1
	}
	normalizeSliderBounds(&params)
	params.Normalize()
	return params
}

// normalizeSliderBounds трактует правую границу слайдера как отсутствие
// верхнего ограничения, чтобы значения NULL не выпадали из выдачи.
func normalizeSliderBounds(params *domain.ListParams) {
	if params.CriticTo >= 100 {
		params.CriticTo = 0
	}
	if params.PlayerTo >= 100 {
		params.PlayerTo = 0
	}
	if params.HLTBToHours >= hltbSliderMaxHours {
		params.HLTBToHours = 0
	}
}

func intValue(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func floatValue(value string, fallback float64) float64 {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func defaultValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func scoreClass(value float64) string {
	switch {
	case value >= 75:
		return "good"
	case value >= 50:
		return "mid"
	default:
		return "bad"
	}
}

func formatCount(value int64) string {
	if value < 1000 {
		return strconv.FormatInt(value, 10)
	}
	tenths := (value + 50) / 100
	if tenths%10 == 0 {
		return strconv.FormatInt(tenths/10, 10) + "к"
	}
	return strconv.FormatInt(tenths/10, 10) + "," + strconv.FormatInt(tenths%10, 10) + "к"
}

func hltbPercent(hours float64) int {
	percent := int(hours/hltbScaleHours*100 + 0.5)
	if percent > 100 {
		return 100
	}
	if percent < 0 {
		return 0
	}
	return percent
}

func hltbOver(hours float64) bool { return hours > hltbScaleHours }

func baseQuery(params domain.ListParams) template.URL {
	values := url.Values{}
	if params.Search != "" {
		values.Set("q", params.Search)
	}
	setPositiveInt(values, "year_from", params.YearFrom)
	setPositiveInt(values, "year_to", params.YearTo)
	for _, genre := range params.Genres {
		values.Add("genre", genre)
	}
	setPositiveFloat(values, "avg_from", params.AvgFrom)
	setPositiveFloat(values, "avg_to", params.AvgTo)
	setPositiveFloat(values, "critic_from", params.CriticFrom)
	setPositiveFloat(values, "critic_to", params.CriticTo)
	setPositiveFloat(values, "player_from", params.PlayerFrom)
	setPositiveFloat(values, "player_to", params.PlayerTo)
	setPositiveInt(values, "reviews_from", params.ReviewsFrom)
	setPositiveInt(values, "reviews_to", params.ReviewsTo)
	setPositiveFloat(values, "hltb_from", params.HLTBFromHours)
	setPositiveFloat(values, "hltb_to", params.HLTBToHours)
	values.Set("sort", params.Sort)
	values.Set("order", params.Order)
	if params.RuSubtitles {
		values.Set("ru_sub", "1")
	}
	if params.RuVoice {
		values.Set("ru_voice", "1")
	}
	// url.Values.Encode кодирует каждое недоверенное значение; после этого строку
	// можно пометить как URL, не ломая разделители запроса в html/template.
	return template.URL(values.Encode())
}

func setPositiveInt(values url.Values, key string, value int) {
	if value > 0 {
		values.Set(key, strconv.Itoa(value))
	}
}

func setPositiveFloat(values url.Values, key string, value float64) {
	if value > 0 {
		values.Set(key, strconv.FormatFloat(value, 'f', -1, 64))
	}
}

func newPageData(browse domain.BrowseResult, params domain.ListParams) pageData {
	result := viewListResult{
		Total: browse.Result.Total, Page: browse.Result.Page,
		PageSize: browse.Result.PageSize, TotalPages: browse.Result.TotalPages,
		Games: make([]gameView, 0, len(browse.Result.Games)),
	}
	for _, game := range browse.Result.Games {
		result.Games = append(result.Games, toGameView(game))
	}
	return pageData{
		Result: result, Years: browse.Years, Genres: browse.Genres, Params: params,
		BaseQuery: baseQuery(params), Buckets: browse.Buckets,
		NextOffset: result.Page * result.PageSize,
		HasNext:    result.Page < result.TotalPages,
	}
}
