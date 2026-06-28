# Contributing

Use the repo's existing Go style: small packages, explicit command contracts,
and focused tests around behavior that touches secrets, state, or external
commands.

Before opening a PR, run:

```sh
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
```

When adding user-facing commands, update:

- `internal/cli/cli.go`
- `docs/non-interactive.md`
- `README.md`
- `man/passage.1`
- `completions/passage.{bash,zsh,fish}`

Tests should use fake `pass`, `gpg`, and clipboard binaries where possible.
Never require a real password store or real GPG key material in CI.

## TUI alignment & drift guards

passage shares its terminal-UI primitives with [ssherpa](https://github.com/0xbenc/ssherpa)
through three semver-pinned modules — `termtheme` (roles + `.theme`), `termnav`
(navigation / list windowing), and `termchrome` (box/footer/kvrow + glyphs/countdown).
Keep them aligned:

- **Footers** go through `termstyle.Footer([]termstyle.KeyHint{...})` — never hand-build
  a multi-space separator. CI greps for `  /  ` and triple-space footer literals.
- **Spinners / progress** use `termstyle.ResolveGlyphs` — never inline frame runes
  (`[]rune{'|','/','-','\\'}`). CI greps for these.
- **No golden-update flag.** There is no `-update`/`UPDATE_GOLDEN`; "goldens" are inline
  string literals. A chrome/pixel change means hand-editing the expectations and keeping
  `assertBorderIntegrity` + `TestTruncateStyledSanitizesOnOverflow` green.
- **Interaction contract.** `docs/flow-contract.md` is the interaction source of truth for
  passage **and** ssherpa. Any PR changing an interactive surface must keep this app
  conformant and, if it changes shared grammar, update the contract (ssherpa references
  this copy).
- **Cross-repo pin lockstep.** passage and ssherpa pin **identical** termtheme/termnav/
  termchrome versions, with **no `replace`** in the released `go.mod`. When bumping a
  shared module, bump both apps. *Hotfix exception:* an urgent single-app fix may bump one
  app ahead; restore lockstep in the next release.

See `0xbenc/docs/tui-alignment/` for the program and `RELEASING.md` for the bottom-up tag
order (`termtheme → {termnav, termchrome} → apps`).
