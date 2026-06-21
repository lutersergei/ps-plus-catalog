# Language Badges Alignment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Display `RU Sub` and `RU Дубляж` as two equal, centered badges without changing the rest of the game card.

**Architecture:** Keep the existing HTML structure and change only the embedded CSS in `templates/index.html`. Add a focused Go test that parses the template source and guards the required grid and centering declarations.

**Tech Stack:** Go `testing`, embedded HTML/CSS template.

---

### Task 1: Lock the badge layout with a regression test

**Files:**
- Create: `template_test.go`
- Read: `templates/index.html`

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"strings"
	"testing"
)

func TestLanguageBadgesUseEqualCenteredColumns(t *testing.T) {
	required := []string{
		".lang-badges { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:6px;",
		".lang-badge { display:flex; align-items:center; justify-content:center;",
		"min-height:24px;",
	}
	for _, declaration := range required {
		if !strings.Contains(indexHTML, declaration) {
			t.Errorf("index template does not contain %q", declaration)
		}
	}
}
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```sh
go test . -run TestLanguageBadgesUseEqualCenteredColumns -count=1
```

Expected: FAIL because `.lang-badges` still uses flexbox and `.lang-badge` does
not center its contents.

### Task 2: Implement the equal badge grid

**Files:**
- Modify: `templates/index.html:58-61`
- Test: `template_test.go`

- [ ] **Step 1: Replace only the language badge CSS**

Use:

```css
.lang-badges { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:6px; margin-top:2px; }
.lang-badge { display:flex; align-items:center; justify-content:center; min-height:24px; font-size:10px; font-weight:700; padding:2px 6px; border-radius:4px; letter-spacing:.3px; text-align:center; }
```

Keep `.lang-badge.sub`, `.lang-badge.voice`, the badge HTML, and all score-card
styles unchanged.

- [ ] **Step 2: Run the focused test and verify GREEN**

Run:

```sh
go test . -run TestLanguageBadgesUseEqualCenteredColumns -count=1
```

Expected: PASS.

- [ ] **Step 3: Run the complete verification suite**

Run:

```sh
go test ./...
go vet ./...
go build ./...
git diff --check
```

Expected: all commands exit with status 0.

- [ ] **Step 4: Inspect the scoped diff**

Run:

```sh
git diff -- templates/index.html template_test.go
```

Expected: only the language-badge CSS and its regression test are added; existing
unrelated edits in `templates/index.html` remain intact.

- [ ] **Step 5: Commit**

```sh
git add templates/index.html template_test.go
git commit -m "fix: align language badges"
```
