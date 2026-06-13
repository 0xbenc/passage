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

### doctor

```sh
passage doctor [--json] [--store-dir PATH]
```

Checks for `pass`, `gpg`, clipboard tools, and `.gpg-id` recipient health.

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
| `2` | Entry not found or doctor warnings. |
