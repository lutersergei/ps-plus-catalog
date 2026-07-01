package scores

import "testing"

func TestBestMatch(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		results   []ocSearchResult
		wantID    int
		wantFound bool
	}{
		{
			name:      "пустая выдача",
			title:     "Hades",
			results:   nil,
			wantFound: false,
		},
		{
			name:      "точное совпадение поверх ближайшего по dist",
			title:     "Hades",
			results:   []ocSearchResult{{ID: 1, Name: "Hades II", Dist: 0.1}, {ID: 2, Name: "Hades", Dist: 0.5}},
			wantID:    2,
			wantFound: true,
		},
		{
			name:      "близкий fallback с совпадением токенов принимается",
			title:     "Death Stranding Directors Cut",
			results:   []ocSearchResult{{ID: 7, Name: "Death Stranding", Dist: 0.3}},
			wantID:    7,
			wantFound: true,
		},
		{
			name:      "нерелевантный ближайший отвергается",
			title:     "Hades",
			results:   []ocSearchResult{{ID: 9, Name: "Bayonetta", Dist: 0.2}},
			wantFound: false,
		},
		{
			name:  "перебирает кандидатов после ближайшего нерелевантного",
			title: "Marvel's Guardians of the Galaxy PS4 & PS5",
			results: []ocSearchResult{
				{ID: 1, Name: "Marvel's Guardians of the Galaxy: The Telltale Series", Dist: 0.38},
				{ID: 2, Name: "Guardians of the Galaxy", Dist: 0.39},
			},
			wantID:    2,
			wantFound: true,
		},
		{
			name:      "близкий по токенам, но слишком далёкий dist отвергается",
			title:     "Hades",
			results:   []ocSearchResult{{ID: 3, Name: "Hades II", Dist: 5.0}},
			wantFound: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, found := bestMatch(c.title, c.results)
			if found != c.wantFound {
				t.Fatalf("found=%v, ждали %v", found, c.wantFound)
			}
			if found && got.ID != c.wantID {
				t.Errorf("ID=%d, ждали %d", got.ID, c.wantID)
			}
		})
	}
}

func TestParseOpenCriticGame(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantScore int
		wantFound bool
		wantErr   bool
	}{
		{"валидная оценка", `{"topCriticScore":83.6}`, 84, true, false},
		{"null → нет данных", `{"topCriticScore":null}`, 0, false, false},
		{"поле отсутствует → нет данных", `{}`, 0, false, false},
		{"ноль (не прорецензировано) → нет данных", `{"topCriticScore":0}`, 0, false, false},
		{"отрицательное → нет данных", `{"topCriticScore":-1}`, 0, false, false},
		{"больше 100 → нет данных", `{"topCriticScore":250}`, 0, false, false},
		{"битый JSON → ошибка", `{`, 0, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			score, found, pageURL, err := parseOpenCriticGame([]byte(c.raw))
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, c.wantErr)
			}
			if found != c.wantFound {
				t.Fatalf("found=%v, ждали %v", found, c.wantFound)
			}
			if found && score != c.wantScore {
				t.Errorf("score=%d, ждали %d", score, c.wantScore)
			}
			if found && pageURL != "" {
				t.Errorf("url=%q, ждали пустой url", pageURL)
			}
		})
	}
}

func TestParseOpenCriticGameURL(t *testing.T) {
	score, found, pageURL, err := parseOpenCriticGame([]byte(`{
		"topCriticScore": 84.6,
		"url": "https://opencritic.com/game/4503/assassins-creed-origins"
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !found || score != 85 {
		t.Fatalf("score=%d found=%v, ждали 85 true", score, found)
	}
	if pageURL != "https://opencritic.com/game/4503/assassins-creed-origins" {
		t.Fatalf("url=%q", pageURL)
	}
}

func TestParseOpenCriticPlayerRating(t *testing.T) {
	score, count, found, err := parseOpenCriticPlayerRating([]byte(`{
		"_id": 1660,
		"median": 70,
		"count": 57
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !found {
		t.Fatal("found=false, ждали true")
	}
	if score != 70 {
		t.Fatalf("score=%d, ждали 70", score)
	}
	if count != 57 {
		t.Fatalf("count=%d, ждали 57", count)
	}
}

func TestParseOpenCriticPlayerRatingTreatsMissingMedianAsNoData(t *testing.T) {
	_, _, found, err := parseOpenCriticPlayerRating([]byte(`{
		"_id": 1660,
		"median": null,
		"count": 0
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if found {
		t.Fatal("found=true, ждали false")
	}
}
