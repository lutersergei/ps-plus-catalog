package services

import (
	"os"
	"testing"

	"github.com/lutersergei/ps-plus-catalog/internal/domain"
)

func mustBackfillJSON(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../catalog_dates_backfill.json")
	if err != nil {
		t.Fatalf("read backfill: %v", err)
	}
	return raw
}

func TestCatalogDateBackfillManifestCoverage(t *testing.T) {
	manifest, err := loadCatalogDateBackfill(mustBackfillJSON(t))
	if err != nil {
		t.Fatalf("load backfill: %v", err)
	}
	if len(manifest.Entries) != 160 {
		t.Fatalf("entries=%d, want 160", len(manifest.Entries))
	}
	launch, researched := 0, 0
	for _, entry := range manifest.Entries {
		if got := entry.AddedOn.Format("2006-01-02"); got == "2022-06-23" {
			launch++
		} else {
			researched++
		}
	}
	if launch != 130 || researched != 30 {
		t.Fatalf("launch=%d researched=%d, want 130 and 30", launch, researched)
	}
	if len(manifest.KeepNull) != 1 || manifest.KeepNull[0].Title != "For The King" {
		t.Fatalf("keep_null=%+v, want For The King only", manifest.KeepNull)
	}
}

func TestMatchCatalogDateBackfillTargetsUsesIDAndExactTitleFallback(t *testing.T) {
	manifest, err := loadCatalogDateBackfill(mustBackfillJSON(t))
	if err != nil {
		t.Fatalf("load backfill: %v", err)
	}
	targets := []domain.CatalogDateTarget{
		{
			MembershipID: 1,
			GameID:       "EP4008-CUSA17267_00-AO2SIEE000000000",
			Title:        "AO Tennis 2",
			TitleEn:      "AO Tennis 2",
			Initial:      true,
		},
		{
			MembershipID: 2,
			GameID:       "replacement-product-id",
			Title:        "Anodyne™",
			TitleEn:      "Anodyne",
			Initial:      true,
		},
		{
			MembershipID: 3,
			GameID:       "replacement-for-the-king-id",
			Title:        "For The King",
			TitleEn:      "For The King",
			Initial:      true,
		},
		{
			MembershipID: 4,
			GameID:       "EP4008-CUSA17267_00-AO2SIEE000000000",
			Title:        "AO Tennis 2",
			TitleEn:      "AO Tennis 2",
			Initial:      false,
		},
		{
			MembershipID: 5,
			GameID:       "unknown",
			Title:        "Unknown Game",
			TitleEn:      "Unknown Game",
			Initial:      true,
		},
	}

	result, err := matchCatalogDateBackfillTargets(targets, manifest)
	if err != nil {
		t.Fatalf("match backfill: %v", err)
	}
	if len(result.Matches) != 2 {
		t.Fatalf("matches=%+v, want two", result.Matches)
	}
	dates := make(map[int64]string, len(result.Matches))
	for _, match := range result.Matches {
		dates[match.MembershipID] = match.AddedOn.Format("2006-01-02")
	}
	if dates[1] != "2022-06-23" || dates[2] != "2023-04-07" {
		t.Fatalf("dates=%v", dates)
	}
	if len(result.KeepNullIDs) != 1 || result.KeepNullIDs[0] != 3 {
		t.Fatalf("keep-null=%v, want membership 3", result.KeepNullIDs)
	}
	if len(result.AnnouncementTargets) != 2 ||
		result.AnnouncementTargets[0].MembershipID != 4 ||
		result.AnnouncementTargets[1].MembershipID != 5 {
		t.Fatalf("announcement targets=%+v, want memberships 4 and 5", result.AnnouncementTargets)
	}
}

func TestMatchCatalogDateBackfillTargetsRejectsProductTitleDrift(t *testing.T) {
	manifest, err := loadCatalogDateBackfill(mustBackfillJSON(t))
	if err != nil {
		t.Fatalf("load backfill: %v", err)
	}
	_, err = matchCatalogDateBackfillTargets([]domain.CatalogDateTarget{{
		MembershipID: 1,
		GameID:       "EP4008-CUSA17267_00-AO2SIEE000000000",
		Title:        "Different Game",
		TitleEn:      "Different Game",
		Initial:      true,
	}}, manifest)
	if err == nil {
		t.Fatal("product ID with a different title must fail closed")
	}
}

func TestMatchCatalogDateBackfillTargetsRejectsDuplicateTitleFallback(t *testing.T) {
	manifest, err := loadCatalogDateBackfill(mustBackfillJSON(t))
	if err != nil {
		t.Fatalf("load backfill: %v", err)
	}
	_, err = matchCatalogDateBackfillTargets([]domain.CatalogDateTarget{
		{
			MembershipID: 1,
			GameID:       "replacement-one",
			Title:        "AO Tennis 2",
			TitleEn:      "AO Tennis 2",
			Initial:      true,
		},
		{
			MembershipID: 2,
			GameID:       "replacement-two",
			Title:        "AO Tennis 2",
			TitleEn:      "AO Tennis 2",
			Initial:      true,
		},
	}, manifest)
	if err == nil {
		t.Fatal("one manifest title must not backfill two current products")
	}
}
