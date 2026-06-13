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
