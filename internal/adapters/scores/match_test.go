package scores

import "testing"

func TestTitlesMatch(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Hades", "Hades", true},                                   // точное
		{"Death Stranding Directors Cut", "Death Stranding", true}, // издание ↔ база
		{"Death Stranding", "Death Stranding Directors Cut", true}, // симметрично
		{"God of War", "God of War Ragnarok", false},               // разные части серии
		{"God of War Ragnarok", "God of War", false},               // и в обратную сторону (база ≠ сиквел)
		{"Hades", "Hades II", false},                               // сиквел
		{"Hades II", "Hades", false},                               // сиквел, обратно
		{"Gris", "Tetris", false},                                  // короткое чужое
		{"It Takes Two", "Takes", false},                           // частичное пересечение
		{"Spider-Man Remastered", "Spider-Man", true},              // ремастер ↔ база
		{"Hogwarts Legacy PS5 Version", "Hogwarts Legacy", true},   // платформа + висящий хвост
		{"Terraria – PlayStation®4 Edition", "Terraria", true},     // PlayStation edition ↔ база
		{"FARCRY 3 Classic Edition", "Far Cry 3 Classic Edition", true},
		{"Marvel's Guardians of the Galaxy PS4 & PS5", "Guardians of the Galaxy", true},
		{"Celeste", "Celeste", true},
	}
	for _, c := range cases {
		if got := TitlesMatch(c.a, c.b); got != c.want {
			t.Errorf("TitlesMatch(%q,%q)=%v, ждали %v", c.a, c.b, got, c.want)
		}
	}
}

func TestBestHLTB(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		data      []hltbGame
		wantName  string
		wantFound bool
	}{
		{"пусто", "Hades", nil, "", false},
		{
			name:      "точное совпадение",
			title:     "Hades",
			data:      []hltbGame{{GameName: "Hades II"}, {GameName: "Hades"}},
			wantName:  "Hades",
			wantFound: true,
		},
		{
			name:      "подзаголовок принимается",
			title:     "Death Stranding Directors Cut",
			data:      []hltbGame{{GameName: "Death Stranding"}},
			wantName:  "Death Stranding",
			wantFound: true,
		},
		{
			name:      "чужой сиквел отвергается",
			title:     "Hades",
			data:      []hltbGame{{GameName: "Hades II"}},
			wantFound: false,
		},
		{
			// регрессия: для нашей игры-сиквела база не должна приниматься за неё
			name:      "база не принимается за сиквел (многословное название)",
			title:     "God of War Ragnarok",
			data:      []hltbGame{{GameName: "God of War"}},
			wantFound: false,
		},
		{
			name:      "короткое чужое название отвергается",
			title:     "Gris",
			data:      []hltbGame{{GameName: "Tetris"}},
			wantFound: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, found := bestHLTB(c.data, c.title)
			if found != c.wantFound {
				t.Fatalf("found=%v, ждали %v", found, c.wantFound)
			}
			if found && g.GameName != c.wantName {
				t.Errorf("name=%q, ждали %q", g.GameName, c.wantName)
			}
		})
	}
}
