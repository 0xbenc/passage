package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xbenc/passage/internal/termstyle"
	"github.com/0xbenc/passage/internal/ui"
)

// TestThemeCommandHelp verifies the standalone `passage theme` command is
// wired into dispatch and help without launching the (TTY-only) editor.
func TestThemeCommandHelp(t *testing.T) {
	cases := [][]string{
		{"theme", "--help"},
		{"help", "theme"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(args, &stdout, &stderr, BuildInfo{})
			if code != 0 {
				t.Fatalf("Run(%v) = %d, want 0; stderr=%s", args, code, stderr.String())
			}
			out := stdout.String()
			for _, want := range []string{"passage theme", "base palette", "Ctrl-O"} {
				if !strings.Contains(out, want) {
					t.Fatalf("theme usage missing %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestThemeCommandRejectsArgs ensures stray positional args are rejected rather
// than silently launching the editor.
func TestThemeCommandRejectsArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"theme", "bogus"}, &stdout, &stderr, BuildInfo{})
	if code != 1 {
		t.Fatalf("Run = %d, want 1; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "unexpected arguments") {
		t.Fatalf("stderr = %q, want unexpected arguments", stderr.String())
	}
}

// TestFormatThemeConfigRoundTripsBase pins the theme-choice persistence
// contract: a non-default base is written as `theme = <name>` and survives a
// format→parse round-trip, while the implicit terminal default is omitted to
// keep configs lean.
func TestFormatThemeConfigRoundTripsBase(t *testing.T) {
	cfg := termstyle.ThemeConfig{
		BaseName: "vivid",
		Codes:    map[termstyle.Role]string{termstyle.RolePrimary: "31"},
		Specs:    map[termstyle.Role]string{termstyle.RolePrimary: "red"},
	}
	data := formatThemeConfig(cfg)
	if !strings.Contains(string(data), "theme = vivid") {
		t.Fatalf("formatted config missing base line:\n%s", data)
	}
	parsed, err := termstyle.ParseThemeConfig(data)
	if err != nil {
		t.Fatalf("re-parse error: %v", err)
	}
	if parsed.BaseName != "vivid" {
		t.Fatalf("round-trip BaseName = %q, want vivid", parsed.BaseName)
	}
	if parsed.Specs[termstyle.RolePrimary] != "red" {
		t.Fatalf("round-trip primary spec = %q, want red", parsed.Specs[termstyle.RolePrimary])
	}

	// Terminal (the default) is left implicit.
	terminalCfg := termstyle.ThemeConfig{BaseName: "terminal"}
	if got := string(formatThemeConfig(terminalCfg)); strings.Contains(got, "theme =") {
		t.Fatalf("terminal base should be implicit, got:\n%s", got)
	}
}

func TestFirstRunGuidanceForMissingStore(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-store-here")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--store-dir", missing, "--state-dir", t.TempDir()}, &stdout, &stderr, BuildInfo{})
	if code != 1 {
		t.Fatalf("Run = %d, want 1; stderr=%s", code, stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{"No password store found", "pass init", "Environment check"} {
		if !strings.Contains(out, want) {
			t.Fatalf("first-run guidance missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "passage: password store directory") {
		t.Fatalf("first-run should replace the raw error, not append it:\n%s", out)
	}
}

func TestRunListJSON(t *testing.T) {
	store := fakeStore(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"list", "--json", "--store-dir", store, "--state-dir", t.TempDir()}, &stdout, &stderr, BuildInfo{})
	if code != 0 {
		t.Fatalf("Run = %d, stderr=%s", code, stderr.String())
	}
	var got struct {
		SchemaVersion int `json:"schema_version"`
		Entries       []struct {
			Path   string `json:"path"`
			HasMFA bool   `json:"has_mfa"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if got.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d", got.SchemaVersion)
	}
	if len(got.Entries) != 3 {
		t.Fatalf("entries = %#v", got.Entries)
	}
}

func TestRunCopyUsesFakePassAndClipboard(t *testing.T) {
	store := fakeStore(t)
	bin := t.TempDir()
	clipOut := filepath.Join(bin, "clip.txt")
	makeScript(t, bin, "pass", `if [ "$PASSWORD_STORE_DIR" != "$STORE_ROOT" ]; then echo "bad store: $PASSWORD_STORE_DIR" >&2; exit 8; fi
if [ "$1" = show ] && [ "$2" = -- ] && [ "$3" = work/github ]; then printf 'pw\nuser: alice\n'; exit 0; fi; exit 9`)
	makeScript(t, bin, "pbcopy", `/bin/cat > "$CLIP_OUT"`)
	t.Setenv("PATH", bin)
	t.Setenv("PASSAGE_PASS_BINARY", filepath.Join(bin, "pass"))
	t.Setenv("CLIP_OUT", clipOut)
	t.Setenv("STORE_ROOT", store)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"copy", "work/github", "--store-dir", store, "--state-dir", t.TempDir()}, &stdout, &stderr, BuildInfo{})
	if code != 0 {
		t.Fatalf("Run = %d, stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(clipOut)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "pw" {
		t.Fatalf("clipboard = %q", data)
	}
}

func TestRunFilterSingleMatchAutoCopies(t *testing.T) {
	store := fakeStore(t)
	bin := t.TempDir()
	clipOut := filepath.Join(bin, "clip.txt")
	makeScript(t, bin, "pass", `if [ "$PASSWORD_STORE_DIR" != "$STORE_ROOT" ]; then echo "bad store: $PASSWORD_STORE_DIR" >&2; exit 8; fi
if [ "$1" = show ] && [ "$2" = -- ] && [ "$3" = alpha ]; then printf 'sekret\nuser: bob\n'; exit 0; fi; exit 9`)
	makeScript(t, bin, "pbcopy", `/bin/cat > "$CLIP_OUT"`)
	t.Setenv("PATH", bin)
	t.Setenv("PASSAGE_PASS_BINARY", filepath.Join(bin, "pass"))
	t.Setenv("CLIP_OUT", clipOut)
	t.Setenv("STORE_ROOT", store)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"alpha", "--store-dir", store, "--state-dir", t.TempDir(), "--no-color"}, &stdout, &stderr, BuildInfo{})
	if code != 0 {
		t.Fatalf("Run = %d, stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(clipOut)
	if err != nil {
		t.Fatalf("ReadFile: %v (the picker likely opened instead of auto-copying); stderr=%s", err, stderr.String())
	}
	if string(data) != "sekret" {
		t.Fatalf("clipboard = %q", data)
	}
}

func TestRunMFAFilterSingleMatchShowsAndCopiesTOTP(t *testing.T) {
	store := fakeStore(t)
	bin := t.TempDir()
	clipOut := filepath.Join(bin, "clip.txt")
	// "mfa" matches only work/github/mfa among the MFA-capable entries.
	makeScript(t, bin, "pass", `if [ "$PASSWORD_STORE_DIR" != "$STORE_ROOT" ]; then echo "bad store: $PASSWORD_STORE_DIR" >&2; exit 8; fi
if [ "$1" = show ] && [ "$2" = -- ] && [ "$3" = work/github/mfa ]; then printf 'JBSWY3DPEHPK3PXP\n'; exit 0; fi; exit 9`)
	makeScript(t, bin, "pbcopy", `/bin/cat > "$CLIP_OUT"`)
	t.Setenv("PATH", bin)
	t.Setenv("PASSAGE_PASS_BINARY", filepath.Join(bin, "pass"))
	t.Setenv("CLIP_OUT", clipOut)
	t.Setenv("STORE_ROOT", store)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"mfa", "mfa", "--store-dir", store, "--state-dir", t.TempDir(), "--no-color"}, &stdout, &stderr, BuildInfo{})
	if code != 0 {
		t.Fatalf("Run = %d, stderr=%s", code, stderr.String())
	}
	shown := strings.TrimSpace(stdout.String())
	if len(shown) != 6 {
		t.Fatalf("TOTP shown onscreen = %q, want 6 digits", shown)
	}
	data, err := os.ReadFile(clipOut)
	if err != nil {
		t.Fatalf("ReadFile: %v (the picker likely opened instead of auto-running TOTP); stderr=%s", err, stderr.String())
	}
	if string(data) != shown {
		t.Fatalf("clipboard = %q, shown = %q", data, shown)
	}
}

// fakeGPGScript is a stub gpg driven by env vars, mirroring the verdict
// engine's needs (see gpgdiag tests).
const fakeGPGScript = `contains() { case " $2 " in *" $1 "*) return 0;; esac; return 1; }
mode=""; listid=""; recipients=""
while [ $# -gt 0 ]; do
  case "$1" in
    --list-secret-keys) mode=secret; shift; listid="$1"; shift ;;
    --list-keys) mode=listkeys; shift
      if [ $# -gt 0 ]; then case "$1" in --*) ;; *) listid="$1"; shift ;; esac; fi ;;
    --encrypt) mode=encrypt; shift ;;
    --recipient) shift; recipients="$recipients $1"; shift ;;
    --output) shift; shift ;;
    --export-ownertrust) mode=ownertrust; shift ;;
    *) shift ;;
  esac
done
case "$mode" in
  secret) contains "$listid" "$GPG_FAKE_SECRET" && exit 0; exit 2 ;;
  encrypt) for r in $recipients; do contains "$r" "$GPG_FAKE_ENCRYPTABLE" || exit 2; done; exit 0 ;;
  ownertrust) exit 0 ;;
  listkeys)
    if [ -n "$listid" ]; then
      if contains "$listid" "$GPG_FAKE_PRESENT" || contains "$listid" "$GPG_FAKE_SECRET"; then
        printf 'pub:-:::::::::::::\nfpr:::::::::%s:\nuid:-:::::::::%s:\n' "$listid" "$listid"; exit 0
      fi
      exit 2
    fi ;;
