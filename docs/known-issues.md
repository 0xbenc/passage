# Known Issues — Resolved

*An execution brief for a later agent. Every item below was found by a full
end-to-end read of the repo (all 65 files) and each claim was verified against
the cited line. Items are ordered by severity, not by file. Where an item
touches an interactive surface, `docs/flow-contract.md` (FROZEN v1) wins over
any convenience argument.*

> **Status: resolved (all ten items + bonus), 2026-09-14.** Implemented in one
> working-tree pass (no intermediate commits; this record is the progress
> report). `gofmt -l . && go vet ./... && go test ./... && go test -race
> ./...` green, CI conformance greps green. The original brief is preserved
> below as the record of what was found.
>
> **Reviewed 2026-09-15.** One regression the pass introduced was found and
> fixed, and the item-8 `ui.Text` judgment call was decided (deleted) — see
> the review addendum below. All gates re-run green after both.

## Resolution report (2026-09-14)

Per-item outcomes, with the decisions made where the brief left judgment:

1. **Filter backspace (high).** `picker.go` backspace now removes one full
   rune via `utf8.DecodeLastRuneInString`, with the documented `RuneError, 1`
   degradation for a trailing invalid byte. Pinned by `TestPickerBackspaceRemovesWholeRune` (seed `café`,
   backspace → `caf`, re-filter runs, drain to `""` no-ops). The composer's
   `textField` and the theme editor's raw-edit buffer already use `[]rune`, so
   the filter was the only byte-level site.

2. **Write-timeout message (medium).** `runInteractiveAction` now threads its
   chosen `timeout` into `interactiveActionError(ctx, req, timeout, err)`,
   which names that value in the `timed out after %s` text (the `pass show --
   <path>` hint branch is untouched). A 60s write timeout reports `1m0s`; a
   12s read timeout reports `12s`. Pinned by
   `TestInteractiveActionErrorNamesActualTimeout` (write verb → 60s, read verb
   → 12s, global → the threaded value).

3. **`--import-dir` planning (medium).** `planImportedKey` now mirrors
   `planToken`'s classification exactly: secret-hold → encryptable (`owned-skip`)
   → unusable → would-own-trust; present-unowned → encryptable
   (`already-valid`) → unusable → would-lsign; absent → would-lsign +
   import. An expired/revoked foreign key plans `unusable` (not a
   would-lsign that gpg refuses), and an owned-but-unusable key plans
   `unusable` (not `owned-skip`). The fake-gpg harness in `gpgtrust_test.go`
   gained `GPG_FAKE_VALIDITY` (ported from `gpgdiag_test.go`),
   `GPG_FAKE_IMPORT_KEYS` (show-only `--import` peek), and `GPG_FAKE_LSIGN_LOG`
   (asserts no `--quick-lsign-key` was attempted). Pinned by
   `TestPlanImportDirUnusableKeys`. Note: the harness's `pub` line now keeps
   the full 14-field colon shape with the validity char in field 2 — the
   parsers skip lines with fewer than ten fields, so the old 3-field form
   made `ResolvePrimary` see nothing.

4. **Theme editor dropped roles (medium).** The editor model keeps a
   `dropped []string`; `config()` (now a pointer receiver) records each
   unparseable role as `label (parse error)`. `ThemeEditorResult` carries
   `DroppedRoles`, and `saveThemeConfig` appends `Dropped invalid roles: …`
   to the save message — so both the `passage theme` CLI notice and the
   picker's Ctrl-O notice name the loss. The save still succeeds for the rest
   (no new abort path). Pinned by
   `TestThemeEditorSaveReportsDroppedRoles` (ui) and
   `TestSaveThemeConfigMessageNamesDroppedRoles` (cli: message names the role,
   written file keeps valid roles and omits the bad spec).

