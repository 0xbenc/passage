# Making passage a Claude Code grade TUI

*A decision-ready roadmap for the maintainer. Every claim below was checked against the real source under `internal/` and `$(go env GOMODCACHE)/charm.land/bubbletea/v2@v2.0.7`. Where the source contradicts a popular assumption, the source wins.*

---

## 1. North Star

"Claude Code grade" is not "more color." It is a small set of felt properties that a user notices in the first ten seconds and trusts by the tenth use. For a password-store TUI specifically, the bar is:

| # | Property | Measurable acceptance test | Today |
|---|----------|---------------------------|-------|
| N1 | **Fuzzy search with live match highlight** | Typing `gh` matches `work/github/token`; the matched runes render in `RoleSearch` on every row; ranking is pins → score → recency | ❌ substring only (`strings.Contains`, picker.go:464), `RoleSearch` defined but **never rendered** |
| N2 | **Filled selection bar + pill chrome** | The cursor row is a solid background band spanning the full inner width *including the metadata column*; pinned/MFA are pills, not `*`/`mfa` | ❌ caret + underline only; `right` column sits outside any fill (picker.go:451); `RoleSelected` = `39;4`, no background |
| N3 | **Motion that informs** | A gpg decrypt >150ms shows an animated spinner + elapsed; a TOTP shows a live-decrementing countdown bar that auto-dismisses at zero | ❌ **zero** `tea.Tick` in the codebase; busy box is static; countdown is a frozen `"%ds remaining"` (reveal.go:89, picker.go:719) |
| N4 | **Responsive adaptive layout** | ≥92 cols: a non-secret detail pane appears; <92 cols / NO_COLOR: byte-near today's single column | ❌ single column at all widths |
| N5 | **Discoverable, non-cramped affordances** | A contextual one-line hint; full keys behind `?`; no 11-chord firehose | ❌ cramped 2-line dump (picker.go:344-347) |
| N6 | **Delightful empty / error / loading states** | Empty vault → guided welcome; zero-match → echoes the (sanitized) query; cold start → branded "loading vault" frame | ⚠️ terse single warnings, no next-step |
| N7 | **Perceptual, capability-aware color** | Vivid theme is reachable from config; truecolor auto-selected only when the profile is actually TrueColor; TerminalTheme is the guaranteed floor | ❌ `ResolveTheme` hardcodes `TerminalTheme()`, ignoring `cfg.BaseName`/`opts.Name` (theme.go:155) — **VividTheme is unreachable from config** |
| N8 | **Rock-solid degradation** | Every layer above has a named ASCII/16-color/NO_COLOR/narrow/`--no-alt-screen` fallback; keyboard is always sufficient | ✅ the substrate (escape-aware `Truncate`/`PadRight`, 48-col clamp) is excellent; the *new* chrome must not regress it |
| N9 | **Secrets stronger, never weaker** | Reveal dwell shortened; alt-tab blanks a secret; `--no-alt-screen` scrubs the last frame; all untrusted text Sanitized at render | ⚠️ `Sanitize` exists, is unit-tested, and has **zero non-test callers**; inline mode leaves the secret in scrollback |

**The gap in one sentence:** passage has a *beautifully disciplined rendering substrate* (escape-aware width math, a single bordered shell, a role-based theme) and *almost none of the felt layer built on top of it* — no fuzzy match, no motion, no second region, and the two security-relevant primitives it already shipped (`Sanitize`, `RoleSearch`) are dead code.

---

## 2. Current State Assessment

### Strengths (do not break these)

