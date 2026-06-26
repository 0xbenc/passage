package gpgdiag

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGPG writes a stub `gpg` driven by env vars so the verdict engine can be
// tested without a real keyring:
//
//	GPG_FAKE_SECRET       ids we hold a secret key for (--list-secret-keys ok)
//	GPG_FAKE_PRESENT      ids present in the keyring (--list-keys ok)
//	GPG_FAKE_ENCRYPTABLE  ids that --encrypt --recipient succeeds for
//	GPG_FAKE_VALIDITY     "id=char" pairs giving the pub-line validity field
func fakeGPG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gpg")
	script := `#!/bin/sh
contains() { case " $2 " in *" $1 "*) return 0;; esac; return 1; }
validity_of() {
  for pair in $GPG_FAKE_VALIDITY; do
    key=${pair%%=*}; val=${pair#*=}
    if [ "$key" = "$1" ]; then printf '%s' "$val"; return; fi
  done
  printf '%s' '-'
}
mode=""; listid=""; recipients=""; colons=0
while [ $# -gt 0 ]; do
  case "$1" in
    --list-secret-keys) mode=secret; shift; listid="$1"; shift ;;
    --list-keys) mode=listkeys; shift
      if [ $# -gt 0 ]; then case "$1" in --*) ;; *) listid="$1"; shift ;; esac; fi ;;
    --encrypt) mode=encrypt; shift ;;
    --recipient) shift; recipients="$recipients $1"; shift ;;
    --with-colons) colons=1; shift ;;
    --output) shift; shift ;;
    --export-ownertrust) mode=ownertrust; shift ;;
    *) shift ;;
  esac
done
case "$mode" in
  secret) contains "$listid" "$GPG_FAKE_SECRET" && exit 0; exit 2 ;;
  encrypt)
    for r in $recipients; do contains "$r" "$GPG_FAKE_ENCRYPTABLE" || exit 2; done
    exit 0 ;;
  ownertrust) exit 0 ;;
  listkeys)
    if [ -n "$listid" ]; then
      if contains "$listid" "$GPG_FAKE_PRESENT" || contains "$listid" "$GPG_FAKE_SECRET"; then
        v=$(validity_of "$listid")
        printf 'pub:%s:\nfpr:::::::::%s:\nuid:%s:::::::::%s:\n' "$v" "$listid" "$v" "$listid"
        exit 0
      fi
      exit 2
    fi
    exit 0 ;;
esac
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake gpg: %v", err)
	}
	return path
}

func writeGPGID(t *testing.T, dir string, ids ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gpg-id"), []byte(strings.Join(ids, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write .gpg-id: %v", err)
	}
}

func scopeByLabel(scopes []ScopeReport, label string) (ScopeReport, bool) {
	for _, s := range scopes {
		if s.Label == label {
			return s, true
		}
	}
	return ScopeReport{}, false
}

func TestVerdictClassification(t *testing.T) {
	root := t.TempDir()
	writeGPGID(t, root, "owner")                                    // owned-only      -> writable
	writeGPGID(t, filepath.Join(root, "team"), "owner", "alice")    // owned+encryptable -> writable
	writeGPGID(t, filepath.Join(root, "secure"), "owner", "carol")  // owned+invalid -> read-only (trust)
	writeGPGID(t, filepath.Join(root, "expired"), "owner", "frank") // owned+unusable -> read-only (unfixable)
	writeGPGID(t, filepath.Join(root, "offsite"), "alice", "dave")  // encryptable+missing, none owned -> no_access (import)

	t.Setenv("GPG_FAKE_SECRET", "owner")
	t.Setenv("GPG_FAKE_PRESENT", "owner alice carol frank")
	t.Setenv("GPG_FAKE_ENCRYPTABLE", "owner alice")
	t.Setenv("GPG_FAKE_VALIDITY", "carol=- frank=e")

	c := Checker{GPGBinary: fakeGPG(t), StoreRoot: root}
	report, err := c.AccessAll(context.Background())
	if err != nil {
		t.Fatalf("AccessAll: %v", err)
	}

	cases := []struct {
		label   string
		verdict Verdict
		fixable string
	}{
		{"default", VerdictWritable, ""},
		{"team", VerdictWritable, ""},
		{"secure", VerdictReadOnly, "trust"},
		{"expired", VerdictReadOnly, "unfixable"},
		{"offsite", VerdictNoAccess, "import"},
	}
	for _, tc := range cases {
		s, ok := scopeByLabel(report.Scopes, tc.label)
		if !ok {
			t.Fatalf("scope %q missing from %#v", tc.label, report.Scopes)
		}
		if s.Verdict != tc.verdict {
			t.Errorf("%s verdict = %s, want %s", tc.label, s.Verdict, tc.verdict)
		}
		if s.Fixable != tc.fixable {
			t.Errorf("%s fixable = %q, want %q", tc.label, s.Fixable, tc.fixable)
		}
	}

	secure, _ := scopeByLabel(report.Scopes, "secure")
	if !equalStrings(secure.Invalid, []string{"carol"}) {
		t.Errorf("secure.Invalid = %#v", secure.Invalid)
	}
	expired, _ := scopeByLabel(report.Scopes, "expired")
	if !equalStrings(expired.Unusable, []string{"frank"}) {
		t.Errorf("expired.Unusable = %#v", expired.Unusable)
	}
	offsite, _ := scopeByLabel(report.Scopes, "offsite")
	if !equalStrings(offsite.Missing, []string{"dave"}) {
		t.Errorf("offsite.Missing = %#v", offsite.Missing)
	}
}

func TestAccessNearestAncestor(t *testing.T) {
	root := t.TempDir()
	writeGPGID(t, root, "owner")
	writeGPGID(t, filepath.Join(root, "work", "aws"), "owner", "carol")
	t.Setenv("GPG_FAKE_SECRET", "owner")
	t.Setenv("GPG_FAKE_PRESENT", "owner carol")
	t.Setenv("GPG_FAKE_ENCRYPTABLE", "owner")
	t.Setenv("GPG_FAKE_VALIDITY", "carol=-")

	c := Checker{GPGBinary: fakeGPG(t), StoreRoot: root}
	// An entry deep under work/aws resolves to that scope (read-only via carol).
	scope, err := c.Access(context.Background(), "work/aws/prod/db")
	if err != nil {
		t.Fatalf("Access: %v", err)
	}
	if scope.Scope != "work/aws" || scope.Verdict != VerdictReadOnly {
		t.Fatalf("Access scope=%q verdict=%s, want work/aws read_only", scope.Scope, scope.Verdict)
	}
	// An entry under root inherits the writable root scope.
	rootScope, err := c.Access(context.Background(), "personal/bank")
	if err != nil {
		t.Fatalf("Access root: %v", err)
	}
	if rootScope.Scope != "" || rootScope.Verdict != VerdictWritable {
		t.Fatalf("root inherit scope=%q verdict=%s, want writable", rootScope.Scope, rootScope.Verdict)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRealGPGProbeMatrix reproduces the load-bearing empirical result against a
// real gpg in a throwaway keyring: lsign-only flips read-only -> writable, while
// ownertrust alone does not. Skipped when gpg is unavailable.
func TestRealGPGProbeMatrix(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Setenv("GNUPGHOME", home)
	gen := func(home, uid string) {
		cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--pinentry-mode", "loopback",
			"--passphrase", "", "--quick-generate-key", uid, "default", "default", "0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generate %s: %v: %s", uid, err, out)
		}
	}
	fpr := func(home, uid string) string {
		out, err := exec.Command(gpgBin, "--homedir", home, "--with-colons", "--list-keys", uid).Output()
		if err != nil {
			t.Fatalf("list %s: %v", uid, err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "fpr:") {
				return strings.Split(line, ":")[9]
			}
		}
		t.Fatalf("no fpr for %s", uid)
		return ""
	}

	gen(home, "Owner <owner@test>")
	ownFpr := fpr(home, "owner@test")

	// A recipient we do not own: mint elsewhere, import public-only.
	recipHome := t.TempDir()
	_ = os.Chmod(recipHome, 0o700)
	gen(recipHome, "Alice <alice@corp>")
	aliceFpr := fpr(recipHome, "alice@corp")
	pub := filepath.Join(t.TempDir(), "alice.pub")
	out, err := exec.Command(gpgBin, "--homedir", recipHome, "--armor", "--output", pub, "--export", "alice@corp").CombinedOutput()
	if err != nil {
		t.Fatalf("export alice: %v: %s", err, out)
	}
	if out, err := exec.Command(gpgBin, "--homedir", home, "--import", pub).CombinedOutput(); err != nil {
		t.Fatalf("import alice: %v: %s", err, out)
	}

	root := t.TempDir()
	writeGPGID(t, filepath.Join(root, "shared"), ownFpr, aliceFpr)
	c := Checker{GPGBinary: gpgBin, StoreRoot: root}

	before, err := c.Access(context.Background(), "shared/x")
	if err != nil {
		t.Fatalf("Access before: %v", err)
	}
	if before.Verdict != VerdictReadOnly {
		t.Fatalf("before lsign verdict = %s, want read_only (alice untrusted)", before.Verdict)
	}

	// ownertrust alone must NOT make it writable.
	trust := aliceFpr + ":4:\n"
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--yes", "--import-ownertrust")
	cmd.Stdin = strings.NewReader(trust)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("import-ownertrust: %v: %s", err, out)
	}
	mid, _ := c.Access(context.Background(), "shared/x")
	if mid.Verdict != VerdictReadOnly {
		t.Fatalf("ownertrust=4 wrongly flipped verdict to %s; must stay read_only", mid.Verdict)
	}

	// lsign-only flips it to writable.
	if out, err := exec.Command(gpgBin, "--homedir", home, "--batch", "--yes", "--pinentry-mode", "loopback",
		"--passphrase", "", "--quick-lsign-key", aliceFpr).CombinedOutput(); err != nil {
		t.Fatalf("quick-lsign-key: %v: %s", err, out)
	}
	after, err := c.Access(context.Background(), "shared/x")
	if err != nil {
		t.Fatalf("Access after: %v", err)
	}
	if after.Verdict != VerdictWritable {
		t.Fatalf("after lsign verdict = %s, want writable", after.Verdict)
	}
}
