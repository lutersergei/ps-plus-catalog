package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

const catalogDateBackfillVersion = 1

type catalogDateBackfillDocument struct {
	Version  int                        `json:"version"`
	Entries  []catalogDateBackfillEntry `json:"entries"`
	KeepNull []catalogDateIdentity      `json:"keep_null"`
}

type catalogDateBackfillEntry struct {
	GameID    string    `json:"game_id"`
	Title     string    `json:"title"`
	AddedOn   time.Time `json:"-"`
	SourceURL string    `json:"source_url"`
	AddedText string    `json:"added_on"`
}

type catalogDateIdentity struct {
	GameID string `json:"game_id"`
	Title  string `json:"title"`
}

type catalogDateBackfill struct {
	Entries       []catalogDateBackfillEntry
	KeepNull      []catalogDateIdentity
	entriesByID   map[string]catalogDateBackfillEntry
	entriesByName map[string]catalogDateBackfillEntry
	nullByID      map[string]catalogDateIdentity
	nullByName    map[string]catalogDateIdentity
}

type catalogDateBackfillMatchResult struct {
	Matches             []domain.CatalogDateBackfillMatch
	KeepNullIDs         []int64
	KeepNullGames       []string
	AnnouncementTargets []domain.CatalogDateTarget
}

func loadCatalogDateBackfill(raw []byte) (catalogDateBackfill, error) {
	var document catalogDateBackfillDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return catalogDateBackfill{}, fmt.Errorf("parse catalog date backfill: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return catalogDateBackfill{}, fmt.Errorf("parse catalog date backfill: %w", err)
	}
	if document.Version != catalogDateBackfillVersion {
		return catalogDateBackfill{}, fmt.Errorf(
			"catalog date backfill version %d, want %d",
			document.Version,
			catalogDateBackfillVersion,
		)
	}

	manifest := catalogDateBackfill{
		Entries:       document.Entries,
		KeepNull:      document.KeepNull,
		entriesByID:   make(map[string]catalogDateBackfillEntry, len(document.Entries)),
		entriesByName: make(map[string]catalogDateBackfillEntry, len(document.Entries)),
		nullByID:      make(map[string]catalogDateIdentity, len(document.KeepNull)),
		nullByName:    make(map[string]catalogDateIdentity, len(document.KeepNull)),
	}
	for i := range manifest.Entries {
		entry := &manifest.Entries[i]
		entry.GameID = strings.TrimSpace(entry.GameID)
		entry.Title = strings.TrimSpace(entry.Title)
		entry.SourceURL = strings.TrimSpace(entry.SourceURL)
		if entry.GameID == "" || entry.Title == "" || entry.AddedText == "" || entry.SourceURL == "" {
			return catalogDateBackfill{}, fmt.Errorf("catalog date backfill entry %d has an empty required field", i)
		}
		if _, exists := manifest.entriesByID[entry.GameID]; exists {
			return catalogDateBackfill{}, fmt.Errorf("duplicate catalog date backfill game_id %q", entry.GameID)
		}
		nameKey := catalogDateIdentityKey(entry.Title)
		if nameKey == "" {
			return catalogDateBackfill{}, fmt.Errorf("catalog date backfill entry %q has an empty normalized title", entry.GameID)
		}
		if old, exists := manifest.entriesByName[nameKey]; exists {
			return catalogDateBackfill{}, fmt.Errorf(
				"duplicate catalog date backfill title %q for %q and %q",
				entry.Title,
				old.GameID,
				entry.GameID,
			)
		}
		addedOn, err := time.Parse("2006-01-02", entry.AddedText)
		if err != nil {
			return catalogDateBackfill{}, fmt.Errorf("parse catalog date for %q: %w", entry.GameID, err)
		}
		if err := validateCatalogDateSourceURL(entry.SourceURL); err != nil {
			return catalogDateBackfill{}, fmt.Errorf("catalog date source for %q: %w", entry.GameID, err)
		}
		entry.AddedOn = addedOn
		manifest.entriesByID[entry.GameID] = *entry
		manifest.entriesByName[nameKey] = *entry
	}
	for i := range manifest.KeepNull {
		identity := &manifest.KeepNull[i]
		identity.GameID = strings.TrimSpace(identity.GameID)
		identity.Title = strings.TrimSpace(identity.Title)
		if identity.GameID == "" || identity.Title == "" {
			return catalogDateBackfill{}, fmt.Errorf("catalog date keep_null entry %d has an empty required field", i)
		}
		if _, exists := manifest.entriesByID[identity.GameID]; exists {
			return catalogDateBackfill{}, fmt.Errorf("catalog date game_id %q is both dated and keep_null", identity.GameID)
		}
		if _, exists := manifest.nullByID[identity.GameID]; exists {
			return catalogDateBackfill{}, fmt.Errorf("duplicate catalog date keep_null game_id %q", identity.GameID)
		}
		nameKey := catalogDateIdentityKey(identity.Title)
		if nameKey == "" {
			return catalogDateBackfill{}, fmt.Errorf("catalog date keep_null entry %q has an empty normalized title", identity.GameID)
		}
		if _, exists := manifest.entriesByName[nameKey]; exists {
			return catalogDateBackfill{}, fmt.Errorf("catalog date title %q is both dated and keep_null", identity.Title)
		}
		if old, exists := manifest.nullByName[nameKey]; exists {
			return catalogDateBackfill{}, fmt.Errorf(
				"duplicate catalog date keep_null title %q for %q and %q",
				identity.Title,
				old.GameID,
				identity.GameID,
			)
		}
		manifest.nullByID[identity.GameID] = *identity
		manifest.nullByName[nameKey] = *identity
	}
	return manifest, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected data after JSON document")
		}
		return err
	}
	return nil
}

func validateCatalogDateSourceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("URL must be absolute HTTPS: %q", raw)
	}
	return nil
}

func catalogDateIdentityKey(title string) string {
	return domain.NormalizeCatalogTitle(title)
}

func (manifest catalogDateBackfill) entryForTarget(target domain.CatalogDateTarget) (catalogDateBackfillEntry, bool, error) {
	if entry, ok := manifest.entriesByID[target.GameID]; ok {
		if !catalogDateTargetHasTitle(target, entry.Title) {
			return catalogDateBackfillEntry{}, false, fmt.Errorf(
				"catalog date backfill title changed for %q: manifest=%q current=%q",
				target.GameID,
				entry.Title,
				target.Title,
			)
		}
		return entry, true, nil
	}
	return manifest.entryForTargetTitle(target)
}

func (manifest catalogDateBackfill) entryForTargetTitle(target domain.CatalogDateTarget) (catalogDateBackfillEntry, bool, error) {
	var found catalogDateBackfillEntry
	for _, title := range []string{target.Title, target.TitleEn} {
		entry, ok := manifest.entriesByName[catalogDateIdentityKey(title)]
		if !ok {
			continue
		}
		if found.GameID != "" && found.GameID != entry.GameID {
			return catalogDateBackfillEntry{}, false, fmt.Errorf(
				"catalog target %q matches multiple backfill entries %q and %q",
				target.GameID,
				found.GameID,
				entry.GameID,
			)
		}
		found = entry
	}
	return found, found.GameID != "", nil
}

func (manifest catalogDateBackfill) keepNullForTarget(target domain.CatalogDateTarget) (catalogDateIdentity, bool, error) {
	if identity, ok := manifest.nullByID[target.GameID]; ok {
		if !catalogDateTargetHasTitle(target, identity.Title) {
			return catalogDateIdentity{}, false, fmt.Errorf(
				"catalog date keep_null title changed for %q: manifest=%q current=%q",
				target.GameID,
				identity.Title,
				target.Title,
			)
		}
		return identity, true, nil
	}
	for _, title := range []string{target.Title, target.TitleEn} {
		if identity, ok := manifest.nullByName[catalogDateIdentityKey(title)]; ok {
			return identity, true, nil
		}
	}
	return catalogDateIdentity{}, false, nil
}

func catalogDateTargetHasTitle(target domain.CatalogDateTarget, expected string) bool {
	want := catalogDateIdentityKey(expected)
	return want != "" && (catalogDateIdentityKey(target.Title) == want || catalogDateIdentityKey(target.TitleEn) == want)
}

// matchCatalogDateBackfillTargets применяет вручную проверенную историю только
// к периодам исходного снимка. Повторные появления по-прежнему должны
// подтверждаться анонсом внутри окна наблюдения.
func matchCatalogDateBackfillTargets(
	targets []domain.CatalogDateTarget,
	manifest catalogDateBackfill,
) (catalogDateBackfillMatchResult, error) {
	result := catalogDateBackfillMatchResult{
		Matches:             make([]domain.CatalogDateBackfillMatch, 0, len(manifest.Entries)),
		AnnouncementTargets: make([]domain.CatalogDateTarget, 0, len(targets)),
	}
	matchedEntries := make(map[string]string)
	matchedNullEntries := make(map[string]string)
	for _, target := range targets {
		if !target.Initial {
			result.AnnouncementTargets = append(result.AnnouncementTargets, target)
			continue
		}
		entry, found, err := manifest.entryForTarget(target)
		if err != nil {
			return catalogDateBackfillMatchResult{}, err
		}
		if found {
			if oldTargetID, exists := matchedEntries[entry.GameID]; exists && oldTargetID != target.GameID {
				return catalogDateBackfillMatchResult{}, fmt.Errorf(
					"catalog date backfill entry %q matches multiple current products %q and %q",
					entry.GameID,
					oldTargetID,
					target.GameID,
				)
			}
			matchedEntries[entry.GameID] = target.GameID
			result.Matches = append(result.Matches, domain.CatalogDateBackfillMatch{
				MembershipID: target.MembershipID,
				AddedOn:      entry.AddedOn,
				SourceURL:    entry.SourceURL,
			})
			continue
		}
		identity, keepNull, err := manifest.keepNullForTarget(target)
		if err != nil {
			return catalogDateBackfillMatchResult{}, err
		}
		if keepNull {
			if oldTargetID, exists := matchedNullEntries[identity.GameID]; exists && oldTargetID != target.GameID {
				return catalogDateBackfillMatchResult{}, fmt.Errorf(
					"catalog date keep_null entry %q matches multiple current products %q and %q",
					identity.GameID,
					oldTargetID,
					target.GameID,
				)
			}
			matchedNullEntries[identity.GameID] = target.GameID
			result.KeepNullIDs = append(result.KeepNullIDs, target.MembershipID)
			result.KeepNullGames = append(result.KeepNullGames, target.Title)
			continue
		}
		result.AnnouncementTargets = append(result.AnnouncementTargets, target)
	}
	return result, nil
}
