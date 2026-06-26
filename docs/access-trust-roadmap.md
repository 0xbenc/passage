# passage: read-only awareness, key trust, and store writes

*A decision-ready roadmap for the maintainer. The load-bearing GPG claims below were verified by running real `gpg` (GnuPG 2.2.27) in a throwaway `GNUPGHOME`, then independently re-run by a second adversarial pass. The Bubble Tea mechanics were checked against the real source under `internal/` and `charm.land/bubbletea/v2@v2.0.7`. Where the source or the experiment contradicts a popular assumption, it wins.*

---

## 1. What we are building

Four capabilities, one coherent story — *"can I write here, and if not, fix it":*

1. **Read-only awareness.** For any entry/subfolder, tell the user whether they can create/edit passwords there or are effectively **read-only** (can decrypt, cannot encrypt to all recipients) or have **no access** (cannot even decrypt).
2. **Key trust (gpgobble, Go-native).** Make a read-only folder writable by **local-signing** its `.gpg-id` recipients — and, separately, import + trust a whole **folder of public-key files** (the original `gpgobble` use case). Default strength is **lsign only**; `--full` (ownertrust) is opt-in.
3. **Store writes.** Give passage real `insert` / `generate` / `edit` / `rm`, gated on the writable verdict so they refuse early instead of letting `gpg` hard-fail mid-encrypt.
4. **Full TUI.** Surface the verdict as a badge + detail block, add in-picker create/edit/generate, and an in-TUI "make writable / trust keys" flow.

### Today, in one paragraph

passage is **retrieval-only**. The only store path is `pass show` (`internal/passstore/passstore.go:264-292`); pins/recents are the only writes, to a local JSON file. `internal/gpgdiag` is the only direct `gpg` caller and is read-only: it reads each directory's literal `.gpg-id` and classifies recipients, consumed by `doctor` and `keys`. There is no `insert`/`edit`/`generate`/`rm`, no `.gpg-id` inheritance resolution, and — see §2 — its notion of "trusted" is **incorrect**.

---

## 2. The correctness pillar: what actually gates encryption

Everything here rests on one question: *when will `pass insert/edit/generate` (i.e. `gpg --encrypt -r <recipients>`) succeed without prompting?* The current code answers with **ownertrust level 4/5** (`gpgdiag.go:237`). That answer is wrong. Measured matrix (own key holds the secret; "recipient" is a public-key-only import):

| recipient state | `--with-colons` validity (field 2) | non-interactive `gpg -e -r` |
|---|---|---|
| public key imported, nothing else | `-` (unknown) | **FAILS** — exit 2, `Unusable public key` |
| **after `gpg --quick-lsign-key` only** | `f` (full) | **SUCCEEDS** ✅ — and writes **no** ownertrust record |
| ownertrust = 4 (full), no lsign | `-` | **FAILS** — exit 2 |
| ownertrust = 5 (ultimate), no lsign | `-` | **FAILS** — exit 2 |
| `--trust-model always` over an invalid key | `-` | succeeds (but masks the real gate — never use for the verdict) |

**Consequences for `gpgdiag.recipientStatus` (gpgdiag.go:225-243):** the ownertrust rule produces *false read-only* (an lsigned, perfectly encryptable key is reported "untrusted") **and** *false writable* (an ownertrust-4/5 but uncertified key is reported "trusted" though encryption fails). The user's choice of **lsign-only** is therefore not just acceptable — it is exactly sufficient: a local signature confers validity `f`, which is the whole requirement.

### Why we don't just "read validity `f`/`u`"

A second adversarial pass refuted the obvious shortcut:

- **lsign does not override expiry/revocation.** An expired key stays `e`, a revoked key stays `r`, and encryption fails — even after lsign.
- **Pub-line validity is the *max across UIDs*.** A multi-UID key can show `f` at the `pub` line yet fail when the `.gpg-id` names the email of an *unsigned* UID.
- The same shape applies to an expired encryption *subkey* under a valid identity.

### The authoritative gate: probe-encrypt

