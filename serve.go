package main

import (
	"database/sql"
	_ "embed"
	"flag"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"ps-extra/internal/store"
)

//go:embed templates/index.html
var indexHTML string

const pageSize = 24

type pageData struct {
	Result    store.ListResult
	Years     []int
	Genres    []string
	Params    store.ListParams
	BaseQuery template.URL // query без page — для ссылок пагинации
	Pages     []int        // окно номеров страниц
	HasPrev   bool
	HasNext   bool
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "ps-extra.db", "путь к файлу SQLite")
	addr := fs.String("addr", ":8081", "адрес HTTP-сервера")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	tmpl, err := template.New("index").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
	}).Parse(indexHTML)
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

	log.Printf("слушаю http://localhost%s (db=%s)", *addr, *dbPath)
	return http.ListenAndServe(*addr, mux)
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
		Genres:        genres,
		YearFrom:      atoiDefault(q.Get("year_from"), 0),
		YearTo:        atoiDefault(q.Get("year_to"), 0),
		AvgFrom:       atofDefault(q.Get("avg_from"), 0),
		AvgTo:         atofDefault(q.Get("avg_to"), 0),
		HLTBFromHours: atofDefault(q.Get("hltb_from"), 0),
		HLTBToHours:   atofDefault(q.Get("hltb_to"), 0),
		Sort:          orDefault(q.Get("sort"), "title"),
		Order:         orDefault(q.Get("order"), "asc"),
		Page:          atoiDefault(q.Get("page"), 1),
		PageSize:      pageSize,
	}

	result, err := store.ListGames(db, p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	years, err := store.DistinctYears(db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	genreList, err := store.DistinctGenres(db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// BaseQuery — query без page, для ссылок пагинации
	base := url.Values{}
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
	if p.HLTBFromHours > 0 {
		base.Set("hltb_from", strconv.FormatFloat(p.HLTBFromHours, 'f', -1, 64))
	}
	if p.HLTBToHours > 0 {
		base.Set("hltb_to", strconv.FormatFloat(p.HLTBToHours, 'f', -1, 64))
	}
	base.Set("sort", p.Sort)
	base.Set("order", p.Order)

	data := pageData{
		Result:    result,
		Years:     years,
		Genres:    genreList,
		Params:    p,
		BaseQuery: template.URL(base.Encode()),
		Pages:     pageWindow(result.Page, result.TotalPages),
		HasPrev:   result.Page > 1,
		HasNext:   result.Page < result.TotalPages,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("render: %v", err)
	}
}

// pageWindow возвращает номера страниц вокруг текущей (максимум 9).
func pageWindow(current, total int) []int {
	if total < 1 {
		return nil
	}
	const span = 4
	start, end := current-span, current+span
	if start < 1 {
		start = 1
	}
	if end > total {
		end = total
	}
	pages := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		pages = append(pages, i)
	}
	return pages
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
