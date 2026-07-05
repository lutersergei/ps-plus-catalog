package main

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"flag"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/store"
)

//go:embed templates/index.html
var indexHTML string

const pageSize = 24

// Шкала полосы времени прохождения на карточке (0–60 ч) и максимум слайдера
// фильтра по времени. Значение слайдера на правом краю означает «без верхней
// границы» — см. normalizeSliderBounds.
const (
	hltbScaleHours     = 60
	hltbSliderMaxHours = 80
)

// newIndexTemplate парсит встроенный шаблон страницы со всеми функциями,
// которые он использует. Общая точка для сервера и тестов.
func newIndexTemplate() (*template.Template, error) {
	return template.New("index").Funcs(template.FuncMap{
		"add":        func(a, b int) int { return a + b },
		"mul":        func(a, b int) int { return a * b },
		"scoreClass": scoreClass,
		"fmtCount":   fmtCount,
		"hltbPct":    hltbPct,
		"hltbOver":   hltbOver,
	}).Parse(indexHTML)
}

// scoreClass — CSS-класс цвета оценки: зелёный от 75, охра от 50, ниже — красный.
func scoreClass(v float64) string {
	switch {
	case v >= 75:
		return "good"
	case v >= 50:
		return "mid"
	default:
		return "bad"
	}
}

// fmtCount форматирует число голосов компактно: до 999 как есть, дальше в
// тысячах с одним знаком («2,3к»), без хвоста «,0» («3к»).
func fmtCount(n int64) string {
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	tenths := (n + 50) / 100
	if tenths%10 == 0 {
		return strconv.FormatInt(tenths/10, 10) + "к"
	}
	return strconv.FormatInt(tenths/10, 10) + "," + strconv.FormatInt(tenths%10, 10) + "к"
}

// hltbPct — заполнение полосы времени в процентах шкалы 0–60 ч (клампится к 100).
func hltbPct(hours float64) int {
	pct := int(hours/hltbScaleHours*100 + 0.5)
	if pct > 100 {
		return 100
	}
	if pct < 0 {
		return 0
	}
	return pct
}

// hltbOver сообщает, что игра длиннее шкалы полосы времени.
func hltbOver(hours float64) bool {
	return hours > hltbScaleHours
}

// normalizeSliderBounds трактует правый край range-слайдеров как «фильтр не
// задан»: иначе critic_to=100 из всегда отправляемого слайдера отсекал бы игры
// без оценок (NULL не проходит сравнение <=).
func normalizeSliderBounds(p *store.ListParams) {
	if p.CriticTo >= 100 {
		p.CriticTo = 0
	}
	if p.PlayerTo >= 100 {
		p.PlayerTo = 0
	}
	if p.HLTBToHours >= hltbSliderMaxHours {
		p.HLTBToHours = 0
	}
}

