package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/0xbenc/passage/internal/passstore"
	"github.com/0xbenc/passage/internal/termstyle"
	"github.com/0xbenc/passage/internal/totp"
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
	// The auto-run screen shows how long the copied code stays valid.
	if !regexp.MustCompile(`\d+s remaining`).MatchString(stderr.String()) {
		t.Fatalf("stderr = %q, want a remaining-time note", stderr.String())
	}
	data, err := os.ReadFile(clipOut)
	if err != nil {
		t.Fatalf("ReadFile: %v (the picker likely opened instead of auto-running TOTP); stderr=%s", err, stderr.String())
	}
	if string(data) != shown {
		t.Fatalf("clipboard = %q, shown = %q", data, shown)
	}
}

// TestRunMFAFilterSingleMatchWaitsForFreshWindow pins the sub-5s behavior: the
// auto-run must never hand over a code that expires in <5s — it waits for the
// next window (showing a refresh line on a piped stderr) and copies the fresh
// code. The fake pass serves a fixed secret, so the shown code is whatever the
// real clock produces; when the wall clock lands in the last 4 seconds of a
// window this test takes up to ~4s, by design.
func TestRunMFAFilterSingleMatchWaitsForFreshWindow(t *testing.T) {
	store := fakeStore(t)
	bin := t.TempDir()
	clipOut := filepath.Join(bin, "clip.txt")
	makeScript(t, bin, "pass", `if [ "$PASSWORD_STORE_DIR" != "$STORE_ROOT" ]; then echo "bad store: $PASSWORD_STORE_DIR" >&2; exit 8; fi
if [ "$1" = show ] && [ "$2" = -- ] && [ "$3" = work/github/mfa ]; then printf 'JBSWY3DPEHPK3PXP\n'; exit 0; fi; exit 9`)
	makeScript(t, bin, "pbcopy", `/bin/cat > "$CLIP_OUT"`)
	t.Setenv("PATH", bin)
	t.Setenv("PASSAGE_PASS_BINARY", filepath.Join(bin, "pass"))
	t.Setenv("CLIP_OUT", clipOut)
	t.Setenv("STORE_ROOT", store)
	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := Run([]string{"mfa", "mfa", "--store-dir", store, "--state-dir", t.TempDir(), "--no-color"}, &stdout, &stderr, BuildInfo{})
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("Run = %d, stderr=%s", code, stderr.String())
	}
	shown := strings.TrimSpace(stdout.String())
	if len(shown) != 6 {
		t.Fatalf("TOTP shown onscreen = %q, want 6 digits", shown)
	}
	if strings.Contains(stderr.String(), "waiting for a fresh TOTP code") {
		// The wait fired. Its length is whatever was left of the window —
		// as little as 1s — so there is no floor to assert here; what must
		// hold is that the code handed over afterwards is a fresh one.
		// (waitForTOTPRefreshClock's fake-clock tests cover the loop.)
		if !regexp.MustCompile(`(2[5-9]|30)s remaining`).MatchString(stderr.String()) {
			t.Fatalf("stderr = %q, want a fresh (>=25s) remaining note after the wait", stderr.String())
		}
		if elapsed > time.Duration(totpStaleSeconds+2)*time.Second {
			t.Fatalf("waited %s; the wait is bounded by the %ds staleness window", elapsed, totpStaleSeconds)
		}
	}
	data, err := os.ReadFile(clipOut)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != shown {
		t.Fatalf("clipboard = %q, shown = %q", data, shown)
	}
}

