package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	content := "# комментарий\n" +
		"export OPENCRITIC_API_KEYS=key1, key2 ,key3\n" +
		"OPENCRITIC_API_KEY=\"single\"\n" +
		"EMPTY=\n" +
		"PRESET=fromfile\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// isolateEnv сбрасывает переменную окружения и восстанавливает исходное
	// значение (или повторный unset) после теста.
	isolateEnv := func(key string) {
		orig, had := os.LookupEnv(key)
		os.Unsetenv(key)
		t.Cleanup(func() {
			if had {
				os.Setenv(key, orig)
			} else {
				os.Unsetenv(key)
			}
		})
	}
	isolateEnv("OPENCRITIC_API_KEYS")
	isolateEnv("OPENCRITIC_API_KEY")

	t.Setenv("PRESET", "fromenv") // должен сохраниться (приоритет OS)

	if err := Load(p); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("OPENCRITIC_API_KEYS"); got != "key1, key2 ,key3" {
		t.Errorf("KEYS = %q", got)
	}
	if got := os.Getenv("OPENCRITIC_API_KEY"); got != "single" {
		t.Errorf("KEY (кавычки сняты) = %q", got)
	}
	if got := os.Getenv("PRESET"); got != "fromenv" {
		t.Errorf("PRESET должен остаться fromenv (приоритет OS), got %q", got)
	}
	if err := Load(filepath.Join(dir, "nope.env")); err != nil {
		t.Errorf("отсутствующий файл не ошибка, got %v", err)
	}
}
