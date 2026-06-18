# Источники оценок — находки (Task 5)

Дата: 2026-06-18.

## Metacritic (подтверждено вживую)

- URL страницы игры: `https://www.metacritic.com/game/<slug>/` (БЕЗ суффикса
  платформы — `/playstation-5/` даёт 301).
- `slug`: из английского названия — нижний регистр, спецсимволы убрать, пробелы → `-`.
- curl проходит (HTTP 200, ~900КБ), Cloudflare не блокирует при обычном
  `User-Agent` + `accept-language: en-US`.
- **Оценка** — в JSON-LD: `<script type="application/ld+json">` содержит объект с
  `"aggregateRating": {"@type":"AggregateRating","name":"Metascore","bestRating":100,
  "ratingValue":<score>}`. Берём `ratingValue` из того ld+json, где `aggregateRating.name == "Metascore"`.
  - Проверено: Death Stranding → `ratingValue: 82`.
- Если игры нет / нет рецензий — ld+json без Metascore-aggregateRating → оценка N/A.

Фикстура: `testdata/metacritic_death_stranding.html`.

## OpenCritic (RapidAPI) — форма по документации, живую проверку делает пользователь

Ключ передаётся через env `OPENCRITIC_API_KEY`; в этой среде не вводился (из
соображений безопасности — пользователь запускает sync со своим ключом сам).

- Host: `opencritic-api.p.rapidapi.com`. Заголовки:
  `X-RapidAPI-Key: <key>`, `X-RapidAPI-Host: opencritic-api.p.rapidapi.com`.
- Поиск: `GET /game/search?criteria=<urlenc name>` →
  массив `[{ "id": <int>, "name": "<...>", "dist": <float, меньше = ближе> }]`.
- Игра: `GET /game/<id>` → объект с `topCriticScore` (float 0–100),
  `tier`, `name`, `percentRecommended`, `firstReleaseDate`, `Platforms`.
- Берём лучшее совпадение поиска (минимальный `dist` и/или совпадение
  нормализованного названия), затем `topCriticScore` из `/game/<id>`,
  округляем до int.

### Лимиты плана Basic (важно для sync)

- **25 поисков/день**, 200 запросов/день, 4 req/s (hard limit).
- Значит, за один прогон собираем оценки максимум ~25 новых игр. Каталог ~449 игр →
  полный сбор за ~18 дней. Поэтому критичны: кэш в SQLite, `scores_updated_at`,
  и флаг лимита на число OpenCritic-обращений за запуск.
