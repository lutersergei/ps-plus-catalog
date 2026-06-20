package scores

import (
	"fmt"
	"io"
)

// Лимиты на размер тела ответа внешних сервисов: защита от ошибочного,
// изменившегося или скомпрометированного endpoint'а, способного отдать
// гигантский ответ и исчерпать память. JSON-ответы (OpenCritic, HLTB) малы;
// HTML-страница Metacritic заметно крупнее, поэтому лимит для неё отдельный.
const (
	maxJSONBytes = 8 << 20  // 8 MiB
	maxHTMLBytes = 16 << 20 // 16 MiB
)

// readLimited читает не более limit байт из r и возвращает явную ошибку, если
// тело ответа превышает лимит (а не молча обрезает его).
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("ответ превышает лимит %d байт", limit)
	}
	return body, nil
}
