package scores

import "testing"

func TestParseMetacriticUserStats(t *testing.T) {
	score, count, found, err := parseMetacriticUserStats([]byte(`{
		"data": {
			"item": {
				"max": 10,
				"score": 7.8,
				"reviewCount": 980
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !found {
		t.Fatal("found=false, ждали true")
	}
	if score != 78 {
		t.Fatalf("score=%d, ждали 78", score)
	}
	if count != 980 {
		t.Fatalf("count=%d, ждали 980", count)
	}
}

func TestParseMetacriticUserStatsTreatsMissingScoreAsNoData(t *testing.T) {
	_, _, found, err := parseMetacriticUserStats([]byte(`{
		"data": {
			"item": {
				"max": 10,
				"score": null,
				"reviewCount": 0
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if found {
		t.Fatal("found=true, ждали false")
	}
}
