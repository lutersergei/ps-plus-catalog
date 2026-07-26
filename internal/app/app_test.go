package app

import (
	"bytes"
	"testing"
)

func TestParseAPIKeysTrimsAndDeduplicates(t *testing.T) {
	got := parseAPIKeys(" first,second; first\nthird ", "second")
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("keys=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("keys=%v, want %v", got, want)
		}
	}
}

func TestRunReturnsUsageCodeForUnknownCommand(t *testing.T) {
	t.Setenv("PS_EXTRA_ENV_FILE", t.TempDir()+"/missing.env")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"unknown"}, Assets{}, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestDisplayURLAddsLocalhostForPortOnly(t *testing.T) {
	if got := displayURL(":8080"); got != "http://localhost:8080" {
		t.Fatalf("displayURL=%q", got)
	}
}
