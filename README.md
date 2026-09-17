# passage

Where's the password? passage is a full-screen terminal picker for your GNU Pass store: type a few letters, hit enter, and the password is on your clipboard — without ever being printed.

GNU Pass stays the source of truth. passage keeps lightweight local state for pins and recents, generates TOTP codes, and handles the GPG trust side so a read-only shared store can become writable again.

## Getting started

1. Install it: `brew install --cask 0xbenc/tap/passage` — or a [release](#install), or [from source](#install).
2. Run `passage`.
3. Type to filter, enter to copy. `?` opens the key reference.

Create, edit, and delete all happen from inside the picker. If something's not right, `passage doctor` will tell you what.

## In the picker

- Printable characters fuzzy-filter the list, with matches highlighted. Pins stay on top, recents follow, and MFA entries wear a badge. Wide terminals get a detail pane.
- `enter` copies the password — or the TOTP code on an MFA row. `ctrl+r` reveals on screen instead. `ctrl+f` toggles the MFA-only view, `ctrl+x` clears the clipboard.
- Shifted keys are commands: `N` create, `G` generate, `E` edit, `D` delete, `P` pin, `T` trust, `I` import + trust a folder of public keys, `S` set up your own secret key on a new machine. Lowercase always filters, so typing never collides.
- Creating a new entry: `tab` completes folders shell-style and lists the folder's existing entries below, so you can confirm a folder really exists before adding under it (`tab` descends, `shift+tab` goes up). A typed secret is entered twice and must match; `ctrl+r` toggles the mask while typing.
- The status line carries a live TOTP countdown and a clipboard pill that auto-clears while passage is open. Folders you can't write to get an `RO` badge, and writes are refused early instead of letting GPG hard-fail mid-encrypt.
- If the terminal loses focus, secrets on screen are blanked.
- Mouse wheel scrolls, click selects. `ctrl+d` runs doctor in place; `ctrl+u` unpins all and `ctrl+e` clears recents (both ask first). `esc`, `ctrl+c`, or `ctrl+q` quits.

## TOTP

For an entry `work/github`, store the secret as a sibling entry:

```sh
pass insert work/github/mfa
```

One line of raw base32. `otpauth://` URIs are rejected — store the secret itself. Codes are generated natively (SHA1), so `oathtool` is no longer needed. From the shell: `passage totp work/github`, and `passage mfa` opens the picker with MFA entries only — narrowed to a single entry (`passage mfa github`), it copies that TOTP directly, waiting out the window first if the code is about to expire.

## Read-only stores

Shared pass stores often arrive in a state where you can decrypt but not write: your key is in `.gpg-id`, but GPG can't encrypt to every recipient. passage deals with this directly:

- `passage access` reports, per `.gpg-id` scope, whether you can write — decided by a real probe-encrypt, not by ownertrust.
- `passage trust` locally signs the recipients GPG can't yet encrypt to (a Go-native port of bash-zoo's `gpgobble`). Idempotent, and it never downgrades trust.
- `passage trust --import-dir DIR` imports a whole folder of public keys and trusts them at once.

On a new machine, `S` in the picker imports your own secret key and sets ultimate trust where it proves yours — that's usually the step that makes the store writable again.

## Themes

`passage theme` (or `ctrl+o` from the picker) opens the theme builder. The top row picks a base palette — `terminal` (16-color ANSI) or `vivid` (24-bit truecolor) — and the rows below tune each text role on top of it: arrows cycle presets, `e` types a raw style like `bold cyan`, `d` inherits from the base, `r` resets, `t` checks contrast, `s` saves. The preview re-tints live.

Themes land in `${XDG_CONFIG_HOME:-~/.config}/passage/theme.conf` unless `PASSAGE_THEME_FILE` or `--theme-file` points elsewhere.

## Install

Homebrew:

```sh
brew install --cask 0xbenc/tap/passage
```

Releases: the latest macOS and Linux builds live on [GitHub Releases](https://github.com/0xbenc/passage/releases/latest) — `tar.gz` archives for darwin/linux × amd64/arm64, plus `.deb` and `.rpm`:

```sh
tar -xzf passage_VERSION_OS_ARCH.tar.gz
sudo install -m 0755 passage /usr/local/bin/passage
```

Source (Go 1.26.5 or newer):

```sh
git clone https://github.com/0xbenc/passage.git && cd passage
go build -trimpath -o passage ./cmd/passage
```

## What you need

- `pass` and `gpg`, already working — `passage doctor` checks.
- A clipboard: `pbcopy`, `wl-copy`, `xclip`, `xsel`, or terminal OSC 52.

The store is read from `PASSWORD_STORE_DIR`, or `~/.password-store` if that's unset. Pins and recents live in `~/.local/state/passage/state.json` (Linux) or `~/Library/Application Support/passage/state.json` (macOS); override with `PASSAGE_STATE_DIR` or `--state-dir`. Old bash-zoo `state.tsv` files from `passage`/`pass-browse` are migrated automatically.

Other environment: `PASSAGE_THEME_FILE` (theme config), `PASSAGE_PASS_BINARY` (testing), `PASSAGE_NO_COLOR` / `NO_COLOR`, `PASSAGE_NO_OSC52`, and `WAYLAND_DISPLAY` (prefers `wl-copy` over `xclip`).

## Scripting

Everything the picker does has a non-interactive twin: `passage copy work/github`, `passage totp work/github`, `passage list --json` — with stable JSON envelopes throughout. The full reference is in [docs/non-interactive.md](docs/non-interactive.md).

## Development

```sh
gofmt -w . && go vet ./... && go test ./... && go test -race ./...
```

passage is part of [termsystem](https://github.com/0xbenc/termsystem), the shared terminal-UI ecosystem: it uses `termtheme`, `termnav`, `termchrome`, and `termintro` alongside [ssherpa](https://github.com/0xbenc/ssherpa). [CONTRIBUTING.md](CONTRIBUTING.md) covers the alignment rules between them.

## License

[MIT](LICENSE) (c) Ben Chapman