The ground truth of "will `pass` succeed" is to **ask `gpg` to do exactly what `pass` will do**, with no secret and no pinentry:

```
# whole-scope writable check — one probe to ALL recipients of the governing .gpg-id
printf '' | gpg --batch --no-tty --yes -e -r <id1> -r <id2> ... -o /dev/null   # rc==0  ⇒  WRITABLE
```

This is cheap (public-key only — no decrypt, no agent), correct for owned / lsigned / expired / revoked / multi-UID / selector-form (fpr | email | short-id) cases alike, and matches `pass` byte-for-byte. The `--with-colons` validity read is kept **only** to *explain* a failure (drive messaging and the "make writable" affordance), never as the gate.

### The verdict model

Per governing `.gpg-id` **scope** (the nearest `.gpg-id` found walking an entry's directory up to the store root):

| verdict | definition |
|---|---|
| **WRITABLE** | all-recipients probe-encrypt returns rc 0 |
| **READ_ONLY** | probe fails, but you own ≥1 recipient's secret key (can still decrypt) |
| **NO_ACCESS** | probe fails and you own no recipient (cannot decrypt or write) |
| **UNINITIALIZED** | no `.gpg-id` found walking up to the store root |

When not writable, each blocking recipient is classified for *fixability* (a per-recipient probe + `--list-keys`/`--list-secret-keys`):

- **MISSING** — not in the keyring → needs import-then-lsign.
- **INVALID/UNKNOWN** — present, validity `-`/`q`/`n`/`m` or an unsigned UID → **lsign-fixable**.
- **UNUSABLE** — validity `e`/`r`/`i`/`d` (expired/revoked/disabled) → lsign cannot help; show a distinct message and suppress the "trust" affordance.

Scope fix hint: *import* if any MISSING, else *trust* if any INVALID, else *unfixable*.

---

## 3. The architecture decision: pinentry from a TUI ("Pattern A")

`--quick-lsign-key` (passphrase + a `Really sign? (y/N)` confirm) and `pass edit`'s `$EDITOR` need a real, cooked-mode controlling terminal. Today the picker runs actions as a `tea.Cmd` **inside the still-running program** (`picker.go:1069-1099`), which keeps raw/alt-screen mode — fine for copy/totp, fatal for an editor or an interactive sign.

**Pattern A — run the terminal-grabbing child in the gap between `tea` programs** (the picker already runs each screen as its own `tea.NewProgram().Run()`):

1. The picker sets its action and `tea.Quit`s, returning a `PickResult` (existing mechanism, `picker.go:1056-1064`).
2. `cli.go` wraps the `ui.Pick` call (`cli.go:283-308`) in a **for-loop**: on a terminal action, the program has fully torn down (terminal restored), so it runs the child against the real tty, refreshes entries, and **relaunches** the picker preserving `Filter`/`MFAOnly` and a new `SelectPath` so the cursor survives.
3. On quit/empty → return.

Two details are load-bearing (each gets a regression test):

- **Do NOT call `procutil.ConfigureCommandCancellation` on these children.** Its `Setpgid:true` (`exec_unix.go:13`) puts the child outside the terminal's foreground process group → `SIGTTIN` hang on read and Ctrl-C theft. Inherit the parent group; the child must be the foreground tty owner. Set `GPG_TTY` via the existing `passstore.SetupGPGTTY`/`gpgTTYEnv` (already called at `cli.go:249`).
- **Timeout.** The 12s `interactiveActionTimeout` (`cli.go:96`) wraps only the in-program path, so gap actions (`$EDITOR`, lsign) get no cap for free — run under `context.Background()`. Fast writes (`insert`/`generate`/`rm`) stay in-program but under a new `writeActionTimeout` (~60s) to survive a git commit-signing pinentry.

*Pattern B* (`tea.ExecProcess` / `ReleaseTerminal`, confirmed present in v2.0.7) is kept only as a documented fallback if relaunch flicker proves unacceptable; it would pull the action back into `Update` and break the clean `cli`-owns-`passstore` separation.

---

## 4. The roadmap

Each phase is independently shippable and keeps CI green (`gofmt -l`, `go vet`, `go test`, `-race`, `govulncheck`, `goreleaser check`).

### Phase 0 — verdict engine + resolver + doctor fix + `passage access`
Pure read path; also fixes the latent ownertrust bug.
- `passstore`: promote `ParseGPGID` (from `gpgdiag.go:285`); add `ResolveRecipientsFile(entryDir)` walking the entry dir → root for the nearest `.gpg-id` (handles the root-has-no-`.gpg-id`-but-subdirs case); add `ScopeDirs()` (a `WalkDir` mirroring `Discover`, collecting `.gpg-id` dirs at **any** depth — replacing the root+1-level walk at `gpgdiag.go:101-121`).
- `gpgdiag`: rewrite `recipientStatus` to `owned | encryptable | invalid | unusable | missing` via probe-encrypt + a validity read; drop the trust map from the decision (keep `ownerTrust()` as diagnostic only for `keys`); add `scopeWritable(ids)`; rewrite `VerifyStores` over `ScopeDirs()`; rename `StoreReport` buckets (`Trusted`→`Encryptable`, `Untrusted`→`Invalid`, add `Unusable`).
- `gpgdiag`: `AccessReport`/`ScopeReport` types (`schema_version: 1`) + `Access(entry)` / `AccessAll()`.
- `cli`: `passage access [ENTRY] [--json]`; rewire `doctor` output to the new buckets.

### Phase 1 — `internal/gpgtrust` + `passage trust`
gpgobble-style trust, CLI only (no TUI yet). Plan/Apply split so dry-run == don't-call-Apply.
- `PlanRecipients(scope, strength)` and `PlanImportDir(dir, strength)`; `Apply(plan)`.
- lsign via `runGPGInteractive` (`GPG_TTY` env, `os.Std*` fds, **no** `ConfigureCommandCancellation`, no `--batch` so pinentry/`y/N` work); `--import-dir` does `gpg --import` first, peeking primary fps via `--import-options import-show`.
- `--full` adds one batched `--import-ownertrust` of `<fp>:4:` lines, **never-downgrade** (skip levels 4/5).
- `passage trust [--full] [--import-dir DIR] [SCOPE] [--json]`; `--json` emits the Plan only (no interactive Apply under `--json`).

### Phase 2 — store write verbs (`passstore` + CLI)
- `Insert(ctx, entry, content, opts{Multiline})`, `Generate(ctx, entry, opts{NoSymbols,Length}) ([]byte,error)`, `Remove(ctx, entry, opts{Recursive})` — buffered, keep `ConfigureCommandCancellation`. `Edit(ctx, entry)` inherits the real tty, Background ctx, **no** `ConfigureCommandCancellation` (gap-path only).
- **Pre-flight every write**: `ResolveRecipientsFile` + `scopeWritable` *before* exec; refuse read-only/no-access with a typed `ErrReadOnly` pointing to `passage trust`.
- `passage insert | generate | edit | rm` verbs (scriptable, `schema_version: 1`). **`rm` is in v1** (strongest confirm in the TUI).

### Phase 3 — Pattern-A terminal handoff
- `ui`: `PickOptions.SelectPath`; `PickResult`/`ActionRequest` gain `Content`, `Multiline`, `Scope`, `Fingerprints`.
- `cli`: convert the `ui.Pick` call site to the for-loop; add `runTerminalAction` (edit/trust) torn-down with no 12s cap, no `Setpgid`, `GPG_TTY`; refresh + relaunch on return. Add `writeActionTimeout` for in-program writes.

### Phase 4 — full TUI
- `passstore.Entry` gains `Writable` (`writable|read_only|no_access`), `GPGIDPath`, `Invalid`/`Missing` slices, precomputed **per scope** at load (cached so `View` never shells `gpg`).
- `ui`: reusable masked `textField` sub-model; a multi-step `composer` (hosted like `themeEditor`); `Action` enum `ActionNew/ActionGenerate/ActionEdit/ActionRemove/ActionTrust`; `RO`/`NO` badge in `entryMarkers`; detail-pane verdict block; `?` help WRITE+TRUST groups; keys `n/g/e/d/t` bound **only when the filter query is empty** (same guard `?` uses, `picker.go:394`).
- Gating: a write key on a read-only scope opens a confirm offering to jump into Trust; no-access shows an import hint with no trust offer.
- **`--full` is exposed in the TUI trust preview** as a toggle (per maintainer decision), not just the CLI flag.

---

## 5. TUI spec

Key scheme: single letters when the filter is empty — `n` new · `g` generate · `e` edit · `d` delete · `t` trust. (Ctrl-chords are nearly exhausted: `ctrl+e` = clear-recents, `ctrl+t` = totp.) Verdict is precomputed per scope and cached; `View` never shells out (honors the decrypt-free detail-pane invariant, `picker.go:888-890`).

**Read-only badge + detail verdict**
```
> 12  work/aws/prod/db                 PIN RO   3d ago
  14  personal/bank                     NO       never

┌ detail ─────────────────────────┐
│ work/aws/prod/db                 │
│ ★ pinned · used 3d ago           │
│ ACCESS  read-only                │
│ .gpg-id work/aws/.gpg-id         │
│ blocked (2):                     │
│   alice@corp  3AF9…B21  invalid  │
│   ci-bot@corp E0C4…77A  invalid  │
│ t  trust to make writable        │
│ enter copy · ^R reveal · ^T totp │
└──────────────────────────────────┘
```
`RO` = read_only, `NO` = no_access, styled with the warning role (not accent). Expired/revoked recipients render `expired — cannot fix` and suppress the `t` hint.

**Create (`n`)** — a multi-step composer:
```
╭ new entry ───────────────╮  ╭ new entry · secret ──────╮  ╭ new entry · generate ────╮
│ path                     │  │ ( ) type a password      │  │ length   [ 20 ]  ◂ ▸     │
│ work/aws/prod/db▮        │  │ (•) generate             │  │ symbols  [ on ]          │
│ folder work/aws/.gpg-id  │  │ password                 │  │                          │
│ writable ✓               │  │ ••••••••▮   (^G generate)│  │ enter make  esc back     │
│ enter next  esc cancel   │  │ tab next  esc cancel     │  ╰──────────────────────────╯
╰──────────────────────────╯  ╰──────────────────────────╯
```
If the resolved folder is read-only the composer does not open (see gating). Optional multiline body uses `textField` (enter = newline, `^D` = done) — kept simple in v1; full body editing defers to `$EDITOR`.

**Edit (`e`)** — choose surface:
```
╭ edit work/github ────────────╮   A) $EDITOR → Pattern-A gap (real tty, no 12s cap),
│ e  open $EDITOR (full file)  │      then refresh + relaunch at SelectPath.
│ l  edit first line in passage│   B) in-TUI first line → a textField prefilled with
│ esc cancel                   │      line 1; Enter re-encrypts via Insert, body kept.
╰──────────────────────────────╯
```

**Make writable / trust (`t`)** — dry-run preview → Danger confirm → gap-run lsign (pinentry):
```
╭ trust · preview ─────────────────────────────╮   ╭ confirm ──────────────────────────╮
│ folder  work/aws/.gpg-id                      │   │ Local-sign 2 keys to make          │
│ will local-sign (lsign) 2 keys:               │   │ work/aws writable?                 │
│   alice@corp   3AF9 1C…  B21                  │   │ lsign is local-only, reversible    │
│   ci-bot@corp  E0C4 9D…  77A                  │   │ y confirm   esc cancel             │
│ already valid: 1 (you)                        │   ╰────────────────────────────────────╯
│ strength  ( ) lsign (local)  ( ) --full       │   → gap-run lsign (no 12s) →
│ y trust   f toggle --full   i import folder…  │   setNotice 'Trusted 2 keys ·
│ esc cancel                                    │   work/aws is now writable.'
╰───────────────────────────────────────────────┘   Entries refresh → RO badge clears.
```
`f` toggles `--full` (ownertrust) per the maintainer decision. `i` opens a `textField` for a key-folder path → `gpg --import` then lsign each (gpgobble parity), previewing imported UIDs before the same confirm.

**Delete (`d`)** — the existing confirm strip (`startConfirm`/`confirmText`, `picker.go:1227-1286`); the only irreversible verb, strongest confirm.

**Gating a write into a read-only folder** — `n`/`g`/`e` on a read_only scope does not open the composer: `setNotice 'work/aws is read-only — 2 untrusted recipients.'` + a confirm offering to trust now. `no_access` (missing key) shows `Cannot write — recipient key missing (run passage trust --import-dir).` with no trust offer.

---

## 6. Engine specs

### `internal/gpgtrust`
Sibling of `gpgdiag` (acyclic; reuses both `gpgdiag` read probes and `passstore` resolution).
- **Types:** `Strength{Lsign(default)|Full}`; `RecipientPlan{Token, Fingerprint, UID, CurrentValidity, Action(would-lsign|already-valid|owned-skip|missing|would-import), WouldSetTrust}`; `Plan{Scope, GPGIDPath, Strength, Recipients[], ImportFiles[]}`; `ApplyResult` / `ApplyReport`.
- **PlanRecipients:** resolve nearest `.gpg-id`; per token — `hasSecret`→owned-skip; `list-keys` empty→missing; else resolve primary fp + probe-encrypt → already-valid, else would-lsign.
- **PlanImportDir:** shallow-list `*.asc`/`*.gpg`/`*.pub`, peek primary fps with `--import-options import-show` (no real import), mark would-import for fps not yet present, then the same classification. **Signs all imported keys** (gpgobble parity, per maintainer decision).
- **Apply (only mutating call, idempotent):** import (skip-on-fail); per would-lsign recipient `runGPGInteractive(--quiet --yes --quick-lsign-key <fp>)`; `--full` → batched `--import-ownertrust` of `<fp>:4:`, never-downgrade. Re-run `scopeWritable` to confirm the flip (belt-and-suspenders: `--check-trustdb` + re-probe).
- **`runGPGInteractive`:** `GPG_TTY` env, `os.Stdin`/stdout→stderr (keep JSON stdout clean), **no** `ConfigureCommandCancellation`, outside `interactiveActionTimeout`.

### Write verbs (`passstore`)
Clone `Show`'s env scaffold via shared `writeEnv()`. `Insert`: argv `insert` always `-f` (overwrite gated by passage's own confirm), `-m` if multiline, stdin = `bytes.Reader`. `Generate`: always `-f`, `-n`/length opts, capture the value by ANSI-stripping stdout's last non-empty line (not a re-Show decrypt → no second pinentry). `Remove`: always `-f`, `-r` if recursive. `Edit`: inherits the real tty, gap-only. **Git is transparent** — `pass` auto-commits iff the store is a git repo; commit-signing uses the wired `GPG_TTY`; optionally `setNotice 'committed to git'`.

