// Package scores добывает оценки игр из OpenCritic (RapidAPI) и Metacritic.
package scores

import (
	"regexp"
	"strings"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// diacritics транслитерирует частые латинские диакритики в ASCII
// (иначе slug рвётся: «Ragnarök» → «ragnar-k» вместо «ragnarok»).
var diacritics = strings.NewReplacer(
	"ä", "a", "à", "a", "á", "a", "â", "a", "ã", "a", "å", "a", "ā", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e", "ē", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ī", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o", "ō", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u", "ū", "u",
	"ñ", "n", "ç", "c", "ć", "c", "č", "c", "š", "s", "ž", "z",
	"ł", "l", "ý", "y", "ß", "ss",
	"Ä", "A", "Ö", "O", "Ü", "U", "É", "E", "Á", "A", "Ó", "O",
)

// editionNoise убирает издательские варианты вида «<тип> ... Edition/Bundle/…».
var editionNoise = regexp.MustCompile(`(?i)\b(standard|classic|enhanced|legendary|extended|slayer|mercenaries|complete|definitive|deluxe|ultimate|gold|premium|digital|remastered|next\s*gen|cross[- ]gen|elite|game of the year|goty|full time|console|anniversary|collector'?s)\b[^|]*?\b(edition|bundle|collection|version|set|upgrade|pass)\b`)

// platNoise и extraNoise убирают платформенные/служебные хвосты.
var platNoise = regexp.MustCompile(`(?i)\b(playstation\s*[45]|ps4|ps5|ps\s*vr2?)\b`)
var extraNoise = regexp.MustCompile(`(?i)\b(free upgrade|expansion pass|cross[- ]gen|next gen|playstation plus)\b`)

var parensNoise = regexp.MustCompile(`\([^)]*\)`)

// trailingWord снимает «висящее» одинокое слово издания, оставшееся после чисток
// (напр. «Cities: Skylines - PlayStation 4 Edition» → «cities skylines edition» → «cities skylines»).
var trailingWord = regexp.MustCompile(`(?i)[\s:–—-]+(edition|version|bundle|collection|set|upgrade|standard|classic)\s*$`)

// CleanTitle нормализует «сырое» название игры: транслитерирует диакритику,
// убирает ™®, апострофы, скобки и платформенный/издательский шум — чтобы повысить
// шанс совпадения с Metacritic/OpenCritic.
func CleanTitle(s string) string {
	s = strings.NewReplacer("’", "", "'", "", "®", " ", "™", " ").Replace(s)
	s = diacritics.Replace(s)
	s = parensNoise.ReplaceAllString(s, " ")
	s = strings.NewReplacer("&", " ", "+", " ").Replace(s)
	s = editionNoise.ReplaceAllString(s, " ")
	s = platNoise.ReplaceAllString(s, " ")
	s = extraNoise.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	for i := 0; i < 3; i++ { // снять до трёх вложенных висящих слов
		s = strings.TrimSpace(trailingWord.ReplaceAllString(s, ""))
	}
	return strings.Join(strings.Fields(s), " ")
}

// NormalizeTitle приводит название к канону для сравнения (нижний регистр, без
// диакритики, пунктуации и лишних пробелов).
func NormalizeTitle(s string) string {
	s = strings.NewReplacer("’", "", "'", "", "™", "", "®", "").Replace(s)
	s = diacritics.Replace(strings.ToLower(s))
	s = nonSlug.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// TitleTokens разбивает нормализованное название на множество значимых токенов.
// Римские/арабские номера частей сохраняются как есть — они различают сиквелы.
func TitleTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(NormalizeTitle(s)) {
		out[w] = true
	}
	return out
}

// TitlesMatch консервативно решает, относятся ли два названия к одной игре.
// Оба названия приводятся к ядру matchClean (снимаются издания/платформы/
// «director's cut» и пр.), после чего требуется РАВЕНСТВО множеств значимых
// токенов. Это принимает издательские варианты одной игры («Death Stranding
// Director's Cut» ↔ «Death Stranding»), но отвергает разные части серии: у них
// после чистки остаётся различающий токен («Ragnarok», «II», «2»). Симметрично:
// безразлично, какое из названий специфичнее (наш запрос или результат поиска).
func TitlesMatch(a, b string) bool {
	ta := TitleTokens(matchClean(a))
	tb := TitleTokens(matchClean(b))
	if len(ta) == 0 || len(ta) != len(tb) {
		return false
	}
	for tok := range ta {
		if !tb[tok] {
			return false
		}
	}
	return true
}

// matchClean агрессивно снимает скобочный контент, издания, платформы и служебный
// шум — приводит название к ядру для сравнения совпадений (см. TitlesMatch) и для
// вариантов поиска HLTB. Чистит сильнее CleanTitle (тот строит slug Metacritic и
// сохраняет, напр., «director's cut» в названии страницы).
var (
	matchBrackets = regexp.MustCompile(`[\(\[][^\)\]]*[\)\]]`)
	matchEdition  = regexp.MustCompile(`(?i)\b(standard|classic|enhanced|legendary|extended|complete|definitive|deluxe|ultimate|gold|premium|digital|next\s*gen|cross[- ]gen|elite|game of the year|goty|console|anniversary|jumbo|collector'?s)\b[^|]*?\b(edition|bundle|collection|version|set|upgrade|pass)\b`)
	matchExtra    = regexp.MustCompile(`(?i)\b(free upgrade|expansion pass|cross[- ]gen|next gen|playstation plus|directors? cut|ea sports|remastered|reforged|ps4|ps5|playstation\s*[45]|ps\s*vr2?)\b`)
	matchPrefix   = regexp.MustCompile(`(?i)^\s*marvels?\s+`)
)

func matchClean(s string) string {
	s = strings.NewReplacer("’", "", "'", "", "®", " ", "™", " ", "|", " ", ":", " ", "-", " ", "–", " ", "—", " ", "&", " ", "+", " ").Replace(s)
	s = diacritics.Replace(s)
	s = strings.NewReplacer("FARCRY", "Far Cry", "Farcry", "Far Cry", "farcry", "far cry").Replace(s)
	s = matchBrackets.ReplaceAllString(s, " ")
	s = matchEdition.ReplaceAllString(s, " ")
	s = matchExtra.ReplaceAllString(s, " ")
	s = matchPrefix.ReplaceAllString(s, " ")
	for i := 0; i < 3; i++ {
		s = strings.TrimSpace(trailingWord.ReplaceAllString(s, ""))
	}
	return strings.Join(strings.Fields(s), " ")
}

// Slugify строит slug для URL Metacritic из (уже очищенного) названия.
func Slugify(s string) string {
	s = diacritics.Replace(strings.ToLower(s))
	s = nonSlug.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
