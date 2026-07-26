// Package envfile читает простой .env-файл (KEY=VALUE) в переменные окружения.
package envfile

import (
	"bufio"
	"os"
	"strings"
)

// Load читает .env по пути path и выставляет переменные через os.Setenv.
// Уже заданные в окружении переменные НЕ перезаписываются (приоритет у OS env).
// Отсутствие файла — не ошибка (возвращает nil).
func Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, val, ok := parseLine(sc.Text())
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, val); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// parseLine разбирает строку вида `KEY=VALUE` (с опциональным `export `,
// комментариями `#` и кавычками вокруг значения).
func parseLine(line string) (key, val string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:eq])
	val = strings.TrimSpace(line[eq+1:])
	// снять обрамляющие кавычки
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
	}
	if key == "" {
		return "", "", false
	}
	return key, val, true
}