- **The shell is the right abstraction.** `renderWorkflowShell` (shell.go) is the single bordered box every screen renders into, clamped to `max(48, width)` (shell.go:17,42). One place owns chrome geometry. This is what makes a second region (C7) *expressible* rather than a per-screen rewrite.
- **`termstyle` is genuinely escape-safe.** `VisibleWidth`/`Strip`/`Truncate`/`PadRight` skip SGR/CSI/OSC and never split a sequence; `Truncate` re-appends a reset when styling is left open and marks cuts with `~`. `Sanitize` correctly drops C0/C1/DEL (the real ANSI-injection introducers). This is the security substrate every visual change builds on.
- **The runtime already does color downsampling for you.** Bubble Tea v2's cursed renderer parses `View.Content` into a cell grid via `uv.NewStyledString` and converts every cell through the detected `colorprofile` — so VividTheme's `38;2;r;g;b` truecolor does **not** garble a 16-color xterm *through the TUI*. The cell buffer is also a bleed firewall: a full-width background does not smear across rows (refuting C8's stated fear).
- **`View()` already returns a `tea.View` struct** (picker.go:294) and already sets `AltScreen`. Adding `Cursor`, `MouseMode`, `ReportFocus`, `WindowTitle` is one line each — verified fields at tea.go:131,143,170,177.
- **The theme model is role-based and complete.** 15 roles including the unused `RoleSearch` (theme.go:75,98) and `RolePill` (theme.go:76,99). `NoColor` makes `Apply` a no-op, so degradation is automatic when codes are empty.

### Limitations that hold it back

- **Search is substring, not fuzzy** (picker.go:464). `gh` will not find `work/github`. This is the single biggest "feels old" tell.
- **The selection is a caret, not a bar** (picker.go:418-451). The `right` column (markers + timestamp) is composed *outside* any styling, so a background fill would stop before the metadata column and look broken.
- **There is no motion infrastructure at all.** Grep confirms **zero** `tea.Tick`/`tea.Every`/`tickMsg` in `internal/`. The busy box, the reveal countdown, and any spinner are all static. The idle app is correctly cold — we must keep it that way.
- **`ResolveTheme` is a dead path** (theme.go:155): it hardcodes `TerminalTheme().Normalized()` and never reads `cfg.BaseName` or `opts.Name`. VividTheme is literally unreachable from config — a latent bug, not a design choice (though a deprecation test pins the current behavior).
- **`wrapSecret` byte-slices a UTF-8 secret** (reveal.go: `cut := min(width, len(value)); value[:cut]`), splitting a multibyte secret mid-rune on narrow terminals — a corruption bug on the secret-display path.
- **Two real security primitives are dead code:** `Sanitize` (zero non-test callers) and `RoleSearch` (defined, never rendered). Untrusted entry paths flow to the error line through escape-aware-but-not-C1-stripping `Truncate`.
- **`--no-alt-screen` leaks the last secret frame.** The inline renderer's `close()` does `MoveTo(bottom)` then `EraseScreenBelow` (cursed_renderer.go:170) — which erases nothing above the cursor, so a revealed password persists in scrollback.
- **The footer is a 2-line, 11-chord firehose** (picker.go:344). No contextual hints, no `?` overlay.

---

## 3. The Architecture Decision

> **Recommendation: Elevate the hand-rolled stack. Do NOT adopt lipgloss/v2 or bubbles.** Grow `termstyle` from a flat SGR-string library into a small in-house **color + width + match toolkit**, leaning on already-transitive low-level primitives (`x/ansi`, `runewidth`/`displaywidth`, `colorprofile`) where they save real code — and spend that capital on the four things users feel (N1–N4).

This is the pivotal call. Here is the defense.

**The lipgloss case is real but loses on the merits.** `charm.land/lipgloss/v2@v2.0.3` is in the cache and shares passage's *exact* transitive deps (ultraviolet, colorprofile, go-colorful, runewidth) — adoption pulls almost no new modules. It offers a `color.Color` theme model, `JoinHorizontal`/`Place`, a `Canvas`/`Layer` compositor for floating modals, and grapheme-correct width. The "charm-native-rewrite" stance scored 80, one point behind. It is a *defensible* path.

It loses for three reasons specific to passage:

1. **It dissolves passage's defining trait.** The whole identity is "100% hand-rolled, security-reviewed rendering." A password manager's render path is a security surface; `termstyle.Sanitize`, the escape-aware `Truncate`, and the time-limited reveal are auditable in 244 lines. Moving the render path into lipgloss means the security review now spans a much larger, faster-moving third-party surface. The reviewability tax is paid forever.

2. **The marquee lipgloss feature — the `Canvas`/`Layer` floating-modal compositor — is the one we already decided to drop** (see §7). Modals in a bordered single-box TUI read fine as in-shell panels. We are not buying the compositor's complexity.

3. **The win lipgloss genuinely offers (grapheme-correct width) is available without it.** The renderer's own width measure is `ansi.WcWidth` backed by a **private** `runewidth.Condition{EastAsianWidth:false, StrictEmojiNeutral:true}` (verified at `x/ansi@v0.11.7/method.go`). We can call `ansi.StringWidth` directly — it is already transitive — and get width math that is *byte-for-byte identical to what the renderer paints*. That is strictly better than lipgloss's measure for our purpose (matching the painter), and it is the C1 fix.

**The crucial nuance that makes "elevate" safe:** the naive `runewidth.RuneWidth` call (the C1 sketch as written) is a *latent cross-environment bug* — its package-level `DefaultCondition.EastAsianWidth` is set from `LANG`/`LC_CTYPE` at init, so in a CJK locale it counts box-drawing glyphs `╭╮╰╯├┤─` as width 2 and *desyncs from the renderer*, tearing the very box it measures. We avoid this entirely by routing through `ansi.StringWidth` (or a package-private `Condition` mirroring the renderer's), which is locale-independent and honors only `RUNEWIDTH_EASTASIAN`. **This is why "elevate" is not just cheaper — it is the only path that keeps passage's width math provably in lockstep with the painter.**

**Net:** zero new top-level dependencies. `runewidth`/`displaywidth`/`colorprofile`/`x/ansi` are already in the build graph (verified in go.mod and go.sum). The binary stays fast-starting and dependency-light (`pass`/`gpg`/clipboard only at runtime). The security-critical paths stay hand-rolled and 244-lines-auditable. We reach ~85% of the lipgloss feel for ~45% of the risk.

---

## 4. The Roadmap

Phases are ordered by dependency. Within a phase, by impact-per-effort. Each change carries its verified feasibility note; **infeasible items are dropped with reasons.**

### Phase 0 — Foundation (correctness the pillars stand on)

| id | What | Why | Files | Effort | Risk |
|----|------|-----|-------|--------|------|
| **C1** | Cell-accurate width via `ansi.StringWidth` | Box edge + timestamp column tear on CJK/emoji; every layout pillar depends on this | `internal/termstyle/termstyle.go` | M | med |
| **C2** | Fix `wrapSecret` to cut on rune boundaries | Byte-slicing splits a multibyte secret mid-rune on narrow terminals — corruption on the secret path | `internal/ui/reveal.go` | S | low |
| **C6** | Single `layoutSpec` per frame | Three drifting budgeters (`availableListLines` :324, `pageSize` :525, `pickerShellStructuralLines` :349) make a second region un-expressible | `internal/ui/picker.go`, `shell.go` | M | med |

> **C1 — implementation correction (mandatory):** do **not** call package-level `runewidth.RuneWidth`. Call `ansi.StringWidth` (already transitive; byte-identical to the renderer's `WcWidth`) **or** hold a package-private `&runewidth.Condition{EastAsianWidth:false, StrictEmojiNeutral:true}`. Keep the escape-skip loop unchanged; make `Truncate`/`PadRight` budget in *cells*, never split a 2-cell rune. Deliberately update golden tests: `TestTruncate` "multibyte cut" `日本語表記,3 → 日~`; in the Strip/VisibleWidth tables, replace `len([]rune(want))` with an explicit cell count. Add a regression test asserting box-glyph width stays 1 with `RUNEWIDTH_EASTASIAN` unset (guards the locale landmine).
>
> **C2 — drop the go-runewidth framing from the original description** (it self-contradicts). Walk runes with `for i, r := range value` matching the existing rune-count convention; assert `utf8.ValidString` per line + `VisibleWidth ≤ width`. Zero new deps.
>
> **C6 — note:** no existing test references `pageSize`/`normalizedScroll`, so C6 has *no current automated guard*. It MUST **add** a `pageSize` + `ensureVisible` scroll test. Carry forward the existing floors (`max(5,...)`, `max(1, available)`, the 48-col clamp). Scope the "bit-identical pageSize" guarantee to the no-message steady state; reconciling `pageSize` to real body height is a (welcome) behavior fix when a message is showing.

### Phase 1 — Search & Layout (the headline feel)

| id | What | Why | Files | Effort | Risk |
|----|------|-----|-------|--------|------|
| **C3** | `internal/fuzzy` package: fzf-style integer scoring + match spans | The single biggest "feels modern" upgrade; pure dependency-free leaf library, golden-testable | `internal/fuzzy/*` (new) | L | low |
| **C4** | Wire fuzzy ranking into the picker filter | Replace `strings.Contains`; rank pins → score → recency → store order | `internal/ui/picker.go` | L | high |
| **C5** | Match highlighting in `renderEntryLine`, Truncate-safe | Light up the dead `RoleSearch`; re-open base role after each span (Apply emits a full reset) | `internal/ui/picker.go`, `theme.go` | L | med |
| **C7** | `Region`/`joinHorizontal` + non-secret detail pane (≥92 cols) | The N4 win; full Path, pill state, relative LastUsed, hints — **never** decrypts | `internal/ui/shell.go`, `picker.go` | L | med |

> **C3** — port fzf's **integer** model (deterministic for goldens): match base, post-delimiter boundary bonus (delimiters `/,:;|` so `gh` → `work/github`), consecutive bonus = 4, first-char ×2, gap penalties, smart-case derived from the query. Return `(score int, positions []int, ok bool)` where positions are **plain-text rune indices**. Golden-fix the delimiter case, empty query, all-uppercase smart-case, no-match, and a multibyte candidate.
>
> **C4** — convert `m.filtered []int` → `[]scoredEntry{idx, positions}`; update all 8 reads (picker.go:110-111, :254, :374, :388, :399-413, :455-468, :475-520, :741-744). Use `sort.SliceStable` with a fully deterministic composite key. Re-point `TestPickerPrintableTextFilters` and `TestPickerCtrlFTogglesMFAOnly` to `.idx`. **Two non-obvious requirements:** (a) **route the picker filter and the CLI auto-run/JSON path (cli.go:239) through one shared ranking fn** — otherwise single-match auto-copy/auto-TOTP counts matches with different semantics than the picker shows, which in a password manager can auto-act on an unintended entry; keep auto-run threshold-gated, not silently widened. (b) Defer the `positions` field if C5 isn't landing in the same PR — unused span tracking is wasted per-keystroke allocation.
>
> **C5 — this is a *rewrite* of `renderEntryLine`'s styling branch (picker.go:442-447), not an addition.** `termstyle.Apply` wraps with a *full* `\x1b[0m` reset, so a highlight run drops the row color mid-line. Build the title as `baseOpen + [plain | Apply(RoleSearch,run) + baseReopen]* + baseReset`. Clip positions by **rune** index to the Truncate-kept prefix (excluding the `~` marker). `RoleSearch` already exists in both themes — **do not add a role.** Cursor row re-opens `RoleSelected`, not `RolePrimary`. Tests at width 48: trailing segment still carries base SGR; `Strip(line) == plain Truncated text`; no stray escapes.
>
> **C7** — compute regions so `listColWidth + 1 + detailColWidth == width-4` exactly (else `workflowLine` re-truncates the joined row and mangles the divider). Route the full Path through **`termstyle.Sanitize`** before hard-wrapping (the pane shows the untruncated path — a larger control-byte surface than today's single column). The pane never calls `Show`/decrypt — add a test at width ≥92 locking the no-decrypt invariant. Existing width-72 view test stays green, proving below-breakpoint output is unchanged.

### Phase 2 — Motion & Polish (the gated tick discipline)

| id | What | Why | Files | Effort | Risk |
|----|------|-----|-------|--------|------|
| **C9** | Gated `tea.Tick` security clock | The load-bearing motion primitive; idle stays cold, no goroutines, self-terminating | `internal/ui/picker.go` | M | med |
| **C12** | Animated busy spinner + elapsed timer | Distinguish a slow gpg from a hang above ~150ms | `internal/ui/picker.go` | M | med |
| **C10** | Live TOTP countdown bar that auto-dismisses *(descoped)* | "Most-expected animation in a password manager" — **for TOTP only** | `internal/ui/picker.go`, `reveal.go` | M | med |
| **G6** | `countdownBar` primitive + urgency role ramp | success→warning→danger as it drains; ASCII `[####----]` fallback | `internal/termstyle/termstyle.go` | M | low |
| **G5** | `GlyphSet` capability table (nerd/unicode/ascii) | The "named fallback" promise has *no mechanism* without it; spinner/bar otherwise hardcode Unicode that breaks on cp437 | `internal/termstyle/termstyle.go` | M | med |

> **C9 — scope the gate to state that exists.** `m.clipArmed` and "decrement clipboard remaining" reference a subsystem the repo does not have (clipboard clears only on manual `^X`). Gate strictly on `(m.modal != nil && m.modal.secret && m.modal.remaining > 0) || m.busy != nil`. `tea.Tick` is a one-shot timer (`commands.go:154`) — re-issue only while live, return `nil` when idle ⇒ zero idle redraws, no goroutines. `secClockMsg` is a new msg type (not a `KeyPressMsg`, so it never dismisses the secret modal). Add a test that `secClockMsg` with no live state returns a nil `Cmd`.
>
> **C10 — descoped, and reframed.** The original "drop the secret reference on the zero tick" is *security theater* (secrets are immutable Go strings; clearing one reference zeroes nothing). And **standalone password reveal has no countdown today** (`reveal.go:87` gate is always false) — auto-dismiss there is a *new policy* needing CLI plumbing, deliberately deferred. **Ship the live countdown for TOTP only**, where `SecretRemaining` is real. **Recompute `remaining` from wall-clock (`ValidUntil − now`), not by blind decrement**, or the bar disagrees with real OTP validity. Auto-dismiss at zero is fine as *UX* (reduce dwell), not as a memory-safety claim.
>
> **C12 — keep immediate cancel.** The dead `pickerBusy.canceling` branch (picker.go:759) is unreachable; `TestPickerBusyCancelReturnsImmediatelyAndIgnoresLateResult` pins immediate teardown. Animate with a self-relooping ~100ms `tea.Tick` batched into `startAction`, guard the handler on `activeID` so an orphan timer can't mutate a finished state, compute elapsed in `busyLines` (View side) so model snapshots stay deterministic. Default glyphs to ASCII `|/-\`; switch to braille only when `LANG` looks like UTF-8 — **glyph support is not a color question.**
>
> **G5 — wire it correctly.** `PickOptions` has no `Env` field; resolve the `GlyphSet` in `cli.go` next to the theme and pass it through. ASCII-rune test: every field (incl. box corners, spinner frames) has no rune > `0x7e`. **Reconsider coupling ASCII to `NoColor`** — Unicode box runes are fine on a monochrome xterm; tie ASCII forcing to TERM/cp437 detection + the narrow breakpoint, not to color (else `TestPickerViewUsesWorkflowShell`, which asserts `╭ PASSAGE` under `NoColor`, breaks for the wrong reason). Prefer a new `TruncateWith(value,width,marker)` over threading a glyph param through every call site.

### Phase 3 — Security Legibility (make the vault *feel* like a vault)

| id | What | Why | Files | Effort | Risk |
|----|------|-----|-------|--------|------|
| **C13** | Focus-aware secret auto-hide + scrub-on-exit | Alt-tab leaves a password on screen; `--no-alt-screen` leaks the last frame to scrollback | `internal/ui/picker.go`, `reveal.go` | S | med |
| **C15** | Inline confirm strip for destructive clears (pins/recents) | `^U`/`^E` wipe curated state on a single chord, no confirmation | `internal/ui/picker.go` | M | low |
| **C14** | Clipboard-armed status pill + auto-clear timer | Make the clearable-clipboard guarantee *visible*; turn dwell minimization into a UI promise | `internal/ui/picker.go`, `cli.go` | L | med |
| **C8** | Filled selection bar + pill/badge chrome | The N2 win; full-inner-width background fill (the cell buffer prevents cross-row bleed) | `internal/termstyle`, `picker.go`, `theme.go` | M | med |
| **C17** | Richer empty/error states with **Sanitized** echo | The new place untrusted text re-enters the render | `internal/ui/picker.go` | S | low |
| **G1** | Distinct empty-vault welcome vs zero-match | `len(entries)==0` (welcome) vs `len(filtered)==0` (search miss) | `internal/ui/picker.go` | S | low |

> **C13 — implementation contract (corrected).** Do **not** add a raw ANSI erase — the renderer's inline `close()` emits `EraseScreenBelow` *after* moving to the bottom (cursed_renderer.go:170), which clears nothing, which is *why* the secret persists. Instead: set a `redacted` flag before returning `tea.Quit` and render a redacted body in `View()` — the graceful-shutdown final render (`tea.go:1167 p.render(model)`) then overwrites the inline frame. For alt-tab: `view.ReportFocus = true` + a `case tea.BlurMsg:` arm that blanks the secret region (`•••• hidden — focus to show`). **Accept and document the killed-path gap:** SIGTERM/kill skips the final render; C10's countdown is the primary guarantee, ReportFocus + scrub are defense-in-depth.
>
> **C14 — gated on C9, with honest copy.** Add `ClipArmed`/`ClipRemaining` to `ActionOutcome`, set on copy/totp success (plumb seconds + tool name, **never the secret**). **Do not reuse `startAction(ActionClearClipboard)`** to fire the clear — it sets busy/clears modal/flashes a box; add a *quiet* `ClearClipboardFunc` that calls `clipboard.Clear` directly. **Make the pill honest:** "clears in ~42s while passage is open" — `tea.Tick` pauses on suspend and the clear only fires if the TUI reaches zero alive+foregrounded. A displayed-but-unfulfilled auto-clear is a false guarantee.
>
> **C8 — drop the bleed fear, fix the three real issues.** The v2 cell buffer already prevents cross-row background bleed (verified: `Draw` clears to nil cells, each cell carries its own resolved style). Spend the prototype budget on: (1) a `FillBar` that pads to inner width *first* then applies one fill SGR (today's `PadRight` adds raw spaces *after* `Apply`'s reset); (2) a **new `RoleSelectedBar` role** with a default background — do **not** overload `RoleSelected` (`39;4`), which `TestDefaultThemeUsesPaletteCodes` pins; (3) an explicit `NoColor` branch (caret `> ` + bold bar + ASCII `[MFA]`/`[PIN]`) since `colorprofile` never sees passage output once `NoColor` is set.
>
> **C17 — Sanitize the error *detail*, not just the query echo.** The reachable threat is entry paths from an unsanitized filesystem walk reaching the error line through `Truncate` (which is escape-aware but does **not** strip C1). Wrap `out.Err.Error()` in `Sanitize` before `wrapText`. The query-echo Sanitize is defense-in-depth (`safeTextInput` already strips controls before they reach `m.query`, so the test must set `m.query` directly). Clamp the multi-line panel to the `available` budget so it can't push the footer off a short screen.

### Phase 4 — Delight & Discoverability

| id | What | Why | Files | Effort | Risk |
|----|------|-----|-------|--------|------|
| **C16/G12** | Context-aware hint bar + task-grouped `?` overlay | Replace the 11-chord firehose; `?` gated on empty query | `internal/ui/picker.go` | M | med |
| **G4** | Unified time-decaying notice channel | One `m.message` slot fights C14's pill + C17's panel; collapse to one channel | `internal/ui/picker.go` | M | med |
| **G7** | Centralized `humanizeRelative` + `formatRemaining` | Three time renderings already coexist and will drift | `internal/ui/picker.go` | S | low |
| **G9** | Stable per-frame metadata column + stable ordinal | Timestamps float; column drifts further once C1 lands | `internal/ui/picker.go` | M | med |
| **G2** | First-run setup → interactive doctor instead of raw stderr | The most common first-run failure becomes guided onboarding | `internal/cli/cli.go` | M | med |
| **G11** | Real beam cursor in the filter field | The painted fake field becomes a real input | `internal/ui/picker.go` | S | med |
| **C18** | Wheel-scroll / click-to-select (alt-screen only) | Strictly progressive; keyboard stays sufficient | `internal/ui/picker.go` | M | low |

> **C16 — refuse the false security copy.** The original `?` overlay "Security" section documents **clipboard auto-clear** and **focus auto-hide** as *existing* guarantees. They do not exist in the repo. Document only truthful guarantees (reveal: any-key clears; clipboard: manual `^X`; the 12s gpg action timeout) — **or** land C13/C14 first and document them after. *Documentation of a security guarantee must follow, never precede, its implementation.* Gate `?` inside the default branch on `m.query == ""` (not a top-level `case "?":`, which steals the glyph for filtering paths containing `?`).
>
> **G7** — `reveal.go` needs only `formatRemaining` (no `now`); don't over-thread `now` into the reveal model. Make `now` injectable on `pickerModel` for deterministic goldens.
>
> **G11** — clamp cursor X to `min(VisibleWidth(query), fieldWidth)` (the field truncates), compute Y dynamically (`+2` when a message is shown), and **set Cursor to nil over any modal/busy/themeEditor** so it never blinks on a revealed secret. Verify the shell cursor shape resets after quit (renderer does `SetCursorStyle(0)` at close, cursed_renderer.go:198).

### Dropped / Infeasible

- **C19 (seed window size + color profile) — DROP.** Verified infeasible. `WithWindowSize` sets program fields, not the *model struct* fields `View()` reads, and on a real TTY it is overwritten by `term.GetSize()` (tea.go:1043). The flash is unchanged. **The real fix is orthogonal:** stop hardcoding `width:92, height:28` (picker.go:191) — render a blank/minimal frame until the first `WindowSizeMsg`. `WithColorProfile` is redundant with the default `colorprofile.Detect`.
- **G3 (cold-start scan spinner) — DEFER, and fix its premise.** `store.Discover` is a pure `filepath.WalkDir`; it never calls gpg. The cold-start delay is the FS walk + `state.Load`, **not** gpg-agent unlock. Ship a *static* "loading vault" first paint standalone (instant branded frame, no motion); add the threshold spinner only after C9/C12/G14.
- **G14 (cadence reconciliation) — lands LAST**, behind C9/C10/C12/G5. Drop the `asciiGlyphs` clause until G5 exists. Make the elapsed-seconds fallback the *unconditional* baseline in every mode; the spinner is decoration on top.
- **G15 (auto-run confirm frame) — ship after C15**, gated on a real TTY check (`x/term.IsTerminal`), `--yes` bypass for scripting. Existing single-match tests stay green precisely because their stdin is not a TTY.
- **G10, G13, G16, G8** — valuable polish, all correctly sequenced behind their dependencies; G16 must Sanitize centralized box titles (entry-derived) and carve out branding from the lowercase rule.

---

## 5. Before / After

### Before (today, all widths, single column)

```
╭ PASSAGE  dev ──────────────────────────────────────────────────╮
│ 14 entries · /home/you/.password-store                          │
│ /gh                                                  3/14        │
│                                                                 │
│ >  3  work/github/token            * mfa  06-22 15:04           │
│    7  personal/github                *    06-21 09:12           │
│   11  ops/gh-deploy-key              *    06-19 22:40           │
│       ... 11 more below                                         │
│                                                                 │
├──────────────────────────────────────────────────────────────────┤
│ type filters | arrows move | enter default | ^C/esc quit         │
│ ^Y copy ^R reveal ^T totp/secret ^P pin ^F mfa ^O theme ^X clip…  │
╰──────────────────────────────────────────────────────────────────╯
```
Substring filter (`gh` would *not* match `github`). Caret-only selection. Static busy box. Frozen `"%ds remaining"`. 11-chord footer dump.

### After (≥92 cols: two-region, fuzzy, motion-aware)

```
╭ PASSAGE  v0.4 ───────────────────────────────────────────────────────────────╮
│ 14 entries  ·  ~/.password-store  ·  ⬤ CLIP armed ~42s  ·  MFA-only            │
│ / gh▏                                                              3/14         │
│                                                                                │
│ ▌  3  work/⟦gh⟧ub/token       ★ ⟦MFA⟧ 2m │  work/github/token                  │
│    7  personal/⟦gh⟧ub             6-21    │                                    │
│   11  ops/⟦gh⟧-deploy-key     ★    4d     │  ★ pinned    ⟦MFA⟧ capable (totp)  │
│       ... 11 more below                   │  used  2 minutes ago               │
│                                           │                                    │
│                                           │  enter copy · ^R reveal · ^T totp  │
│       ╭ decrypting ─────────────────────╮ │  ^P pin                            │
│       │ ⠹  work/github   elapsed 2s/12s  │ │                                    │
│       │ esc cancel                       │ │                                    │
│       ╰──────────────────────────────────╯ │                                    │
├────────────────────────────────────────────────────────────────────────────────┤
│ type to fuzzy-filter · ↑↓ move · enter copy · ^T totp · ^F mfa-only ● · ? help │
╰────────────────────────────────────────────────────────────────────────────────╯

  ⟦gh⟧ = RoleSearch match-highlight (bold)   ▌ = RoleSelectedBar full-width fill
```

### TOTP reveal (live countdown, urgency ramp, auto-dismiss)

```
  ╭ TOTP  work/github ─────────────────────────────────────╮
  │ 482 915                                                 │
  │                                                         │
  │ 08s ▰▰▰▰▰▱▱▱▱▱   (green→amber<15s→red<5s; auto-clears)  │
  ╰ press any key to clear ────────────────────────────────╯
```

### After (NO_COLOR / 16-color / <92 cols — byte-near today, ASCII glyphs)

```
╭ PASSAGE  v0.4 ───────────────────────────────────────────────╮
│ 14 entries · ~/.password-store · * CLIP armed ~42s           │
│ /gh                                                  3/14     │
│ >  3  work/github/token            * mfa  06-22 15:04        │
│  | Clear all pins? removes 6 pins      y confirm   esc cancel │
│  - decrypting work/github   elapsed 3s/12s                   │
│    TOTP  08s [#####-----]  press any key to clear            │
├──────────────────────────────────────────────────────────────┤
│ type filter · arrows move · enter copy · ? all keys          │
╰──────────────────────────────────────────────────────────────╯
```
Same frame, no truecolor, ASCII spinner/bar, single column, the detail pane silently omitted. Keyboard fully sufficient.

---

## 6. Degradation & Security Guarantees

**Every layer is additive with a named fallback.** The contract:

| Concern | Guarantee | Mechanism |
|---------|-----------|-----------|
| **NO_COLOR / 16-color** | `Apply` is a no-op when codes empty ⇒ no escapes emitted; highlight→bold, fill→caret, pills→`[MFA]`, spinner/bar→ASCII | passage's own `NoColor` gate stays authoritative (the runtime profile never sees colored output once `NoColor` is set); `TerminalTheme` is the guaranteed floor |
| **Narrow (<48 → clamp)** | Detail pane omitted <92 cols; single column is byte-near today; rows truncate with escape-aware `~` | 48-col clamp (shell.go:17,42) untouched; `layoutSpec` (C6) carries the breakpoint |
| **`--no-alt-screen`** | Last secret frame scrubbed via a redacted final `View()` render (not a raw erase) | C13; killed-path (SIGTERM) gap documented, C10 countdown is the primary guarantee |
| **CJK/emoji width** | Box + columns measured with `ansi.StringWidth` = the renderer's own measure | C1; locale-independent, byte-identical to the painter |

**Secrets, strengthened not weakened (N9):**

1. **Match/highlight read only Sanitized non-secret `Path`/`Display`** — never secret bytes (C3 returns indices into the candidate, C4 ranks metadata only).
2. **Every new place untrusted text re-enters the render is wrapped in `Sanitize`** — the detail-pane full path (C7), the error panel detail (C17), centralized box titles (G16). This *activates* the dead `Sanitize` primitive on real paths.
3. **Reveal dwell is shortened** (TOTP auto-dismiss, C10) and **alt-tab blanks the secret** (`ReportFocus` + `BlurMsg`, C13).
4. **The clearable clipboard backend stays the path** (never `tea.SetClipboard`, which has no clear/expiry); C14 makes the clear *visible* with honest copy.
5. **Motion is gated** — a single `tea.Tick` exists only while a secret countdown or busy action is live, re-issued only while needed, stopped the instant it clears. Idle startup stays cold; no goroutines, no permanent ticker, no idle redraws.
6. **Destructive clears require confirmation** (C15) — `^U`/`^E` no longer wipe curated state on a single chord.

---

## 7. Risks, Tradeoffs & What We Deliberately Won't Do

**Risks we accept:**
- **C4 (high risk):** the `[]int → struct` filtered refactor touches 8 call sites and two pinned tests. Mitigated by mechanical `.idx` deref and `SliceStable` determinism. The *real* hazard is auto-run semantics drift — mitigated by the shared ranking fn.
- **C5/C8 (medium):** the highlight × truncation × fill × SGR-reset interaction is off-by-one prone. Mitigated by width-48 tests asserting `Strip(line) == plain` and no stray escapes.
- **C1 locale landmine:** mitigated entirely by using `ansi.StringWidth`, not package-level `runewidth`.

**Tradeoffs:**
- Elevating the hand-rolled stack means we *own* the color/width/match code forever. That is the price of a 244-line auditable security surface. We judge it worth paying for a password manager.
- The detail pane (C7) reads only existing `Entry` fields — no gpg-id/recipients plumbing. Lower cost, lower feel, but it keeps opening the picker cheap (no decrypt).

**What we deliberately will NOT do** (highest-cost / lowest-felt, dropped from scope):
- **No lipgloss/bubbles adoption** — defended in §3.
- **No in-house OKLCH/WCAG color-math engine** — `TerminalTheme` + `VividTheme` + the runtime's `colorprofile` downsampling already cover the floor and the ceiling.
- **No lipgloss `Canvas`/`Layer` floating-modal compositor** — in-shell panels read fine; the compositor's complexity buys little here.
- **No `:` command palette** — the `?` overlay + contextual hints deliver discoverability without a new modal mode.
- **No mouse as a headline** — wheel/click ships last, alt-screen-only, strictly progressive (C18). Keyboard is always sufficient.
- **No "drop the secret reference" as a security claim** — secrets are immutable Go strings; that is cosmetic, and we will not document it as memory safety.
- **No documentation of guarantees before they exist** — C16's "clipboard auto-clears" / "focus auto-hide" copy is forbidden until C14/C13 ship.

---

## 8. Recommended First PR

**Title:** *Foundation: cell-accurate width, rune-safe secret wrap, and a unified layout spec.*

**Scope: C1 + C2 + C6.** This is the smallest slice that (a) is pure correctness with no behavioral surprise, (b) unblocks *every* downstream pillar, and (c) fixes two real bugs — one of them on the secret-display path.

**Concretely:**

1. **C2 first (S, low risk, self-contained):** rewrite `wrapSecret` (reveal.go) to advance rune-by-rune via `for i, r := range value`, cutting on rune boundaries. Add `internal/ui/reveal_test.go` with a multibyte secret at narrow width asserting `utf8.ValidString` per line and `VisibleWidth ≤ width`. **Zero new deps, ASCII byte-identical.** Ship-anytime safety fix.

2. **C1 (M, medium):** route `VisibleWidth`/`PadRight`/`Truncate` through `ansi.StringWidth` (promote `github.com/charmbracelet/x/ansi` from indirect to direct in go.mod — *no new download*, it is already in go.sum). Keep the escape-skip loop unchanged; budget in cells; never split a 2-cell rune. Deliberately update the `TestTruncate` multibyte golden and the Strip/VisibleWidth width assertions. **Add the regression test that box-glyph width stays 1 with `RUNEWIDTH_EASTASIAN` unset** — this is the guard against the locale landmine that makes the whole "elevate" thesis safe.

3. **C6 (M, medium):** introduce `layoutSpec` computed once per frame; replace `availableListLines` (:324), `pageSize` (:525), `pickerShellStructuralLines` (:349) with reads off it. Carry forward all existing floors and the 48-col clamp. **Add the missing `pageSize` + `ensureVisible` scroll test** (there is none today). Scope the "identical to today" guarantee to the no-message steady state.

**Why this PR:** it is all green-keeping or deliberate-golden-update; it ships a real secret-path bugfix the moment it lands; and it lays the exact substrate — provably-painter-matched width, one source of truth for layout geometry — that C3/C4/C5 (fuzzy + highlight) and C7 (detail pane) stand on. Nothing visible changes for the ASCII-only user, which is exactly the right first step for a security-reviewed vault: earn trust with correctness before spending it on motion.

*Verification command for the PR:* `RUNEWIDTH_EASTASIAN= go test ./internal/termstyle/... ./internal/ui/... && LANG=ja_JP.UTF-8 go test ./internal/termstyle/...` — proving width math is locale-independent and the box stays intact.