5. **Letter-free quit (medium).** Removed `Q` from the theme editor's browse
   mode and `q`/`Q` from the text viewer. One correction to the brief: the
   text viewer did *not* already have the full contract set — it was missing
   `ctrl+q` — so it was added alongside the removal (the FROZEN contract
   requires `esc`/`ctrl+c`/`ctrl+q` on every screen; the theme editor was
   covered by the `EditTheme`/picker wrapper's ctrl-chord handling as stated).
   The text viewer's footer hint changed `enter/q` → `enter/esc` to match.
   Pinned by `TestThemeEditorQuitIsLetterFree` (`q`/`Q` inert: no done, no
   canceled, no status; `esc` cancels). Grep confirms no `q`/`Q` quit arms
   remain in `internal/ui` (the two remaining test references assert the new
   behavior). No README/man update needed: neither file ever documented
   q/Q quit (the man page's key list already says `Esc / Ctrl-C / Ctrl-Q`).

6. **Busy box detail (low).** `startAction` passes an empty busy detail for
   every action where `actionNeedsEntry` is false; per-entry verbs keep the
   path (the composer path already named the new entry's path and is
   unchanged). Pinned by `TestBusyDetailOnlyForEntryActions` (doctor → empty,
   copy → path).

7. **Not-found exit codes (low).** Kept `2` (the documented contract and the
   majority): `runCopy`, `runReveal`, and `runTOTP` now do the same
   `findEntry` pre-check `runShow`/`runPin`/`runRm` do and exit `2` with
   `passage: entry "…" not found`. The helpers' not-found errors are retained
   for the interactive path (where the entry always exists, so they're
   belt-and-braces). Since the decision matches the documented contract,
   `docs/non-interactive.md` needed no change (its exit-code table already
   says `2` = entry not found). Pinned by `TestRunMissingEntryExitsTwo`
   (copy/reveal/totp → 2); verified end-to-end with a built binary
   (copy/reveal/totp/show missing → 2).

8. **Dead code (low).** Deleted `totpEntry`, `showDoctorUI`, `showKeysUI`
   (zero callers, confirmed by grep before deleting). Deleted
   `pickerBusy.canceling` + its branch: esc in the busy state removes the box
   and shows "Canceled." immediately, so the SIGKILL-then-`WaitDelay`(2s)
   window is *not* user-visible — the status line was unreachable, per the
   brief's delete-if-in-doubt rule. Deleted `clipState.tool` and, with it,
   `ActionOutcome.ClipTool` (its only reader was the deleted field) plus the
   two `out.ClipTool = …` assignments in `runInteractiveActionOnce`; the
   `ClipArmed` doc now says the pill shows a countdown only. Dropped
   `parseCommon`'s always-nil `error` return (mechanical: 19 call sites; the
   one `=` → `:=` fix in `runGenerate`). **Flag for the maintainer:** with
   `showDoctorUI`/`showKeysUI` gone, `ui.Text`/`TextOptions`/`textModel`
   (`internal/ui/text.go`, plus the caller-less `LinesFromText`) now have no
   in-module callers. The brief's out-of-scope note describes that viewer as
   deliberate (plain-`Truncate` pass-through for command output) and item 5
   fixed it in place, so it was kept rather than deleted — a judgment call to
   confirm (delete the file, or keep it as a primitive). *Resolved in review
   (2026-09-15): deleted.*

9. **Stale comments (low).** All four rewritten to state the true fact:
   `passstore.go` (no `InsertOptions` exists → names only `GenerateOptions /
   RemoveOptions`), `gpgtrust_test.go` (now names the trustdb-file scale —
   4=marginal, 5=full, 6=ultimate — and warns against "fixing" code to match
   a comment), `composer_test.go` (fixture lives in `fixtures_test.go`, not
   the deleted `pathindex_test`), `shell.go` (truncation happens in
   termchrome's line renderer, not a nonexistent `workflowLine`).

10. **Shell completion drift (low).** `remove` added to the top-level command
    list in all three files (bash `commands`, zsh `commands` array, fish
    subcommand list); the bash `rm|remove` and zsh `rm|remove` case arms
    already existed and now also cover the alias from completion. Bash `list`
    gained `--no-alt-screen`; bash `copy` gained `--no-color --theme-file
    --no-alt-screen` (the same common set as `reveal`), per the brief. Fish
    kept its flat flag model (the brief permits this) with `remove` added.
    `TestCompletionsMentionCommands` passes; `bash -n`/`zsh -n`/`fish -n`
    clean; bash function smoke-tested (top level offers `remove`; `rm`/`remove`
    complete identically; `list`/`copy` offer the aligned sets). **Flag for
    the maintainer:** the remaining bash arms (`show`, `pin|unpin`,
    `clear-recents|clear-pins`, `access`, `trust`, `insert`, `generate`,
    `edit`, `rm|remove`) are still narrower than what `parseCommon` accepts
    (e.g. no `--no-color/--theme-file/--no-alt-screen`, which are parsed and
    harmlessly ignored on those verbs). The brief scoped the flag alignment to
    `list`+`copy`, so the rest were left as-is.

**Bonus.** `.mise.toml` bumped to `go = "1.26.5"` with the comment updated to
match go.mod.

**Also noted while working:** the `gpgdiag_test.go` fake gpg emits a 3-field
`pub` line, which the ≥10-field colon parsers (`ResolvePrimary`, `LocalKeys`)
silently skip — latent there (its tests never exercise those paths) but worth
aligning with the 14-field form used by the `gpgtrust_test.go` fake.

## Review addendum (2026-09-15)

An independent review of the working tree confirmed all ten items + bonus
were implemented as specified, with two follow-ups:

- **Regression found and fixed.** Item 8's removal of `parseCommon`'s error
  return took the outer `err` out of scope in `runGenerate`, and the
  compensating `=` → `:=` turned `length, err := strconv.Atoi(rest[1])` into
  a block-scoped shadow of `length` — `passage generate ENTRY LENGTH`
  validated LENGTH, then silently discarded it (pass received
  `generate --force -- ENTRY` and used its default length). Proven by
  recording the fake pass's argv; the existing test only asserted exit code
  and message. Fixed (`n, err := …; length = n`), and
  `TestRunGenerateWritable` now asserts `generate --force -- work/new 16`
  reaches pass's argv so the regression cannot return silently.
- **`internal/ui/text.go` deleted** (maintainer decision on the item-8 flag):
  `ui.Text`/`TextOptions`/`textModel` had no callers left after the item-8
  deletions, so the file went away — superseding item 5's fix to the text
  viewer's quit keys. The theme-editor half of item 5 stands as written.

Gates after both changes: `gofmt -l .`, `go vet ./...`, `go test ./...`,
`go test -race ./...`, and the four CI conformance greps — all green.

---

## Ground rules

- After each item (or at the end): `gofmt -l . && go vet ./... && go test ./... && go test -race ./...` must stay green.
- CI conformance greps (`.github/workflows/ci.yml`) must stay green: no hand-built `  /  ` footer separators, no inline spinner frame runes, no golden-update flag, no `replace` in `go.mod`.
- No new dependencies. No `replace` directives.
- TUI chrome rules from `CONTRIBUTING.md`: footers through `termstyle.Footer`, glyphs through `termstyle.ResolveGlyphs`, no golden-update flag, keep passage/ssherpa termtheme/termnav/termchrome pins in lockstep.
- Where a fix changes user-visible text that a test pins, update the test in the same commit and say why in the commit message.
- Do not "fix" behavior that a test deliberately pins (e.g. the C6 layout invariants, the border-integrity goldens) unless the item says so.

---

## 1. Filter backspace corrupts multi-byte UTF-8 — **high**

- **Where:** `internal/ui/picker.go:444-449`
- **What:** `case "backspace":` does `m.query = m.query[:len(m.query)-1]` — it removes one *byte*, not one rune. The filter accepts any safe printable text, including non-ASCII (e.g. an entry named `café`): typing then backspacing removes half the rune, leaving an invalid byte sequence in the query (matching and cursor math then operate on corrupted input; rendering survives only because `Sanitize`/`Truncate` tolerate it).
- **Fix:** Remove one full rune:

  ```go
  case "backspace":
      if m.query != "" {
          if _, size := utf8.DecodeLastRuneInString(m.query); size > 0 {
              m.query = m.query[:len(m.query)-size]
          }
          m.applyFilter()
      }
  ```

  (`utf8.DecodeLastRuneInString` returns `RuneError, 1` on a trailing invalid byte; deleting that one byte is the correct degradation.)
- **Verify:** Add a `picker_test.go` case: seed `query = "café"`, feed `backspace`, assert `query == "caf"` and that `applyFilter` ran without panic; feed `backspace` again to `""` and assert no-op.

## 2. Write-action timeout message reports 12s instead of 60s — **medium**

- **Where:** `internal/cli/cli.go:2116-2122` (`interactiveActionError`), with the real deadline chosen at `:1934-1940` (`writeActionTimeout` const at `:166`).
- **What:** `runInteractiveAction` gives store mutations (create/generate/remove) a 60s deadline, but `interactiveActionError` hardcodes `interactiveActionTimeout` (12s) in the `timed out after %s` text. A 60s write timeout therefore tells the user "timed out after 12s".
- **Fix:** Thread the actual deadline through. E.g. `runInteractiveAction` passes its chosen `timeout` into `interactiveActionError(ctx, req, timeout)` and the message uses that value. Keep the `pass show -- <path>` hint branch as-is.
- **Verify:** Existing tests around `interactiveActionError` (search `timed out` in `cli_test.go`) still pass; add/adjust one assertion that a write-verb timeout names 60s.

## 3. `--import-dir` plans `would-lsign` for expired/revoked keys — **medium**

- **Where:** `internal/gpgtrust/gpgtrust.go:309-328` (`planImportedKey`), contrast `planToken` at `:154-186`.
- **What:** The recipient-scope path classifies a present-but-unencryptable key with `d.Unusable` and marks it `unusable` (unfixable). The import-dir path has no such check: a present, unowned, expired/revoked key is planned `would-lsign` — a "fix" that cannot work — and an owned+unusable key comes out `owned-skip` instead of `unusable`. The trust preview and apply report then promise a fix gpg will refuse.
- **Fix:** Mirror `planToken`'s classification in `planImportedKey`: for a present, unowned key check `d.Unusable` before defaulting to `would-lsign`; for a key whose secret is held, check `Unusable` before `owned-skip` (order: secret → encryptable → unusable → would-lsign/would-own-trust).
- **Verify:** Extend the fake-gpg harness in `gpgtrust_test.go` (it already supports `GPG_FAKE_VALIDITY`) with an `Apply`/plan case where the import-dir contains an expired foreign key: assert the plan action is `unusable`, the plan is not actionable for that recipient, and the apply report does not claim it was signed.

## 4. Theme editor silently drops unparseable role specs on save — **medium**

- **Where:** `internal/ui/theme_editor.go:510-521` (`config()`); live behavior in `currentTheme()` just below.
- **What:** `config()` skips any role whose spec fails `ParseStyleSpec` (bare `continue`, no warning). A `theme.conf` that the user saved with an older/newer binary (or hand-edited) is loaded into the editor, and pressing `s` rewrites the file with those overrides deleted — quiet data loss on the user's config. `saveThemeConfig` in `cli.go` takes a backup, which is the only recovery.
- **Fix:** Collect the skipped roles and surface them. Minimum: have the editor model keep a `droppedRoles []string` (or extend the existing `warning`/`message` state) and include it in `ThemeSaveResult.Message` / the picker notice, e.g. `saved; dropped invalid roles: primary (unknown style token "…")`. Do not invent a new error path that aborts the save — warn, keep the rest.
- **Verify:** `theme_editor_test.go`: seed a `ThemeConfig` with one invalid spec, run the save path, assert the result message names the dropped role and the saved `Config.Specs` omits it while everything else survives.

## 5. Theme editor quits on `Q`, violating the frozen interaction contract — **medium**

- **Where:** `internal/ui/theme_editor.go:184` (`case "esc", "Q"`); same-rule violation in `internal/ui/text.go:73` (`case "ctrl+c", "esc", "q", "Q"`).
- **What:** `docs/flow-contract.md` §1 (FROZEN v1) is explicit: *"Quit is letter-free on every screen: `esc`, `ctrl+c`, `ctrl+q`. No letter (no `q`/`Q`) quits or goes back."* Both screens bind a letter to quit.
- **Fix:** Remove the letter bindings. Both screens already have the full contract set: `esc`, and `ctrl+c`/`ctrl+q` (the `EditTheme` program wrapper handles the ctrl chords even mid raw-edit). No replacement binding needed.
- **Verify:** Grep for `"Q"` and `"q"` quit arms in `internal/ui/`; add a key-handling test that `q` in the theme editor's browse mode does *nothing* (no `done`/`canceled`) while `esc` cancels.

## 6. Busy box shows the hovered entry's path for global actions — **low**

- **Where:** `internal/ui/picker.go:1342` (`startAction` passes `entry.Path` as the busy detail for *every* action).
- **What:** Global actions (doctor, keys, clear pins/recents, clear clipboard) render e.g. `running doctor work/github` / `clearing pins work/github` with whichever entry happened to be hovered — the path is irrelevant and misleading.
- **Fix:** Pass an empty detail for actions where `actionNeedsEntry(action)` is false (or drop the path when `entry` is empty). Per-entry verbs keep the path.
- **Verify:** `picker_test.go`: trigger `ActionDoctor` with a selection, assert the busy box detail is empty; trigger `ActionCopy`, assert the path is present.

## 7. Not-found exit codes are inconsistent — **low**

- **Where:** exit `2` at `internal/cli/cli.go:1088` (`show`), `:1223` (`pin`/`unpin`), `:1636` (`rm`); exit `1` for the same condition in `copy`/`reveal`/`totp` via `copyEntry`/`generateTOTP` errors at `:2185`, `:2210`, `:2300`.
- **What:** `docs/non-interactive.md` documents exit `2` as "Entry not found, doctor warnings, or a non-writable access scope", but `copy`/`reveal`/`totp` return `1` when the entry doesn't exist.
- **Fix:** Pick one. Recommended: keep `2` for not-found (it's the documented contract and matches the majority) — have the `copy`/`reveal`/`totp` handlers detect the not-found error (a sentinel or a `findEntry` pre-check like the others) and return `2`. Update `docs/non-interactive.md` only if the decision differs.
- **Verify:** Add CLI tests: `copy missing/entry` → exit 2 (and same for `reveal`, `totp`); existing not-found tests for `show`/`pin`/`rm` stay green.

## 8. Dead code — **low**

- **Where / what:**
  - `internal/cli/cli.go:2275` `totpEntry`, `:2354` `showDoctorUI`, `:2366` `showKeysUI` — superseded by `ActionOutcome{TextTitle, TextLines}` in `runInteractiveActionOnce`; zero callers.
  - `internal/ui/picker.go:235` `pickerBusy.canceling` — declared and read at `:1710` (renders `Cancel requested. Waiting for the command to stop.`) but never set anywhere; the status line is unreachable.
  - `internal/ui/picker.go:1603` `clipState.tool` — populated from `out.ClipTool` but never read; the pill renders only `clip clears in ~%ds`. The `ClipArmed` doc comment at `:125-131` claims the pill is "named by ClipTool" — also stale.
  - `internal/cli/cli.go:2393-2408` `parseCommon` always returns `nil` error, so every `if err != nil` after a `parseCommon` call (~15 sites) is unreachable.
- **Fix:** Delete the three CLI functions. For `canceling`: either wire it (set `canceling=true` on esc in the busy state, clear on `actionDoneMsg`) so the "waiting for the command to stop" line is real, or delete the field + branch — decide based on whether a SIGKILL-then-`WaitDelay`(2s) window is user-visible; if in doubt, delete. For `tool`: delete the field and fix the `ClipArmed` doc to say the pill shows a countdown only. For `parseCommon`: drop the `error` return (mechanical) or keep the signature and delete the dead checks — prefer dropping the error.
- **Verify:** Build + full test suite; `go vet` clean; no test references the removed symbols (search before deleting).

## 9. Stale comments — **low**

- **Where / what:**
  - `internal/passstore/passstore.go:388` — "InsertOptions / GenerateOptions / RemoveOptions tune the write verbs." No `InsertOptions` type exists.
  - `internal/gpgtrust/gpgtrust_test.go:250-252` — doc says `--full` sets "ownertrust=4 on a fresh key … existing ultimate (5)"; the test body asserts fresh→`5` and ultimate=`6` (the trustdb-file scale: 6=ultimate, 5=full). The comment contradicts its own assertions.
  - `internal/ui/composer_test.go:235` — "uses fixturePaths from pathindex_test"; that file was deleted, the fixture lives in `internal/ui/fixtures_test.go`.
  - `internal/ui/shell.go:95` — `joinColumns` doc references `workflowLine`, which no longer exists (truncation now happens in `termchrome.Line`).
- **Fix:** Rewrite each to state the true fact (for the gpgtrust one, name the trustdb-file scale explicitly — 6=ultimate, 5=full — to prevent the next reader from "fixing" the code to match the comment).
- **Verify:** None beyond review; no behavior changes.

## 10. Shell completion drift — **low**

- **Where:** `completions/passage.{bash,zsh,fish}`; drift guard `internal/cli/completions_drift_test.go`.
- **What:**
  - The `remove` alias works (dispatch in `cli.go`) and is handled in the bash/zsh `rm|remove` case arms (`passage.bash:73`, `passage.zsh:70`) but appears in no top-level command list in any of the three files, and fish has no alias handling at all.
  - bash `list` arm (`passage.bash:16`) omits `--no-alt-screen`; bash `copy` arm (`passage.bash:26`) is narrower than zsh's common array (missing `--no-color --theme-file --no-alt-screen`).
  - fish is fully flat: every flag completes for every subcommand (e.g. `--recursive` on `list`).
- **Fix:** Add `remove` to the top-level command lists in all three (the drift test requires command *names* to appear textually — an alias in the list satisfies it). Align the bash per-command flag lists with what `parseCommon` + each handler actually accept (cheapest: give `list` and `copy` the same common set as `reveal`). Fish: leave the flat model as-is if preferred, but at minimum add `remove`.
- **Verify:** `go test ./internal/cli/ -run TestCompletions` (the drift guard); manually smoke `complete -p`-style expansion if possible.

---

## Bonus: one-line staleness (found in the same pass)

- `.mise.toml:3` pins `go = "1.26.3"` and its comment claims it matches go.mod; go.mod is `go 1.26.5`. Bump the pin and the comment.

## Out of scope (noted, deliberately not in the list)

These were observed during the read but are judgment calls, not clear defects —
flag them to the maintainer before touching:

- `gpgdiag.Access(ctx, "")` resolves the *parent* of the store root (no empty-entry guard).
- Owned-but-expired keys yield an empty `Fixable` hint (foreign expired keys say `unfixable`) — asymmetric.
- `runGenerate`/TUI `ActionGenerate` swallow clipboard failures silently while `copy`/`reveal` surface them as success messages.
- `LocalKeys` omits `--batch` (every other gpg listing call passes it).
- `keyLines` (TUI) and `runKeys` (CLI) disagree on column order/separator.
- `text.go`'s output viewer passes lines through plain `Truncate` (ANSI pass-through) while chrome paths Sanitize — deliberate for command output, but the divergence is undocumented.

## Definition of done

All ten items fixed (or each explicitly declined with a note appended here),
the bonus one-liner done, `gofmt -l . && go vet ./... && go test ./... && go test -race ./...` green, the CI conformance greps green, and `docs/non-interactive.md` / `README.md` / `man/passage.1` updated for any user-visible change (exit codes in item 7, theme-editor keys in item 5). Then delete this file or retitle it as a resolved record, matching how `docs/access-trust-roadmap.md` carries its "Status: implemented" header.