func TestShouldWaitForTOTPRefresh(t *testing.T) {
	cases := []struct {
		name string
		opts totpOptions
		code totp.Code
		want bool
	}{
		{name: "no wait flag", opts: totpOptions{}, code: totp.Code{Remaining: 2}, want: false},
		{name: "wait, stale", opts: totpOptions{wait: true}, code: totp.Code{Remaining: 4}, want: true},
		{name: "wait, at threshold", opts: totpOptions{wait: true}, code: totp.Code{Remaining: 5}, want: true},
		{name: "wait, just past threshold", opts: totpOptions{wait: true}, code: totp.Code{Remaining: 6}, want: false},
		{name: "wait, almost expired", opts: totpOptions{wait: true}, code: totp.Code{Remaining: 1}, want: true},
		{name: "wait, fresh", opts: totpOptions{wait: true}, code: totp.Code{Remaining: 30}, want: false},
		{name: "wait, fixed --at time", opts: totpOptions{wait: true, at: time.Unix(59, 0)}, code: totp.Code{Remaining: 2}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldWaitForTOTPRefresh(tc.opts, tc.code); got != tc.want {
				t.Fatalf("shouldWaitForTOTPRefresh = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTotpRemainingNote(t *testing.T) {
	cases := []struct {
		msg       string
		remaining int
		want      string
	}{
		{msg: "TOTP copied to clipboard (pbcopy).", remaining: 23, want: "TOTP copied to clipboard (pbcopy). 23s remaining."},
		{msg: "Clipboard copy failed: no tool", remaining: 4, want: "Clipboard copy failed: no tool. 4s remaining."},
		{msg: "", remaining: 29, want: "29s remaining."},
	}
	for _, tc := range cases {
		if got := totpRemainingNote(tc.msg, tc.remaining); got != tc.want {
			t.Fatalf("totpRemainingNote(%q,%d) = %q, want %q", tc.msg, tc.remaining, got, tc.want)
		}
	}
}

func TestTotpRefreshFrameCountsDown(t *testing.T) {
	asc := termstyle.ASCIIGlyphs()
	if got := totpRefreshFrame(asc, 0, 4*time.Second); got != "| waiting for a fresh TOTP code (refreshes in 4s)" {
		t.Fatalf("ascii frame = %q", got)
	}
	if got := totpRefreshFrame(asc, 1, 1500*time.Millisecond); got != "/ waiting for a fresh TOTP code (refreshes in 2s)" {
		t.Fatalf("ascii frame ceil = %q", got)
	}
	uni := termstyle.UnicodeGlyphs()
	if got := totpRefreshFrame(uni, 0, 3*time.Second); got != uni.Spinner[0]+" waiting for a fresh TOTP code (refreshes in 3s)" {
		t.Fatalf("unicode frame = %q", got)
	}
}

func TestSpinnerLineRewritesAndFinishes(t *testing.T) {
	var buf bytes.Buffer
	s := &spinnerLine{w: &buf}
	s.rewrite("abcd")
	s.rewrite("ab") // shorter frame must pad, not truncate the previous tail
	s.finish()
	want := "\rabcd\rab  \r  \n"
	if buf.String() != want {
		t.Fatalf("spinner bytes = %q, want %q", buf.String(), want)
	}
	s.finish() // idempotent: no second clear line
	if buf.String() != want {
		t.Fatalf("second finish changed output: %q", buf.String())
	}
}

func TestSpinnerLinePadsWideGlyphs(t *testing.T) {
	g := termstyle.UnicodeGlyphs()
	var buf bytes.Buffer
	s := &spinnerLine{w: &buf}
	s.rewrite(g.Spinner[0] + " abcd") // braille frame is one cell wide
	s.rewrite(g.Spinner[1] + " ab")
	s.finish()
	want := "\r" + g.Spinner[0] + " abcd\r" + g.Spinner[1] + " ab  \r    \n"
	if buf.String() != want {
		t.Fatalf("spinner bytes = %q, want %q", buf.String(), want)
	}
}

// TestWaitForTOTPRefreshWaitsToBoundary drives the wait loop with a fake
// clock: it must keep polling until the boundary passes, printing the
// piped-stderr line exactly once.
func TestWaitForTOTPRefreshWaitsToBoundary(t *testing.T) {
	var stderr bytes.Buffer
	r := runner{stderr: &stderr, env: os.Environ(), build: BuildInfo{}.normalized()}
	start := time.Unix(1_000_000, 0)
	calls := 0
	now := func() time.Time {
		calls++
		if calls == 1 {
			return start
		}
		return start.Add(4 * time.Second) // the boundary
	}
	code := totp.Code{Remaining: 4, ValidUntil: start.Unix() + 4}
	if err := r.waitForTOTPRefreshClock(context.Background(), code, now, time.Millisecond); err != nil {
		t.Fatalf("waitForTOTPRefreshClock = %v", err)
	}
	if got := strings.Count(stderr.String(), "waiting for a fresh TOTP code (refreshes in 4s)"); got != 1 {
		t.Fatalf("waiting line printed %d times:\n%s", got, stderr.String())
	}
	if calls < 2 {
		t.Fatalf("clock polled %d times, want >=2 (must poll past the boundary)", calls)
	}
}

// TestWaitForTOTPRefreshHonorsContext pins that a canceled context stops the
// wait instead of sleeping out the full window.
func TestWaitForTOTPRefreshHonorsContext(t *testing.T) {
	var stderr bytes.Buffer
	r := runner{stderr: &stderr, env: os.Environ(), build: BuildInfo{}.normalized()}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	code := totp.Code{Remaining: 30, ValidUntil: time.Now().Unix() + 30}
	start := time.Now()
	err := r.waitForTOTPRefreshClock(ctx, code, time.Now, time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("waited %s; must stop at context cancel", elapsed)
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
	argvLog := filepath.Join(t.TempDir(), "pass-argv.log")
	pass := makeScript2(t, bin, "pass", `echo "$@" >> `+argvLog+`; if [ "$1" = generate ]; then printf 'pw-line\nGenSecret\n'; exit 0; fi; exit 9`)
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
	// The LENGTH argument must reach pass; a dropped length silently falls
	// back to pass's default and no other assertion would notice.
	argv, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("pass was never invoked: %v", err)
	}
	if want := "generate --force -- work/new 16"; !strings.Contains(string(argv), want) {
		t.Fatalf("pass argv = %q, want it to contain %q", argv, want)
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

// TestInteractiveActionErrorNamesActualTimeout pins item 2 of the known-issues
// brief: the timeout message must name the deadline actually used for the
// action — 60s for a write verb, not the 12s read timeout.
func TestInteractiveActionErrorNamesActualTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("ctx.Err() = %v, want deadline exceeded", ctx.Err())
	}

	writeErr := interactiveActionError(ctx, ui.ActionRequest{
		Action: ui.ActionRemove,
		Entry:  passstore.Entry{Path: "work/github"},
	}, writeActionTimeout, errors.New("ignored"))
	if want := "remove work/github timed out after 1m0s; try `pass show -- work/github` once outside passage to unlock or diagnose GPG"; writeErr.Error() != want {
		t.Fatalf("write timeout = %q, want %q", writeErr, want)
	}

	readErr := interactiveActionError(ctx, ui.ActionRequest{
		Action: ui.ActionCopy,
		Entry:  passstore.Entry{Path: "work/github"},
	}, interactiveActionTimeout, errors.New("ignored"))
	if want := "copy work/github timed out after 12s; try `pass show -- work/github` once outside passage to unlock or diagnose GPG"; readErr.Error() != want {
		t.Fatalf("read timeout = %q, want %q", readErr, want)
	}

	globalErr := interactiveActionError(ctx, ui.ActionRequest{Action: ui.ActionKeys}, writeActionTimeout, errors.New("ignored"))
	if want := "keys timed out after 1m0s"; globalErr.Error() != want {
		t.Fatalf("global timeout = %q, want %q", globalErr, want)
	}
}

// TestRunMissingEntryExitsTwo pins item 7 of the known-issues brief: not-found
// is exit 2 on every entry verb (the documented contract), including
// copy/reveal/totp, which used to report 1.
func TestRunMissingEntryExitsTwo(t *testing.T) {
	store := fakeStore(t)
	for _, verb := range []string{"copy", "reveal", "totp"} {
		t.Run(verb, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run([]string{verb, "missing/entry", "--store-dir", store, "--state-dir", t.TempDir()}, &stdout, &stderr, BuildInfo{})
			if code != 2 {
				t.Fatalf("Run %s missing/entry = %d, want 2; stderr=%s", verb, code, stderr.String())
			}
			if !strings.Contains(stderr.String(), `entry "missing/entry" not found`) {
				t.Fatalf("stderr = %q, want not-found message", stderr.String())
			}
		})
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

// TestSaveThemeConfigMessageNamesDroppedRoles pins item 4 of the known-issues
// brief end-to-end: the save result message the user sees names the roles that
// were dropped for failing to parse, while the rest of the config is written.
func TestSaveThemeConfigMessageNamesDroppedRoles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.conf")
	r := runner{stdout: io.Discard, stderr: io.Discard, env: os.Environ(), build: BuildInfo{}.normalized()}
	saved, err := r.saveThemeConfig(t.Context(), commonFlags{themeFile: path}, ui.ThemeEditorResult{
		Config: termstyle.ThemeConfig{
			Specs: map[termstyle.Role]string{termstyle.RoleWarning: "bold red"},
		},
		Path:         path,
		DroppedRoles: []string{`primary (unknown style token "bogustoken")`},
	})
	if err != nil {
		t.Fatalf("saveThemeConfig: %v", err)
	}
	if !saved.Changed {
		t.Fatalf("save should report a change, got: %#v", saved)
	}
	for _, want := range []string{
		"Theme saved to",
		`Dropped invalid roles: primary (unknown style token "bogustoken").`,
	} {
		if !strings.Contains(saved.Message, want) {
			t.Fatalf("message = %q, want it to contain %q", saved.Message, want)
		}
	}
	// The written file keeps the valid role and omits the dropped one.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "warning = bold red") {
		t.Fatalf("saved file missing the valid role:\n%s", data)
	}
	if strings.Contains(string(data), "bogustoken") {
		t.Fatalf("saved file keeps the unparseable spec:\n%s", data)
	}
}

// TestIntroDecision is the truth table for the pure intro gate (no TTY check):
// flag/env precedence first, then the once-per-version default. shouldPlayIntro
// only layers the TTY requirement on top, which is why the decision is factored
// out here.
func TestIntroDecision(t *testing.T) {
	cases := []struct {
		name        string
		flags       commonFlags
		env         []string
		lastVersion string
		build       string
		want        bool
	}{
		{name: "same version, no flags => skip", lastVersion: "1.0.0", build: "1.0.0", want: false},
		{name: "new version => play", lastVersion: "0.9.0", build: "1.0.0", want: true},
		{name: "never seen => play", lastVersion: "", build: "1.0.0", want: true},
		{name: "dev unchanged => skip", lastVersion: "dev", build: "dev", want: false},
		{name: "--intro forces on same version", flags: commonFlags{intro: true}, lastVersion: "1.0.0", build: "1.0.0", want: true},
		{name: "--no-intro suppresses new version", flags: commonFlags{noIntro: true}, lastVersion: "0.9.0", build: "1.0.0", want: false},
		{name: "--no-intro beats --intro", flags: commonFlags{noIntro: true, intro: true}, lastVersion: "0.9.0", build: "1.0.0", want: false},
		{name: "PASSAGE_INTRO_ALWAYS forces on same version", env: []string{"PASSAGE_INTRO_ALWAYS=1"}, lastVersion: "1.0.0", build: "1.0.0", want: true},
		{name: "PASSAGE_NO_INTRO suppresses new version", env: []string{"PASSAGE_NO_INTRO=true"}, lastVersion: "0.9.0", build: "1.0.0", want: false},
		{name: "PASSAGE_NO_INTRO beats PASSAGE_INTRO_ALWAYS", env: []string{"PASSAGE_NO_INTRO=1", "PASSAGE_INTRO_ALWAYS=1"}, lastVersion: "0.9.0", build: "1.0.0", want: false},
		{name: "falsy PASSAGE_NO_INTRO is ignored", env: []string{"PASSAGE_NO_INTRO=0"}, lastVersion: "0.9.0", build: "1.0.0", want: true},
		{name: "--no-intro beats PASSAGE_INTRO_ALWAYS", flags: commonFlags{noIntro: true}, env: []string{"PASSAGE_INTRO_ALWAYS=1"}, lastVersion: "1.0.0", build: "1.0.0", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := introDecision(tc.flags, tc.env, tc.lastVersion, tc.build); got != tc.want {
				t.Fatalf("introDecision = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIntroVersionLabel pins the bottom-road-band label formatting.
func TestIntroVersionLabel(t *testing.T) {
	cases := map[string]string{
		"":      "dev",
		"dev":   "dev",
		"1.2.3": "v1.2.3",
		"2.0":   "v2.0",
	}
	for in, want := range cases {
		if got := introVersionLabel(in); got != want {
			t.Fatalf("introVersionLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGenerateTOTPMessageHasNoRemainingNote pins where the remaining-time note
// is applied. generateTOTP feeds three callers, two of which render a live
// countdown (the picker modal's SecretRemaining and `passage totp`'s Reveal);
// a frozen "23s remaining" glued to their message would contradict the ticking
// one a second later. Only runAutoAction, which has no countdown on screen,
// adds the note.
func TestGenerateTOTPMessageHasNoRemainingNote(t *testing.T) {
	store := fakeStore(t)
	bin := t.TempDir()
	clipOut := filepath.Join(bin, "clip.txt")
	makeScript(t, bin, "pass", `if [ "$1" = show ] && [ "$2" = -- ] && [ "$3" = work/github/mfa ]; then printf 'JBSWY3DPEHPK3PXP\n'; exit 0; fi; exit 9`)
	makeScript(t, bin, "pbcopy", `/bin/cat > "$CLIP_OUT"`)
	t.Setenv("PATH", bin)
	t.Setenv("PASSAGE_PASS_BINARY", filepath.Join(bin, "pass"))
	t.Setenv("CLIP_OUT", clipOut)

	var stdout, stderr bytes.Buffer
	r := runner{stdout: &stdout, stderr: &stderr, env: os.Environ(), build: BuildInfo{}.normalized()}
	flags := commonFlags{storeDir: store, stateDir: t.TempDir()}
	rt, err := r.load(flags)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	code, msg, err := r.generateTOTP(context.Background(), rt, "work/github", totpOptions{copy: true})
	if err != nil {
		t.Fatalf("generateTOTP: %v", err)
	}
	if strings.Contains(msg, "remaining") {
		t.Fatalf("msg = %q, want no remaining-time note (the callers own the countdown)", msg)
	}
	if !strings.HasSuffix(msg, ").") {
		t.Fatalf("msg = %q, want the sibling clipboard-message punctuation", msg)
	}
	if note := totpRemainingNote(msg, code.Remaining); !strings.HasSuffix(note, "s remaining.") {
		t.Fatalf("auto-run note = %q", note)
	}
}
