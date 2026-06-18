// Package scores добывает оценки игр из OpenCritic (RapidAPI) и Metacritic.
package scores

import (
	"regexp"
	"strings"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

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
