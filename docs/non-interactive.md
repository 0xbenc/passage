# passage Non-Interactive CLI Reference

This document covers the scriptable `passage` surface.

## Conventions

- `ENTRY` is a GNU Pass entry path without `.gpg`.
- Password means the first line returned by `pass show -- ENTRY`.
- JSON output includes `schema_version`.
- `--store-dir PATH` overrides `PASSWORD_STORE_DIR`.
- `--state-dir PATH` overrides `PASSAGE_STATE_DIR`.

## Defaults

| Item | Default |
| --- | --- |
| Pass store | `PASSWORD_STORE_DIR` or `~/.password-store` |
| Linux state dir | `${XDG_STATE_HOME:-~/.local/state}/passage` |
| macOS state dir | `~/Library/Application Support/passage` |
| Theme file | platform config dir + `/passage/theme.conf` |

## Environment Variables

| Variable | Effect |
| --- | --- |
| `PASSWORD_STORE_DIR` | Pass store root. |
| `PASSAGE_STATE_DIR` | State directory. |
| `PASSAGE_PASS_BINARY` | Override `pass`, primarily for tests. |
| `PASSAGE_THEME_FILE` | Theme config file. |
| `PASSAGE_NO_COLOR` | Disable color. |
| `PASSAGE_NO_OSC52` | Disable terminal OSC 52 clipboard fallback. |
| `NO_COLOR` | Disable color. |
| `WAYLAND_DISPLAY` | Prefer `wl-copy` before `xclip`. |

## Commands

### list

```sh
passage list [--json] [--filter TEXT] [--mfa] [--store-dir PATH] [--state-dir PATH]
```

Lists entries sorted by pinned first, newest recent second, and path third.

### show

```sh
passage show ENTRY [--json] [--store-dir PATH] [--state-dir PATH]
```

Shows metadata only. It never decrypts or prints secrets.

### copy

```sh
passage copy ENTRY [--private] [--store-dir PATH] [--state-dir PATH]
```

Decrypts `ENTRY`, copies the first line, and updates MRU unless `--private` is set.

### reveal

```sh
passage reveal ENTRY [--private] [--store-dir PATH] [--state-dir PATH]
```

Copies and reveals the first line until a key is pressed.

### totp

```sh
passage totp ENTRY [--json] [--no-copy] [--private] [--wait] [--at UNIX] [--store-dir PATH] [--state-dir PATH]
```

Uses `ENTRY/mfa` when present. Direct `*/mfa` entries also work. The MFA entry
must contain one non-empty base32 line. `--wait` waits for the next code when
the current one has three seconds or less remaining.

### pin / unpin

```sh
passage pin ENTRY
passage unpin ENTRY
```

### clear-recents / clear-pins / clear-clipboard

```sh
passage clear-recents
passage clear-pins
passage clear-clipboard
```

### insert / generate / edit / rm

```sh
passage insert ENTRY [--multiline] [--force] [--store-dir PATH]
passage generate ENTRY [LENGTH] [--no-symbols] [--no-copy] [--force] [--json]
passage edit ENTRY [--store-dir PATH]
passage rm ENTRY [--recursive] [--yes] [--json] [--store-dir PATH]
```

The store-writing verbs. They delegate to `pass` (which remains the source of
truth) and pre-flight the writable verdict, refusing early with a pointer to
`passage trust` when the target folder is read-only, rather than letting gpg
hard-fail mid-encrypt.

- `insert` reads the secret from stdin — the first line, or the whole stream
  with `--multiline`. `--force` overwrites an existing entry.
- `generate` creates a random password (optional `LENGTH`, `--no-symbols`) and
  copies it to the clipboard unless `--no-copy`; the secret is never printed.
- `edit` opens the entry in `$EDITOR` and re-encrypts on save (needs a terminal).
- `rm` deletes an entry, or a subtree with `--recursive`; it confirms unless
  `--yes`. (Removal does not encrypt, so it is not gated on writability.)

`insert`, `generate`, and `rm` accept `--json`, emitting `{schema_version,
entry, action}`.

### doctor

```sh
passage doctor [--json] [--store-dir PATH]
```

Checks for `pass`, `gpg`, clipboard tools, and `.gpg-id` recipient health.

### access

```sh
passage access [ENTRY] [--json] [--store-dir PATH]
```

Reports, per `.gpg-id` scope, whether you can write (encrypt to every
recipient). Each scope's `verdict` is one of:

- `writable` — gpg can encrypt to all recipients; `pass insert/edit` will work.
- `read_only` — you own a recipient (can decrypt) but at least one recipient is
  not a valid encryption target, so writes would fail.
- `no_access` — cannot encrypt to all recipients and you own none.
- `uninitialized` — no `.gpg-id` governs the path.

Writability is decided by a real probe-encrypt (`gpg --encrypt` to the literal
recipients), not by ownertrust — a locally-signed key counts as writable. When
not writable, `fixable` hints the remedy: `trust` (local-sign present keys),
`import` (a recipient key is missing), or `unfixable` (expired/revoked). With an
`ENTRY` (or folder path) argument, only the governing scope is reported. The
command exits `2` when any reported scope is not writable.

JSON envelope: `schema_version`, `store_root`, optional `entry`, and `scopes[]`
with `label`, `scope`, `gpg_id_path`, `verdict`, `recipient_count`, the
recipient buckets (`owned`, `encryptable`, `invalid`, `unusable`, `missing`),
and `fixable`.

### trust

```sh
passage trust [SCOPE] [--full] [--yes] [--json] [--store-dir PATH]
passage trust --import-dir DIR [--full] [--yes] [--json] [--store-dir PATH]
```

Makes a read-only scope writable by local-signing the recipients gpg cannot yet
encrypt to (the Go-native port of bash-zoo's `gpgobble`). `SCOPE` is an entry
path or folder; its nearest `.gpg-id` governs. Default strength is **local-sign
only** — exactly the validity pass needs — while `--full` additionally raises
ownertrust to full (5), never downgrading an existing 5/6.

- `--import-dir DIR` imports every public-key file in `DIR` and trusts them all
  (gpgobble parity), instead of a store scope's recipients.
- `--yes` applies without the interactive confirmation.
- `--json` prints the dry-run **plan only** (`schema_version`, `scope`,
  `gpg_id_path`, `strength`, `recipients[]` with `token`/`fingerprint`/`action`)
  and never mutates the keyring.

Local-signing uses your own secret key, so a passphrase prompt (pinentry) may
appear. It is idempotent: re-running re-signs nothing and never downgrades
trust. After applying, run `passage access SCOPE` to confirm the scope flipped
to `writable`.

A recipient whose **secret key you hold** but that isn't a valid encryption
target yet (the common "new machine, freshly imported secret key" case — gpg
does not auto-trust imported keys) is set to **ultimate** ownertrust rather than
local-signed: holding the secret proves it's yours. This is what makes your own
store writable again on a new machine.

### keys

```sh
passage keys [--json] [--store-dir PATH]
```

Lists local public keys, ownertrust, and whether the secret key is present.

### version

```sh
passage version
```

Prints:

```text
passage VERSION
commit: COMMIT
built: DATE
```

## Exit Codes

| Code | Meaning |
| --- | --- |
| `0` | Success. |
| `1` | Usage, validation, decrypt, write, or runtime failure. |
| `2` | Entry not found, doctor warnings, or a non-writable `access` scope. |