esac
exit 0`

func TestRunAccessJSON(t *testing.T) {
	root := t.TempDir()
	writeGPGIDFile(t, root, "owner")
	writeGPGIDFile(t, filepath.Join(root, "work"), "owner", "carol")

	bin := t.TempDir()
	makeScript(t, bin, "gpg", fakeGPGScript)
	t.Setenv("PATH", bin)
	t.Setenv("GPG_FAKE_SECRET", "owner")
	t.Setenv("GPG_FAKE_PRESENT", "owner carol")
	t.Setenv("GPG_FAKE_ENCRYPTABLE", "owner")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"access", "--json", "--store-dir", root}, &stdout, &stderr, BuildInfo{})
	// Exit 2 because the work scope is read-only.
	if code != 2 {
		t.Fatalf("Run access = %d, want 2; stderr=%s\nstdout=%s", code, stderr.String(), stdout.String())
	}
	var got struct {
		SchemaVersion int `json:"schema_version"`
		Scopes        []struct {
			Label   string `json:"label"`
			Verdict string `json:"verdict"`
			Fixable string `json:"fixable"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if got.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d", got.SchemaVersion)
	}
	verdicts := map[string]string{}
	for _, s := range got.Scopes {
		verdicts[s.Label] = s.Verdict
	}
	if verdicts["default"] != "writable" {
		t.Errorf("default verdict = %q, want writable", verdicts["default"])
	}
	if verdicts["work"] != "read_only" {
		t.Errorf("work verdict = %q, want read_only", verdicts["work"])
	}
}