---

## 7. Test strategy

1. **Hermetic (no keyring)** — extend the `cli_test.go` `makeScript` fake-binary harness with a scriptable fake `gpg`: `GPG_FAKE_ENCRYPTABLE` makes `-e -r <id>` exit 0/2; `GPG_FAKE_VALIDITY=token:char` emits colon records (incl. multi-UID rows with differing field-2 to reproduce the optimism bug); `GPG_FAKE_SECRET` drives `--list-secret-keys`. Plus a fake `pass insert/generate/edit/rm` writing a `.gpg` sentinel. Covers every verdict×fixability combo, the read-only pre-flight refusal, and `completions_drift_test`.
2. **Real-gpg integration** — guarded by `exec.LookPath("gpg")` + `t.TempDir()` `GNUPGHOME`. Reproduces the probe matrix end-to-end: bare-import fails → read_only; after lsign, probe succeeds, validity flips to `f`, **no** ownertrust written, verdict → writable; ownertrust-4/5-no-lsign stays failing (guards the old rule from regressing); expired/revoked stays unusable after lsign; multi-UID signed/unsigned selector divergence. `gpgtrust` `Apply` tested against the same throwaway homedir.
3. **UI** — `picker_test.go` for `textField`, composer step sequencing, badge rendering from `Entry.Writable`, gating; Pattern-A relaunch tested at the cli layer with a fake terminal action asserting `SelectPath`/`Filter` survive relaunch.

