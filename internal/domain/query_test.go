package domain

import (
	"reflect"
	"strings"
	"testing"
)

func TestListParamsNormalizeIsIdempotent(t *testing.T) {
	genres := []string{" Action ", "", "Action", "Adventure"}
	for i := 0; i < maxGenres+5; i++ {
		genres = append(genres, "genre-"+strings.Repeat("x", i+1))
	}
	params := ListParams{
		Search:   "  " + strings.Repeat("я", maxSearchRunes+10) + "  ",
		Genres:   genres,
		YearFrom: 2025, YearTo: 2020,
		Sort: "unknown", Order: "DESC", Page: -5, PageSize: 0,
	}
	params.Normalize()
	first := params
	params.Normalize()

	if !reflect.DeepEqual(params, first) {
		t.Fatalf("повторная нормализация изменила параметры: first=%+v second=%+v", first, params)
	}
	if len([]rune(params.Search)) != maxSearchRunes {
		t.Fatalf("длина поиска=%d, ждали %d", len([]rune(params.Search)), maxSearchRunes)
	}
	if len(params.Genres) != maxGenres || params.Genres[0] != "Action" || params.Genres[1] != "Adventure" {
		t.Fatalf("жанры нормализованы неверно: %v", params.Genres)
	}
	if params.Page != 1 || params.PageSize != defaultPageSize || params.Sort != "title" || params.Order != "desc" {
		t.Fatalf("параметры по умолчанию неверны: %+v", params)
	}
	if params.YearFrom != 0 || params.YearTo != 0 {
		t.Fatalf("перевёрнутый диапазон не сброшен: %+v", params)
	}
}
