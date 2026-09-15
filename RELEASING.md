# Releasing

A release is cut by pushing a `vX.Y.Z` tag, which triggers the goreleaser
**Release** workflow (`.github/workflows/release.yml`). The version is taken from
the tag (`-ldflags -X main.version`), so there is nothing to edit in code.

## Steps

1. Make sure `main` is green and holds everything to ship.

2. **If any pinned `0xbenc` module changed, release those first** — bottom-up,
   `termtheme` → `termnav` / `termchrome`; `termintro` is independent
   (each repo has its own `RELEASING.md`) — then bump the pins here:

   ```sh
   go get github.com/0xbenc/termtheme@vX.Y.Z     # only the ones that changed
   go get github.com/0xbenc/termnav@vX.Y.Z
   go get github.com/0xbenc/termchrome@vX.Y.Z
   go get github.com/0xbenc/termintro@vX.Y.Z
   go mod tidy && go test ./...
   git commit -am "Bump term* pins"
   ```

   passage pins these by tag with **no `replace` directive**, so every tag
   **must already exist on the proxy** before this release builds in CI — that
   is why the shared modules are tagged first, bottom-up.

   Lockstep: passage and ssherpa pin identical `termtheme`/`termnav`/
   `termchrome` versions. When one of those three bumps, bump **both** apps
   (the hotfix exception lives in `CONTRIBUTING.md`).

3. Tag and push:

   ```sh
   git tag -a vX.Y.Z -m "..."
   git push origin main
   git push origin vX.Y.Z      # triggers the Release workflow
   ```

If no pinned module changed, skip step 2 entirely.

## Versioning

Semantic versioning. Backward-compatible features are a **minor** bump;
fixes are a **patch** bump.
