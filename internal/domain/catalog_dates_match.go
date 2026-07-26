package domain

import (
	"html"
	"sort"
	"strings"
	"unicode"
)

// NormalizeCatalogTitle приводит заголовки стора и блога к форме для
// сопоставления. Смысловые слова наподобие "remastered" сохраняются.
func NormalizeCatalogTitle(value string) string {
	value = html.UnescapeString(value)
	if pipe := strings.Index(value, "|"); pipe >= 0 {
		value = value[:pipe]
	}
	value = strings.ToLower(strings.NewReplacer(
		"™", "", "®", "", "©", "",
		"’", "'", "‘", "'", "–", "-", "—", "-",
		"&", " and ",
	).Replace(value))
	var normalized strings.Builder
	space := true
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			normalized.WriteRune(char)
			space = false
		} else if !space {
			normalized.WriteByte(' ')
			space = true
		}
	}
	return strings.Join(strings.Fields(normalized.String()), " ")
}

// MatchCatalogAddition находит одно безопасное соответствие. found и
// ambiguous взаимоисключающие: false/false означает отсутствие кандидата, а
// false/true — несколько неразличимых кандидатов. Для повторного анонса одной
// игры выбирается самая новая дата.
func MatchCatalogAddition(title, titleEn string, candidates []CatalogAddition) (match CatalogAddition, found, ambiguous bool) {
	targetKeys := titleMatchKeys(titleEn)
	for key := range titleMatchKeys(title) {
		targetKeys[key] = true
	}
	if len(targetKeys) == 0 {
		return CatalogAddition{}, false, false
	}

	type scoredCandidate struct {
		candidate CatalogAddition
		key       string
		score     float64
	}
	var matches []scoredCandidate
	for _, candidate := range candidates {
		bestScore := 0.0
		bestKey := ""
		for candidateKey := range titleMatchKeys(candidate.Title) {
			for targetKey := range targetKeys {
				score := titleSimilarity(targetKey, candidateKey)
				if score > bestScore {
					bestScore, bestKey = score, candidateKey
				}
			}
		}
		if bestScore >= 0.86 {
			matches = append(matches, scoredCandidate{candidate: candidate, key: bestKey, score: bestScore})
		}
	}
	if len(matches) == 0 {
		return CatalogAddition{}, false, false
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		if !matches[i].candidate.AddedOn.Equal(matches[j].candidate.AddedOn) {
			return matches[i].candidate.AddedOn.After(matches[j].candidate.AddedOn)
		}
		return matches[i].candidate.Title < matches[j].candidate.Title
	})
	best := matches[0]
	if best.score == 1 {
		for _, candidate := range matches[1:] {
			if candidate.score != 1 {
				break
			}
			if NormalizeCatalogTitle(candidate.candidate.Title) == NormalizeCatalogTitle(best.candidate.Title) &&
				candidate.candidate.AddedOn.After(best.candidate.AddedOn) {
				best = candidate
			}
		}
		return best.candidate, true, false
	}
	for _, other := range matches[1:] {
		if other.score < best.score-0.08 {
			break
		}
		if other.key != best.key && NormalizeCatalogTitle(other.candidate.Title) != NormalizeCatalogTitle(best.candidate.Title) {
			return CatalogAddition{}, false, true
		}
	}
	for _, candidate := range matches {
		if candidate.key == best.key && candidate.candidate.AddedOn.After(best.candidate.AddedOn) {
			best = candidate
		}
	}
	return best.candidate, true, false
}

func titleMatchKeys(value string) map[string]bool {
	full := NormalizeCatalogTitle(value)
	keys := make(map[string]bool)
	if full == "" {
		return keys
	}
	for _, variant := range safeTitleVariants(full) {
		addTitleMatchKeys(keys, variant)
	}
	return keys
}

// safeTitleVariants добавляет только известные варианты написания, которые не
// меняют идентичность игры. Они не входят в NormalizeCatalogTitle, поскольку
// такая специфичная для Store замена была бы неожиданной при обычном сравнении.
func safeTitleVariants(title string) []string {
	variants := []string{title}
	words := strings.Fields(title)
	for i, word := range words {
		if word != "farcry" {
			continue
		}
		aliasWords := append([]string(nil), words[:i]...)
		aliasWords = append(aliasWords, "far", "cry")
		aliasWords = append(aliasWords, words[i+1:]...)
		variants = append(variants, strings.Join(aliasWords, " "))
		break
	}
	return variants
}

func addTitleMatchKeys(keys map[string]bool, full string) {
	if full == "" {
		return
	}
	keys[full] = true
	words := strings.Fields(full)
	drop := map[string]bool{"ps4": true, "ps5": true}
	var noPlatform []string
	for _, word := range words {
		if !drop[word] {
			noPlatform = append(noPlatform, word)
		}
	}
	trimmed := strings.TrimSuffix(strings.Join(noPlatform, " "), " and")
	if trimmed == "" {
		return
	}
	keys[trimmed] = true

	// Store иногда добавляет название подписки в скобках. К этому моменту
	// пунктуация и платформы уже удалены, поэтому отбрасываются только точные
	// известные суффиксы.
	for _, suffix := range []string{" playstation plus extra", " playstation plus"} {
		if base := strings.TrimSuffix(trimmed, suffix); base != trimmed && base != "" {
			keys[base] = true
			addEditionTitleKeys(keys, base)
		}
	}
	addEditionTitleKeys(keys, trimmed)
}

func addEditionTitleKeys(keys map[string]bool, title string) {
	for _, suffix := range []string{
		" standard edition", " digital deluxe edition", " deluxe edition",
		" ultimate edition", " complete edition", " game of the year edition",
	} {
		if base := strings.TrimSuffix(title, suffix); base != title && base != "" {
			keys[base] = true
		}
	}
}

func titleSimilarity(left, right string) float64 {
	if left == right {
		return 1
	}
	leftWords, rightWords := strings.Fields(left), strings.Fields(right)
	count := make(map[string]int)
	for _, word := range leftWords {
		count[word]++
	}
	common := 0
	for _, word := range rightWords {
		if count[word] > 0 {
			common++
			count[word]--
		}
	}
	if len(leftWords)+len(rightWords) == 0 {
		return 0
	}
	return 2 * float64(common) / float64(len(leftWords)+len(rightWords))
}
