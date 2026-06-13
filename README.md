# passage

[![CI](https://github.com/0xbenc/passage/actions/workflows/ci.yml/badge.svg)](https://github.com/0xbenc/passage/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/0xbenc/passage?sort=semver)](https://github.com/0xbenc/passage/releases/latest)
[![Go](https://img.shields.io/badge/go-1.26.3-00ADD8.svg)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

GNU Pass as the source of truth, with a fast TUI for daily secret retrieval.

`passage` is a standalone Go rewrite of the Bash Zoo `passage` helper. It
reads your real GNU Pass store, keeps lightweight local state for pins and
recents, copies passwords without printing them, and generates TOTP codes from
the same `entry/mfa` convention used by the old script.

## Features

- Full-screen terminal picker with filtering, pins, recents, and MFA badges.
- Password copy and reveal actions. Passwords are the first line of `pass show`.
- Native SHA1 TOTP generation; `oathtool` is no longer required.
- Old Bash TSV state migration from `bash-zoo/passage` and `bash-zoo/pass-browse`.
- Scriptable commands with stable JSON envelopes.
- `doctor` checks for `pass`, `gpg`, clipboard tools, and `.gpg-id` recipient health.
- GoReleaser archives, Linux packages, and Homebrew cask publishing.

## Install

### Homebrew cask

```sh
brew install --cask 0xbenc/tap/passage
```

Or:

```sh
brew tap 0xbenc/tap
brew install --cask passage
```

### Release artifacts

Download the latest macOS or Linux artifact from
[GitHub Releases](https://github.com/0xbenc/passage/releases/latest).

Archives are published for:

- `darwin_amd64`
- `darwin_arm64`
- `linux_amd64`
- `linux_arm64`

```sh
tar -xzf passage_VERSION_OS_ARCH.tar.gz
sudo install -m 0755 passage /usr/local/bin/passage
```

Linux packages are also published as `.deb` and `.rpm`.

### From source

Requires Go 1.26.3 or newer.

```sh
git clone https://github.com/0xbenc/passage.git
cd passage
go build -trimpath -o passage ./cmd/passage
sudo install -m 0755 passage /usr/local/bin/passage
```

## Runtime Requirements

- `pass`
- `gpg`
- Clipboard support through `pbcopy`, `wl-copy`, `xclip`, `xsel`, or terminal OSC 52

`passage` honors `PASSWORD_STORE_DIR`; otherwise it reads `~/.password-store`.

## Quick Use

```sh
passage                 # open picker
passage github          # open picker with an initial filter
passage mfa github      # open MFA-only picker with a filter
passage list --json
passage copy work/github
passage totp work/github
passage doctor
```

In the TUI:

- Printable characters always type into the filter.
- `enter`: default action, copy password or generate TOTP for an MFA-secret row
- `ctrl+y`: copy password, or TOTP in MFA-only mode
- `ctrl+r`: reveal password, or TOTP in MFA-only mode
- `ctrl+t`: show/copy TOTP, or reveal the stored secret on an MFA-secret row
- `ctrl+p`: toggle pin
- `ctrl+f`: toggle MFA-only view
- `ctrl+o`: open theme editor
- `ctrl+x`: clear clipboard
- `ctrl+u`: unpin all
- `ctrl+e`: clear recents
- `ctrl+d`: doctor
- `ctrl+k`: keys
- `esc`, `ctrl+c`, or `ctrl+q`: quit

## Theme Editor

Press `ctrl+o` from the picker to edit the semantic text roles used by the TUI.
Use arrows to cycle role presets, `e` to type a raw style such as `bold cyan`,
`d` to inherit the default, and `s` to save. Themes are written to
`${XDG_CONFIG_HOME:-~/.config}/passage/theme.conf` unless `PASSAGE_THEME_FILE`
or `--theme-file` points elsewhere.

## MFA Convention

For an entry `work/github`, store the TOTP secret at:

```sh
pass insert work/github/mfa
```

The MFA entry must contain one non-empty base32 secret line. `otpauth://` URIs
are rejected; store the raw base32 secret instead.

## State

Linux state defaults to:

```text
~/.local/state/passage/state.json
```

macOS state defaults to:

```text
~/Library/Application Support/passage/state.json
```

Set `PASSAGE_STATE_DIR` or pass `--state-dir` to override.

## Environment

- `PASSWORD_STORE_DIR`: GNU Pass store path.
- `PASSAGE_STATE_DIR`: state directory override.
- `PASSAGE_PASS_BINARY`: testing/advanced override for the `pass` binary.
- `PASSAGE_THEME_FILE`: theme config path.
- `PASSAGE_NO_COLOR` or `NO_COLOR`: disable color.
- `PASSAGE_NO_OSC52`: disable terminal OSC 52 clipboard fallback.
- `WAYLAND_DISPLAY`: prefers `wl-copy` before `xclip`.

## Docs

- [Non-interactive CLI reference](docs/non-interactive.md)

## License

[MIT](LICENSE) (c) Ben Chapman
