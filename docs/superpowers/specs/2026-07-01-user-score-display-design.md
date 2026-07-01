# User Score Display Design

## Goal

Show newly collected Metacritic user scores and OpenCritic player ratings in the
catalog UI without losing the existing overall rating workflow.

The catalog should support three rating concepts:

- Overall average: all available score sources.
- Critic average: critic-only sources.
- Player average: user/player sources, including HLTB Rating.

## Current Context

The database already stores the new raw fields:

- `metacritic_user_score`
- `metacritic_user_count`
- `opencritic_player_score`
- `opencritic_player_count`

The sync path writes those fields through `UpdateMetacriticScores` and
`UpdateOpenCriticScores`. The UI does not yet read or render them. The current
`average_score` formula uses only Metacritic critic score, OpenCritic critic
score, and HLTB Rating.

## Rating Semantics

`average_score` remains the main "Среднее" metric, but its formula changes. It
is calculated from these five sources:

- `metacritic_score`
- `metacritic_user_score`
- `opencritic_score`
- `opencritic_player_score`
- `hltb_rating`

Only values greater than zero participate in averages. `NULL` and `0` mean "no
usable score" and are skipped. If no usable score exists, the average is `NULL`.

Add two stored summary columns:

- `critic_average_score`: average of `metacritic_score` and `opencritic_score`.
- `player_average_score`: average of `metacritic_user_score`,
  `opencritic_player_score`, and `hltb_rating`.

This keeps filtering and sorting simple and matches the current stored
`average_score` pattern.

## Data Flow

Add migrations for `critic_average_score` and `player_average_score`.

Replace the current single-average recomputation with a recomputation step that
updates all three stored averages after any score source changes. The affected
write paths are:

- `UpdateMetacriticScores`
- `UpdateOpenCriticScores`
- `UpdateHLTB`
- `RecomputeAllAverages`

For compatibility, keep the existing `average_score` column name and update its
meaning to the new five-source overall formula.

## Query API

Extend `store.ListParams` with critic and player score ranges:

- `CriticFrom`
- `CriticTo`
- `PlayerFrom`
- `PlayerTo`

`NormalizeParams` applies the same inverted-range behavior used by the existing
average and HLTB filters: if both bounds are set and upper is lower than lower,
drop that pair.

`ListGames` adds filters for:

- `critic_average_score >= ?`
- `critic_average_score <= ?`
- `player_average_score >= ?`
- `player_average_score <= ?`

Extend sorting with:

- `critic`: `critic_average_score`
- `player`: `player_average_score`

Rows with `NULL` in the sorted column continue to sort last.

## View Model

Extend `store.GameView` with:

- `MetacriticUser`
- `MetacriticUserCount`
- `OpenCriticPlayer`
- `OpenCriticPlayerCount`
- `CriticAverage`
- `PlayerAverage`

The existing `MetacriticURL` and `OpenCriticURL` methods remain sufficient for
source links. The new user/player details link to the same source pages as their
critic counterpart.

## UI

The approved card direction is "summary first, details below".

Each card shows a top rating row:

- `Среднее`: overall five-source average.
- `Критики`: critic average.
- `Игроки`: player average.

The HLTB row remains visible for completion time and HLTB Rating.

Below the summary, render compact source details:

- MC critic
- MC user
- OC critic
- OC player

Show `—` when a source has no usable value. Show user/player counts in small
secondary text when count fields are available.

The filters keep the current `Средний рейтинг от / до` range and add:

- `Оценки критиков от / до`
- `Оценки игроков от / до`

Sorting keeps the existing options and adds:

- `По оценке критиков`
- `По оценке игроков`

`По средней оценке` sorts by the new five-source `average_score`.

## Error Handling And Edge Cases

Missing user/player scores are expected and should render as `—`.

Zero score values do not participate in any average and render as missing in the
summary/detail UI. This avoids treating API placeholders such as `0` as real
negative sentiment.

If counts are unavailable, omit the count text rather than showing `0` unless
the source explicitly returned a real count of zero alongside no score.

## Testing

Add or update store tests for:

- Recomputing overall, critic, and player averages with mixtures of valid
  values, `NULL`, and `0`.
- Filtering by critic score range.
- Filtering by player score range.
- Sorting by critic and player averages, including `NULL` sorted last.
- Loading the new raw score fields and averages into `GameView`.

Run:

- `go test ./...`
- `go vet ./...`

If a local database is available, also run the server and inspect the catalog
page manually.