type pageData struct {
	Result     store.ListResult
	Years      []int
	Genres     []string
	Params     store.ListParams
	BaseQuery  template.URL // query без page/offset — для ссылок ленты и индекса
	Letters    []store.LetterBucket
	NextOffset int // смещение следующей партии для «Показать ещё»
	HasNext    bool
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "ps-extra.db", "путь к файлу SQLite")
	// По умолчанию слушаем только localhost. Для внешнего доступа (Docker и т.п.)
	// задайте -addr :8080 явно и поставьте перед сервисом reverse proxy/TLS.
	addr := fs.String("addr", "127.0.0.1:8080", "адрес HTTP-сервера (напр. 127.0.0.1:8080 или :8080 для всех интерфейсов)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	tmpl, err := newIndexTemplate()
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		handleIndex(w, r, db, tmpl)
	})

	srv := &http.Server{
		Addr:         *addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		log.Printf("слушаю %s (db=%s)", displayURL(*addr), *dbPath)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		stop() // вернуть стандартную обработку повторного сигнала
		log.Println("получен сигнал завершения, останавливаю сервер…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		// дождаться выхода ListenAndServe (вернёт ErrServerClosed)
		if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// displayURL строит человекочитаемый адрес для лога: для ":8080" подставляет
// localhost, для явного host:port — оставляет как есть.
func displayURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}

func handleIndex(w http.ResponseWriter, r *http.Request, db *sql.DB, tmpl *template.Template) {
	q := r.URL.Query()

	// Мультивыбор жанров: несколько значений параметра genre
	rawGenres := q["genre"]
	var genres []string
	for _, g := range rawGenres {
		if g != "" {
			genres = append(genres, g)
		}
	}

	p := store.ListParams{
		Search:        strings.TrimSpace(q.Get("q")),
		Genres:        genres,
		YearFrom:      atoiDefault(q.Get("year_from"), 0),
		YearTo:        atoiDefault(q.Get("year_to"), 0),
		AvgFrom:       atofDefault(q.Get("avg_from"), 0),
		AvgTo:         atofDefault(q.Get("avg_to"), 0),
		CriticFrom:    atofDefault(q.Get("critic_from"), 0),
		CriticTo:      atofDefault(q.Get("critic_to"), 0),
		PlayerFrom:    atofDefault(q.Get("player_from"), 0),
		PlayerTo:      atofDefault(q.Get("player_to"), 0),
		HLTBFromHours: atofDefault(q.Get("hltb_from"), 0),
		HLTBToHours:   atofDefault(q.Get("hltb_to"), 0),
		Sort:          orDefault(q.Get("sort"), "title"),
		Order:         orDefault(q.Get("order"), "asc"),
		Page:          atoiDefault(q.Get("page"), 1),
		PageSize:      pageSize,
		RuSubtitles:   q.Get("ru_sub") == "1",
		RuVoice:       q.Get("ru_voice") == "1",
	}
	// offset — альтернатива page для бесконечной ленты: число строк от начала
	// выдачи. Округляется вниз до границы страницы; точную карточку внутри
	// партии докручивает клиентский JS.
	if off := atoiDefault(q.Get("offset"), -1); off >= 0 {
		p.Page = off/pageSize + 1
	}
	// Нормализуем параметры здесь, до построения формы и ссылок пагинации, чтобы
	// отображаемые значения и query в ссылках совпадали с тем, что уйдёт в SQL
	// (обрезка длинного поиска, лишних жанров, перевёрнутых диапазонов). Верхний
	// клампинг номера страницы делает ListGames; форма берёт его из result.Page.
	normalizeSliderBounds(&p)
	store.NormalizeParams(&p)

	result, err := store.ListGames(db, p)
	if err != nil {
		log.Printf("list games: %v", err)
		http.Error(w, "внутренняя ошибка сервера", http.StatusInternalServerError)
		return
	}

	// Режим фрагмента: только карточки для бесконечной ленты. Общее число —
	// в заголовке, чтобы клиент обновлял счётчик без парсинга HTML.
	if q.Get("fragment") == "cards" {
		w.Header().Set("X-Total", strconv.Itoa(result.Total))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, "cards", pageData{Result: result}); err != nil {
			log.Printf("render fragment: %v", err)
		}
		return
	}

	years, err := store.DistinctYears(db)
	if err != nil {
		log.Printf("distinct years: %v", err)
		http.Error(w, "внутренняя ошибка сервера", http.StatusInternalServerError)
		return
	}
	genreList, err := store.DistinctGenres(db)
	if err != nil {
		log.Printf("distinct genres: %v", err)
		http.Error(w, "внутренняя ошибка сервера", http.StatusInternalServerError)
		return
	}

	// Буквенный индекс имеет смысл только при сортировке по названию.
	var letters []store.LetterBucket
	if p.Sort == "title" {
		letters, err = store.TitleLetterBuckets(db, p)
		if err != nil {
			log.Printf("letter buckets: %v", err)
			http.Error(w, "внутренняя ошибка сервера", http.StatusInternalServerError)
			return
		}
	}

	// BaseQuery — query без page, для ссылок пагинации
	base := url.Values{}
	if p.Search != "" {
		base.Set("q", p.Search)
	}
	if p.YearFrom > 0 {
		base.Set("year_from", strconv.Itoa(p.YearFrom))
	}
	if p.YearTo > 0 {
		base.Set("year_to", strconv.Itoa(p.YearTo))
	}
	// Несколько жанров через Add (не Set, иначе перезапишет)
	for _, g := range p.Genres {
		base.Add("genre", g)
	}
	if p.AvgFrom > 0 {
		base.Set("avg_from", strconv.FormatFloat(p.AvgFrom, 'f', -1, 64))
	}
	if p.AvgTo > 0 {
		base.Set("avg_to", strconv.FormatFloat(p.AvgTo, 'f', -1, 64))
	}
	if p.CriticFrom > 0 {
		base.Set("critic_from", strconv.FormatFloat(p.CriticFrom, 'f', -1, 64))
	}
	if p.CriticTo > 0 {
		base.Set("critic_to", strconv.FormatFloat(p.CriticTo, 'f', -1, 64))
	}
	if p.PlayerFrom > 0 {
		base.Set("player_from", strconv.FormatFloat(p.PlayerFrom, 'f', -1, 64))
	}
	if p.PlayerTo > 0 {
		base.Set("player_to", strconv.FormatFloat(p.PlayerTo, 'f', -1, 64))
	}
	if p.HLTBFromHours > 0 {
		base.Set("hltb_from", strconv.FormatFloat(p.HLTBFromHours, 'f', -1, 64))
	}
	if p.HLTBToHours > 0 {
		base.Set("hltb_to", strconv.FormatFloat(p.HLTBToHours, 'f', -1, 64))
	}
	base.Set("sort", p.Sort)
	base.Set("order", p.Order)
	if p.RuSubtitles {
		base.Set("ru_sub", "1")
	}
	if p.RuVoice {
		base.Set("ru_voice", "1")
	}

	data := pageData{
		Result:     result,
		Years:      years,
		Genres:     genreList,
		Params:     p,
		BaseQuery:  template.URL(base.Encode()),
		Letters:    letters,
		NextOffset: result.Page * result.PageSize,
		HasNext:    result.Page < result.TotalPages,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("render: %v", err)
	}
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func atofDefault(s string, def float64) float64 {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return def
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
