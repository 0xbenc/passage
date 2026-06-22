package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
