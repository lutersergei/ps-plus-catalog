// Package psstore получает каталог игр PS Plus из публичного эндпоинта
// playstation.com/bin/imagic/gameslist и разбирает его в доменные структуры.
package psstore

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Game — одна игра каталога PS Plus.
type Game struct {
	ID          string   // productId — уникальный идентификатор конкретного издания
	Title       string   // локализованное название (для отображения)
	TitleEn     string   // английское название (для матчинга оценок)
	ReleaseYear int      // год из releaseDate (0, если не распознан)
	Genres      []string // жанры без дублей, в исходном порядке
	Platforms   []string // device, напр. ["PS4","PS5"]
	ImageURL    string
	StoreURL    string // conceptUrl
}

// rawGroup и rawGame повторяют форму ответа gameslist.
type rawGroup struct {
	CatalogKey string    `json:"catalogKey"`
	Count      int       `json:"count"`
	Games      []rawGame `json:"games"`
}

type rawGame struct {
	ConceptID   json.Number `json:"conceptId"`
	ProductID   string      `json:"productId"`
	Name        string      `json:"name"`
	NameEn      string      `json:"nameEn"`
	ConceptURL  string      `json:"conceptUrl"`
	ImageURL    string      `json:"imageUrl"`
	Genre       []string    `json:"genre"`
	ReleaseDate string      `json:"releaseDate"`
	Device      []string    `json:"device"`
}

// parseGamesList разбирает ответ gameslist (массив алфавитных групп) в плоский
// список игр. Жанры дедуплицируются, год извлекается из releaseDate.
//
// Снимок проверяется на целостность: пустой результат, пустой productId/name,
// дубли productId и расхождение числа игр в группе с её полем count считаются
// ошибкой формата. Это защищает от частичного/изменившегося ответа upstream,
// который иначе массово деактивировал бы игры в каталоге (см. syncCatalog).
func parseGamesList(raw []byte) ([]Game, error) {
	var groups []rawGroup
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("parse gameslist: %w", err)
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("parse gameslist: пустой ответ (нет групп)")
	}
	var games []Game
	seen := make(map[string]string) // productId → name, для отлова дублей
	for _, grp := range groups {
		// Группа объявляет ожидаемое число игр — сверяем, чтобы поймать
		// усечённый/частичный ответ.
		if grp.Count != len(grp.Games) {
			return nil, fmt.Errorf("parse gameslist: группа %q объявляет count=%d, но содержит %d игр",
				grp.CatalogKey, grp.Count, len(grp.Games))
		}
		for _, g := range grp.Games {
			if g.ProductID == "" {
				return nil, fmt.Errorf("parse gameslist: пустой productId у игры %q (группа %q)", g.Name, grp.CatalogKey)
			}
			if g.Name == "" {
				return nil, fmt.Errorf("parse gameslist: пустой name у игры с productId %q", g.ProductID)
			}
			if prev, dup := seen[g.ProductID]; dup {
				return nil, fmt.Errorf("parse gameslist: дублирующийся productId %q (%q и %q)", g.ProductID, prev, g.Name)
			}
			seen[g.ProductID] = g.Name
			games = append(games, Game{
				ID:          g.ProductID,
				Title:       g.Name,
				TitleEn:     g.NameEn,
				ReleaseYear: yearFromISO(g.ReleaseDate),
				Genres:      dedupe(g.Genre),
				Platforms:   g.Device,
				ImageURL:    g.ImageURL,
				StoreURL:    g.ConceptURL,
			})
		}
	}
	return games, nil
}

// yearFromISO возвращает год из ISO8601-даты или 0, если разобрать не удалось.
func yearFromISO(s string) int {
	if s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Year()
	}
	// запасной разбор первых 4 цифр
	if len(s) >= 4 {
		if y, err := strconv.Atoi(s[:4]); err == nil {
			return y
		}
	}
	return 0
}

// dedupe удаляет повторы из списка жанров, сохраняя порядок.
func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