---

## 8. Files to touch

`internal/gpgdiag/{gpgdiag.go,gpgdiag_test.go}` · `internal/passstore/{passstore.go,passstore_test.go}` · `internal/gpgtrust/{gpgtrust.go,gpgtrust_test.go}` (new) · `internal/ui/{picker.go,textfield.go,composer.go,picker_test.go}` · `internal/cli/{cli.go,cli_test.go,completions_drift_test.go}` · `completions/passage.{bash,zsh,fish}` · `man/passage.1` · `docs/non-interactive.md` · `README.md`.

Reminder (see CONTRIBUTING.md:16-22): each new subcommand (`access`, `trust`, `insert`, `generate`, `edit`, `rm`) must be added to the **hardcoded command list in `completions_drift_test.go`** *and* all three completion files, or CI fails — plus the dispatch case, a usage const, a `runHelp` case, the master usage list, man, docs, README.

---

## 9. Risks & what we deliberately watch

| risk | mitigation |
|---|---|
| **Verdict correctness (highest)** — naive validity read is wrong on expiry/revocation/multi-UID | probe-encrypt to the literal `.gpg-id` selectors is the gate; validity only explains failures |
| **Performance** — probe-encrypt at picker load on a big store | one probe per *distinct scope* (not per entry), cached; encrypt-to-/dev/null is public-key-only (cheap); fall back to lazy/on-select if the open stalls |
| **TTY / process-group** for gap children | explicit no-`Setpgid` path + a test asserting the interactive child inherits the parent pgroup; confirm no passage SIGINT handler intercepts |
| **Commit-signing timeout** — `pass.signcommits` prompts pinentry on write | `writeActionTimeout` (~60s) for in-program writes; gap-path (edit/trust) has no cap |
| **JSON shape break** — renaming `StoreReport` buckets while keeping `schema_version:1` | pre-1.0 internal; rename in one commit + update docs; add deprecated aliases or bump schema if an external consumer surfaces |
| **Key-binding leak** — single letters into the filter | bind only when query is empty (same guard `?` uses); document that filter-then-act needs arrow selection |
| **Relaunch flicker** (Pattern A) | restore `SelectPath`/`Filter`/`MFAOnly`; Pattern B (`tea.ExecProcess`) is the fallback |
| **Scope edge** — root has no `.gpg-id` but subdirs do | `ScopeDirs` enumerates real scopes at any depth; entries directly under root resolve to UNINITIALIZED |
| **`--import-dir` footgun** — imports arbitrary key files | strictly opt-in, always previewed before Apply |

**Won't do:** adopt `--trust-model always` as a default (it would silently encrypt to unvalidated keys and mask the very problem `trust` exists to fix).

---

## 10. Resolved decisions

| decision | resolution |
|---|---|
| Scope | write verbs **and** verdict **and** trust **and** full TUI |
| Trust input | **both** — store-scope recipients *and* `--import-dir` folder of keys |
| Trust strength default | **lsign-only**; `--full` (ownertrust) opt-in |
| Verdict signal | **probe-encrypt** (not ownertrust, not a bare validity read) |
| New vs extend doctor | **new** `passage access` + `passage trust`; doctor reuses the fixed engine |
| `rm` in v1 | **yes** (strongest confirm) |
| `--import-dir` sign scope | **sign all** imported keys (gpgobble parity) |
| `--full` in the TUI | **yes** — exposed as a toggle in the trust preview, not CLI-only |

---

## 11. Recommended first PR

**Phase 0 only.** It is self-contained, ships real value (the read-only/writable verdict + `passage access`), fixes the latent ownertrust correctness bug in `doctor`, and carries **zero TUI or pinentry risk** — the validity/probe-encrypt foundation everything else builds on, landed and tested first.
