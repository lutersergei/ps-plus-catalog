package psstore

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseAnnouncementSitemapFiltersCatalogPosts(t *testing.T) {
	raw := []byte(`<?xml version="1.0"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://blog.playstation.com/2025/07/09/playstation-plus-game-catalog-for-july-games/</loc><lastmod>2025-07-09T12:00:00Z</lastmod></url>
  <url><loc>https://blog.playstation.com/2025/07/10/15-must-play-games-on-playstation-plus-game-catalog/</loc><lastmod>2025-07-10T12:00:00Z</lastmod></url>
  <url><loc>https://blog.playstation.com/2025/07/11/unrelated/</loc><lastmod>2025-07-11T12:00:00Z</lastmod></url>
  <url><loc>https://blog.playstation.com.evil.example/2025/07/09/playstation-plus-game-catalog-for-july/</loc><lastmod>2025-07-09T12:00:00Z</lastmod></url>
</urlset>`)
	refs, err := ParseAnnouncementSitemap(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(refs) != 1 || !strings.Contains(refs[0].URL, "game-catalog-for-july") {
		t.Fatalf("refs=%#v", refs)
	}
}

func TestParseAnnouncementSitemapIndexUsesPublishedYearsAndRecent(t *testing.T) {
	raw := []byte(`<?xml version="1.0"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>https://blog.playstation.com/wp-sitemap-posts-recent.xml</loc></sitemap>
  <sitemap><loc>https://blog.playstation.com/wp-sitemap-posts-2026.xml</loc></sitemap>
  <sitemap><loc>https://blog.playstation.com/wp-sitemap-posts-2025.xml</loc></sitemap>
  <sitemap><loc>https://blog.playstation.com/wp-sitemap-posts-2021.xml</loc></sitemap>
  <sitemap><loc>https://example.com/wp-sitemap-posts-2026.xml</loc></sitemap>
</sitemapindex>`)

	got, err := parseAnnouncementSitemapIndex(raw, 2027)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{
		"https://blog.playstation.com/wp-sitemap-posts-2025.xml",
		"https://blog.playstation.com/wp-sitemap-posts-2026.xml",
		"https://blog.playstation.com/wp-sitemap-posts-recent.xml",
	}
	if len(got) != len(want) {
		t.Fatalf("sitemaps=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sitemaps[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseCatalogAnnouncementUsesTurkeyDefaultAndGlobalException(t *testing.T) {
	raw := announcementHTML("2026-07-15T08:30:00-07:00", `
<p>Games will be available on varying dates in the US, the UK and Japan.
The full lineup will be available to play on July 21 in all other regions.</p>
<h2>PlayStation Plus Extra and Premium | Game Catalog</h2>
<p><strong>Rise of the Ronin | PS5</strong></p>
<p><em>Rise of the Ronin will be available July 15 in the US, the UK and Japan.</em></p>
<p><strong>Avatar: Frontiers of Pandora | PS5</strong></p>
<p><em>Avatar: Frontiers of Pandora will be available globally July 20.</em></p>
<h2>PlayStation Plus Premium</h2>
<p><strong>Premium Classic | PS5, PS4</strong></p>`)

	got, err := ParseCatalogAnnouncement(raw, AnnouncementRef{URL: "https://blog.playstation.com/example"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.Games) != 2 {
		t.Fatalf("games=%#v", got.Games)
	}
	assertAdditionDate(t, got.Games, "Rise of the Ronin", "2026-07-21")
	assertAdditionDate(t, got.Games, "Avatar: Frontiers of Pandora", "2026-07-20")
}

func TestParseCatalogAnnouncementHandlesPerGameLaunchDates(t *testing.T) {
	raw := announcementHTML("2025-07-09T08:30:00-07:00", `
<p>Cyberpunk 2077 is available to play today, July 9, with Abiotic Factor launching into the service July 22.
All other titles will be available to play on July 15.</p>
<h2>PlayStation Plus Extra and Premium | Game Catalog</h2>
<p><strong>Cyberpunk 2077 | PS5, PS4</strong></p>
<p><strong>Abiotic Factor | PS5</strong></p>
<p><strong>Banishers: Ghosts of New Eden | PS5</strong></p>
<h2>PlayStation Plus Premium</h2>`)

	got, err := ParseCatalogAnnouncement(raw, AnnouncementRef{URL: "https://blog.playstation.com/example"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	assertAdditionDate(t, got.Games, "Cyberpunk 2077", "2025-07-09")
	assertAdditionDate(t, got.Games, "Abiotic Factor", "2025-07-22")
	assertAdditionDate(t, got.Games, "Banishers: Ghosts of New Eden", "2025-07-15")
}

func TestParseCatalogAnnouncementHandlesLegacyListsAndEuropeLaunch(t *testing.T) {
	raw := announcementHTML("2022-05-16T08:00:00-07:00", `
<h2>PS4 and PS5 Game Catalog</h2>
<h3>PlayStation Plus Extra and Premium/Deluxe Plans</h3>
<ul>
  <li>Alienation | Housemarque, PS4</li>
  <li>Demon's Souls | Bluepoint Games, PS5</li>
</ul>
<h2>Classic Games Catalog</h2>
<p>Launch starts with Asia on May 24, Japan on June 2, America on June 13,
and finally Europe, Australia, and New Zealand on June 23.</p>`)

	got, err := ParseCatalogAnnouncement(raw, AnnouncementRef{URL: "https://blog.playstation.com/example"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.Games) != 2 {
		t.Fatalf("games=%#v", got.Games)
	}
	assertAdditionDate(t, got.Games, "Alienation", "2022-06-23")
	assertAdditionDate(t, got.Games, "Demon's Souls", "2022-06-23")
}

func TestParseCatalogAnnouncementFixtures(t *testing.T) {
	tests := []struct {
		name string
		path string
		want map[string]string
	}{
		{
			name: "legacy launch list",
			path: "../../../testdata/playstation_blog_catalog_2022_legacy.html",
			want: map[string]string{
				"Alienation":    "2022-06-23",
				"Demon’s Souls": "2022-06-23",
			},
		},
		{
			name: "regional modern announcement",
			path: "../../../testdata/playstation_blog_catalog_2026_regional.html",
			want: map[string]string{
				"Rise of the Ronin":            "2026-07-21",
				"Avatar: Frontiers of Pandora": "2026-07-20",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			got, err := ParseCatalogAnnouncement(raw, AnnouncementRef{URL: "https://blog.playstation.com/fixture"})
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			for title, want := range tt.want {
				assertAdditionDate(t, got.Games, title, want)
			}
		})
	}
}

func TestParseCatalogAnnouncementDoesNotApplyAsideDefaultToExceptions(t *testing.T) {
	raw := announcementHTML("2024-04-10T08:00:00-07:00", `
<p>Aside from Animal Well and Tales of Kenzera: Zau, all titles will be available on April 16.</p>
<h2>PlayStation Plus Extra and Premium | Game Catalog</h2>
<p><strong>Animal Well* | PS5</strong></p>
<p>*Animal Well will launch May 9.</p>
<p><strong>Tales of Kenzera: Zau* | PS5</strong></p>
<p>*Tales of Kenzera: ZAU will launch April 23.</p>
<p><strong>Dave the Diver | PS4, PS5</strong></p>
<h2>PlayStation Plus Premium</h2>`)

	got, err := ParseCatalogAnnouncement(raw, AnnouncementRef{URL: "https://blog.playstation.com/example"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	assertAdditionDate(t, got.Games, "Animal Well", "2024-05-09")
	assertAdditionDate(t, got.Games, "Tales of Kenzera: Zau", "2024-04-23")
	assertAdditionDate(t, got.Games, "Dave the Diver", "2024-04-16")
}

func TestParseCatalogAnnouncementHandlesReleasedException(t *testing.T) {
	raw := announcementHTML("2025-12-10T08:00:00-07:00", `
<p>All titles will be available to play December 16, aside from Skate Story,
which released into the service December 8.</p>
<h2>PlayStation Plus Extra and Premium | Game Catalog</h2>
<p><strong>Skate Story | PS5</strong></p>
<p><strong>Assassin's Creed Mirage | PS5, PS4</strong></p>
<h2>PlayStation Plus Premium</h2>`)
	got, err := ParseCatalogAnnouncement(raw, AnnouncementRef{URL: "https://blog.playstation.com/example"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	assertAdditionDate(t, got.Games, "Skate Story", "2025-12-08")
	assertAdditionDate(t, got.Games, "Assassin's Creed Mirage", "2025-12-16")
}

func TestDateFromPartsInfersYearAcrossPublicationBoundary(t *testing.T) {
	tests := []struct {
		name      string
		published string
		month     string
		day       string
		want      string
	}{
		{
			name:      "previous year",
			published: "2026-01-10",
			month:     "December",
			day:       "16",
			want:      "2025-12-16",
		},
		{
			name:      "next year",
			published: "2025-12-10",
			month:     "January",
			day:       "15",
			want:      "2026-01-15",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			published := dateForTest(tt.published)
			got, ok := dateFromParts(tt.month, tt.day, published)
			if !ok {
				t.Fatalf("dateFromParts returned ok=%v", ok)
			}
			if got.Format("2006-01-02") != tt.want {
				t.Fatalf("date=%s, want %s", got.Format("2006-01-02"), tt.want)
			}
		})
	}
}

func TestParseCatalogAnnouncementIgnoresSingleRegionDates(t *testing.T) {
	raw := announcementHTML("2026-07-15T08:30:00-07:00", `
<p>UK Only Game will be available in the UK on July 15.</p>
<p>Japan Only Game will be available in Japan on July 16.</p>
<p>All other titles will be available on July 21.</p>
<h2>PlayStation Plus Extra and Premium | Game Catalog</h2>
<p><strong>UK Only Game | PS5</strong></p>
<p><strong>Japan Only Game | PS5</strong></p>
<h2>PlayStation Plus Premium</h2>`)

	got, err := ParseCatalogAnnouncement(raw, AnnouncementRef{URL: "https://blog.playstation.com/example"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	assertAdditionDate(t, got.Games, "UK Only Game", "2026-07-21")
	assertAdditionDate(t, got.Games, "Japan Only Game", "2026-07-21")
}

func announcementHTML(published, content string) []byte {
	return []byte(`<html><body>
<time class="entry-date published" datetime="` + published + `"></time>
<div class="post-single__content single__content entry-content">` + content + `
<div class="post-single__footer"></div></div>
</body></html>`)
}

func assertAdditionDate(t *testing.T, games []CatalogAddition, title, want string) {
	t.Helper()
	for _, game := range games {
		if game.Title == title {
			if got := game.AddedOn.Format("2006-01-02"); got != want {
				t.Fatalf("%s date=%s, want %s", title, got, want)
			}
			return
		}
	}
	t.Fatalf("game %q not found in %#v", title, games)
}

func dateForTest(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}