func TestRunAccessSingleEntryWritable(t *testing.T) {
	root := t.TempDir()
	writeGPGIDFile(t, root, "owner")
	bin := t.TempDir()
	makeScript(t, bin, "gpg", fakeGPGScript)
	t.Setenv("PATH", bin)
	t.Setenv("GPG_FAKE_SECRET", "owner")
	t.Setenv("GPG_FAKE_PRESENT", "owner")
	t.Setenv("GPG_FAKE_ENCRYPTABLE", "owner")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"access", "personal/bank", "--store-dir", root}, &stdout, &stderr, BuildInfo{})
	if code != 0 {
		t.Fatalf("Run access entry = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "writable") {
		t.Fatalf("output missing writable verdict:\n%s", stdout.String())
	}
}

func TestRunTrustPlanJSON(t *testing.T) {
	root := t.TempDir()
	writeGPGIDFile(t, filepath.Join(root, "work"), "owner", "carol")
	bin := t.TempDir()
	makeScript(t, bin, "gpg", fakeGPGScript)
	t.Setenv("PATH", bin)
	t.Setenv("GPG_FAKE_SECRET", "owner")
	t.Setenv("GPG_FAKE_PRESENT", "owner carol")
	t.Setenv("GPG_FAKE_ENCRYPTABLE", "owner")

	var stdout, stderr bytes.Buffer
	// --json prints the plan only and must never mutate, so no confirm is needed.
	code := Run([]string{"trust", "work", "--json", "--store-dir", root}, &stdout, &stderr, BuildInfo{})
	if code != 0 {
		t.Fatalf("Run trust --json = %d; stderr=%s", code, stderr.String())
	}
	var got struct {
		SchemaVersion int    `json:"schema_version"`
		Strength      string `json:"strength"`
		Recipients    []struct {
			Token  string `json:"token"`
			Action string `json:"action"`
		} `json:"recipients"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if got.SchemaVersion != 1 || got.Strength != "lsign-only" {
		t.Fatalf("schema/strength = %d/%q", got.SchemaVersion, got.Strength)
	}
	actions := map[string]string{}
	for _, r := range got.Recipients {
		actions[r.Token] = r.Action
	}
	if actions["owner"] != "owned-skip" {
		t.Errorf("owner = %q, want owned-skip", actions["owner"])
	}
	if actions["carol"] != "would-lsign" {
		t.Errorf("carol = %q, want would-lsign", actions["carol"])
	}
}

func TestRunGenerateWritable(t *testing.T) {
	root := t.TempDir()
	writeGPGIDFile(t, root, "owner")
	bin := t.TempDir()
	makeScript(t, bin, "gpg", fakeGPGScript)
	pass := makeScript2(t, bin, "pass", `if [ "$1" = generate ]; then printf 'pw-line\nGenSecret\n'; exit 0; fi; exit 9`)
	t.Setenv("PATH", bin)
	t.Setenv("PASSAGE_PASS_BINARY", pass)
	t.Setenv("GPG_FAKE_SECRET", "owner")
	t.Setenv("GPG_FAKE_PRESENT", "owner")
	t.Setenv("GPG_FAKE_ENCRYPTABLE", "owner")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"generate", "work/new", "16", "--no-copy", "--store-dir", root, "--state-dir", t.TempDir()}, &stdout, &stderr, BuildInfo{})
	if code != 0 {
		t.Fatalf("Run generate = %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Generated work/new") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunInsertReadOnlyRefusedBeforeExec(t *testing.T) {
	root := t.TempDir()
	writeGPGIDFile(t, filepath.Join(root, "secure"), "owner", "carol")
	bin := t.TempDir()
	makeScript(t, bin, "gpg", fakeGPGScript)
	sentinel := filepath.Join(bin, "pass-was-called")
	pass := makeScript2(t, bin, "pass", `echo called > "$PASS_SENTINEL"; exit 0`)
	t.Setenv("PATH", bin)
	t.Setenv("PASSAGE_PASS_BINARY", pass)
	t.Setenv("PASS_SENTINEL", sentinel)
	t.Setenv("GPG_FAKE_SECRET", "owner")
	t.Setenv("GPG_FAKE_PRESENT", "owner carol")
	t.Setenv("GPG_FAKE_ENCRYPTABLE", "owner") // carol not encryptable -> read-only

	var stdout, stderr bytes.Buffer
	code := Run([]string{"insert", "secure/x", "--store-dir", root, "--state-dir", t.TempDir()}, &stdout, &stderr, BuildInfo{})
	if code != 1 {
		t.Fatalf("Run insert = %d, want 1; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "read-only") {
		t.Fatalf("stderr = %q, want read-only refusal", stderr.String())
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("pass was invoked despite read-only pre-flight; it must refuse before exec")
	}
}

// makeScript2 mirrors makeScript but returns the script path (for PASSAGE_PASS_BINARY).
func makeScript2(t *testing.T, dir, name, body string) string {
	t.Helper()
	makeScript(t, dir, name, body)
	return filepath.Join(dir, name)
}

// TestInteractiveNewRefusesOverwrite guards against the in-TUI new/generate
// composer silently clobbering an existing entry (the CLI verbs guard via
// --force, but the composer has no such flag, so the handler must refuse).
func TestInteractiveNewRefusesOverwrite(t *testing.T) {
	store := fakeStore(t) // contains work/github
	bin := t.TempDir()
	sentinel := filepath.Join(bin, "pass-called")
	pass := makeScript2(t, bin, "pass", `echo called > "$PASS_SENTINEL"; exit 0`)
	t.Setenv("PASSAGE_PASS_BINARY", pass)
	t.Setenv("PASS_SENTINEL", sentinel)

	r := runner{stdout: io.Discard, stderr: io.Discard, env: os.Environ(), build: BuildInfo{}.normalized()}
	flags := commonFlags{storeDir: store, stateDir: t.TempDir()}
	rt, err := r.load(flags)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := r.runInteractiveActionOnce(t.Context(), &rt, flags, ui.ActionRequest{
		Action:  ui.ActionNew,
		NewPath: "work/github", // already exists
		Content: []byte("hijack"),
	})
	if out.Err == nil || !strings.Contains(out.Err.Error(), "already exists") {
		t.Fatalf("ActionNew over existing entry: err = %v, want 'already exists'", out.Err)
	}
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatal("pass was invoked — the existing entry would have been overwritten")
	}
}

func TestDirBrowseEntriesListsSubdirs(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"work", "personal", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "key.asc"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries := dirBrowseEntries(root)
	if entries[0].Kind != "use" || entries[1].Kind != "up" {
		t.Fatalf("first rows = %q,%q, want use,up", entries[0].Kind, entries[1].Kind)
	}
	kindByTitle := map[string]string{}
	for _, e := range entries {
		kindByTitle[e.Title] = e.Kind
	}
	if kindByTitle["personal/"] != "dir" || kindByTitle["work/"] != "dir" {
		t.Fatalf("subdirs missing: %#v", kindByTitle)
	}
	// The key file is shown for reference, as a non-selectable "file" row.
	if kindByTitle["key.asc"] != "file" {
		t.Fatalf("key file not listed as a file row: %#v", kindByTitle)
	}
	if _, ok := kindByTitle[".hidden/"]; ok {
		t.Fatalf("listed a hidden dir: %#v", kindByTitle)
	}
	// directories come before files, each sorted (personal before work).
	var order []string
	for _, e := range entries {
		if e.Kind == "dir" || e.Kind == "file" {
			order = append(order, e.Kind+":"+e.Title)
		}
	}
	want := []string{"dir:personal/", "dir:work/", "file:key.asc"}
	if len(order) != len(want) {
		t.Fatalf("order = %#v, want %#v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q", i, order[i], want[i])
		}
	}
}

func writeGPGIDFile(t *testing.T, dir string, ids ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gpg-id"), []byte(strings.Join(ids, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write .gpg-id: %v", err)
	}
}

func fakeStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{"work/github.gpg", "work/github/mfa.gpg", "alpha.gpg"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return root
}

func makeScript(t *testing.T, dir string, name string, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	data := []byte("#!/bin/sh\n" + body + "\n")
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
}

// TestThemeExportImportRoundTrip drives Phase 5 through the CLI: export the
// active theme to a portable .theme file, then import it into a fresh config,
// asserting the base + a role survive the round-trip.
func TestThemeExportImportRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.conf")
	if err := os.WriteFile(src, []byte("theme = vivid\nprimary = red\n"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	exported := filepath.Join(dir, "out.theme")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"theme", "export", "--theme-file", src, exported}, &stdout, &stderr, BuildInfo{Version: "test"}); code != 0 {
		t.Fatalf("export = %d; stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(exported)
	if err != nil {
		t.Fatalf("read exported: %v", err)
	}
	out := string(data)
	for _, want := range []string{"# termtheme v1", "theme = vivid", "primary = red"} {
		if !strings.Contains(out, want) {
			t.Fatalf("export missing %q:\n%s", want, out)
		}
	}

	// Import into a fresh config file.
	dest := filepath.Join(dir, "dest.conf")
	stderr.Reset()
	if code := Run([]string{"theme", "import", "--theme-file", dest, exported}, &stdout, &stderr, BuildInfo{}); code != 0 {
		t.Fatalf("import = %d; stderr=%s", code, stderr.String())
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	for _, want := range []string{"theme = vivid", "primary = red"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("imported config missing %q:\n%s", want, got)
		}
	}
}

// ssherpaThemeFixture is a theme exported by ssherpa: 15 roles, no selected_bar
// (ssherpa paints no selection bar).
const ssherpaThemeFixture = `# termtheme v1
# source = ssherpa 0.4.0
format = 1
theme = vivid
primary = 1;38;2;96;221;255
danger = 1;38;2;255;151;112
`

// TestThemeImportFromSsherpaFillsMissingRole is the reverse cross-app contract:
// passage imports an ssherpa .theme that omits selected_bar; the import
// succeeds and passage fills the missing role from its own builtin base
// (fail-open), so the role passage paints is never blank.
func TestThemeImportFromSsherpaFillsMissingRole(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "ssherpa.theme")
	if err := os.WriteFile(src, []byte(ssherpaThemeFixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	dest := filepath.Join(dir, "theme.conf")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"theme", "import", "--theme-file", dest, src}, &stdout, &stderr, BuildInfo{}); code != 0 {
		t.Fatalf("import = %d; stderr=%s", code, stderr.String())
	}
	// The written config carries the base + the roles ssherpa exported, and no
	// selected_bar line (it was absent).
	got, _ := os.ReadFile(dest)
	if !strings.Contains(string(got), "theme = vivid") {
		t.Fatalf("imported config missing base:\n%s", got)
	}
	// passage still resolves selected_bar from its own vivid base (fail-open).
	theme, err := termstyle.ResolveTheme(termstyle.ThemeOptions{File: dest, Env: []string{}, SkipDefaultFile: true})
	if err != nil {
		t.Fatalf("resolve imported: %v", err)
	}
	if got := theme.Style(termstyle.RoleSelectedBar, "x"); got == "x" {
		t.Fatalf("selected_bar rendered plain; should be filled from passage's vivid base")
	}
}
