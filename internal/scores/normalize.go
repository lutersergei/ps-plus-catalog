// Package scores добывает оценки игр из OpenCritic (RapidAPI) и Metacritic.
package scores

import (
	"regexp"
	"strings"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// editionNoise убирает платформенные/издательские суффиксы, мешающие матчингу:
// "PS4 & PS5", "PS4® & PS5™", "- Standard", "Console Edition",
// "Collector's/Deluxe/Standard/Complete/Definitive ... Edition", "Bundle".
var editionNoise = regexp.MustCompile(`(?i)\s*(?:[-–—:]\s*)?(?:` +
	`ps4|ps5|playstation\s*[45]|console edition|bundle|` +
	`(?:collector'?s?|deluxe|standard|complete|definitive|gold|ultimate|digital|premium|remastered)\b[^|]*?edition` +
	`)\b`)

// CleanTitle нормализует «сырое» название игры, отбрасывая платформенный/издательский
// шум, чтобы повысить шанс совпадения с Metacritic/OpenCritic.
func CleanTitle(s string) string {
	s = strings.NewReplacer("™", "", "®", "", "&", " ").Replace(s)
	s = editionNoise.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// NormalizeTitle приводит название к канону для сравнения: нижний регистр,
// без ™®, без пунктуации, схлопнутые пробелы.
func NormalizeTitle(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("™", "", "®", "", "’", "", "'", "").Replace(s)
	s = nonSlug.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// Slugify строит slug для URL Metacritic: нижний регистр, не-алфанумерик → "-",
// без ведущих/замыкающих дефисов.
func Slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("™", "", "®", "", "’", "", "'", "").Replace(s)
	s = nonSlug.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
