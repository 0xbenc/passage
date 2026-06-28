# TUI Interaction Contract

> **Status: DRAFT** (prototyped on passage; to be reconciled against ssherpa's live-PTY
> overlay + wizard surfaces and **frozen** in TUI-alignment phase P6.5).
> Canonical home: this file in the `passage` repo. `ssherpa` carries a pointer stub to it.
> This is the **interaction source of truth** for both TUIs: any PR changing an
> interactive surface must keep it and both apps conformant.

App-agnostic. It describes interaction *grammar and behavior*, not any one app's code.
Pure *style* (box geometry, palette role values, footer grammar) lives in the shared
`termchrome`/`termtheme` modules and is out of scope here.

## 1. Key grammar

- **Lowercase letters always type into the fuzzy filter.** Fuzzy matching is
  case-insensitive, so lowercase is never needed as a command — it is reserved for
  filtering on every screen with a filter.
- **UPPERCASE (shifted) letters are commands on the current selection** (e.g. new,
  edit, delete, toggle-pin). They never leak into the filter.
- **`ctrl`-chords are action verbs** (copy, reveal, etc.), app-specific.
- **Emacs cursor motion is universal:** `ctrl+p` = up, `ctrl+n` = down, in addition to
  the arrow keys. (Because `ctrl+p`/`ctrl+n` are nav, no command may bind them.)
- **Navigation:** `up`/`down` move; `pgup`/`pgdn` page; `home`/`end` jump to
  first/last; `shift+up`/`shift+down` jump by section where the list is grouped.
  Horizontal arrows are swallowed in lists (never leak into the filter).
- **Quit is letter-free on every screen:** `esc`, `ctrl+c`, `ctrl+q`. No letter
  (no `q`/`Q`) quits or goes back — letters are reserved for filter/commands so the
  fuzzy filter is unambiguous everywhere.
- **`esc` is "back / cancel one level":** it closes the active overlay, cancels the
  current step, or (at the top level) quits. It is the universal "back".
- **Help:** `?` opens the grouped key reference **only when the filter is empty**, so a
  query containing `?` can still be typed.

## 2. Selection cue

- **Required canonical cue: a `">>"` 2-cell caret on the selected list row** in every
  list context (entry/host pickers, file/dir browsers, completion-candidate lists,
  role lists). Unselected rows lead with two spaces so columns align. The active
  **text-field cursor** is distinct (a block cursor), not a list caret.
- **Optional enhancement — full-row selection bar:** an app MAY paint a full-row
  `RoleSelectedBar` background on the selected row **of a top-level, full-width
  picker list only**. It is never painted in a preview-pane split, a live overlay, a
  wizard/form, or a chooser. Where painted, the `">>"` caret remains the no-color
  fallback (so selection is legible with `NO_COLOR`).

## 3. Filter / fuzzy semantics

- Printable characters fuzzy-filter the visible list; matched runes are highlighted in
  the search role on every row (shared `render.HighlightMatches`).
- Ranking is app-defined but should be stable and put the most relevant rows first.
- A zero-match filter echoes the (sanitized) query rather than rendering an empty void.

## 4. Footer grammar

- Footers are built from key hints via the shared `termchrome.Footer([]KeyHint, width)`:
  `"key label / key label"` with the canonical `" / "` separator and progressive `+N`
  overflow when the hints exceed the width. No hand-built multi-space separators.
- Footer hint **order** is meaningful (most important first; head survives overflow).

## 5. Per-surface expectations

| Surface | Notes |
|---|---|
| Entry/host picker | Full key grammar; `">>"` caret (+ optional bar per §2); `?` help; section jump if grouped. |
| File / directory browser | List caret `">>"`; `enter` opens/selects; `esc` cancels; loading/error/empty states rendered. |
| Forms / single field | Block text-field cursor (not `">>"`); `enter` advances/saves; `esc` cancels; inline validation error line. |
| Completion-candidate list | List caret `">>"` on the highlighted candidate. |
| Theme editor | List caret `">>"`; arrows change; `esc` closes; key grammar as above. |
| Wizard / step rail | **App-local UX** (see §6) — the step-rail *composition* stays per app; only the key grammar (nav + letter-free quit) is shared. |

## 6. App-local (to be finalized in P6.5)

These surfaces exist in only one app today, so their detailed UX is reconciled against
the real implementation before this contract is frozen, and the genuinely app-specific
parts stay app-local:

- **Live-PTY session overlay** (ssherpa): key/quit grammar and the bottom-strip paint
  mechanics. The overlay must honor letter-free quit and `esc`=back; its raw-transcript
  rendering keeps its own `Strip` overflow policy (not the trusted-chrome `Sanitize`).
- **Multi-step wizard / step rail** (ssherpa): the `✓ ● ○` progress rail composition is
  app-local; the navigation and quit grammar are shared.

## 7. Non-goals / intentional divergences

- Apps keep their own **builtin palettes** and **brand** colors (e.g. a brand border
  color is not canonicalized).
- Apps keep their own **shell composition** (`renderWorkflowShell`/`workflowShell`) and
  their own overflow `Truncator` policy (trusted chrome = `Sanitize`; raw transcript =
  `Strip`).
- The two apps' **pickers are different primitives** (an async action shell vs a
  return-a-selection menu) and are not unified; only the grammar above is shared.
- Secret-display surfaces (redaction, focus-hiding) are domain-specific and not shared.
