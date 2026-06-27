package gpgtrust

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fakeGPG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gpg")
	script := `#!/bin/sh
contains() { case " $2 " in *" $1 "*) return 0;; esac; return 1; }
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

func actionOf(plan Plan, token string) Action {
	for _, r := range plan.Recipients {
		if r.Token == token {
			return r.Action
		}
	}
	return ""
}

func TestPlanRecipientsClassification(t *testing.T) {
	root := t.TempDir()
	writeGPGID(t, filepath.Join(root, "work"), "owner", "alice", "carol")
	t.Setenv("GPG_FAKE_SECRET", "owner")
	t.Setenv("GPG_FAKE_PRESENT", "owner alice carol")
	t.Setenv("GPG_FAKE_ENCRYPTABLE", "owner alice")

	tr := Truster{GPGBinary: fakeGPG(t), StoreRoot: root}
	plan, err := tr.PlanRecipients(context.Background(), "work", Lsign)
	if err != nil {
		t.Fatalf("PlanRecipients: %v", err)
	}
	if got := actionOf(plan, "owner"); got != ActionOwnedSkip {
		t.Errorf("owner action = %q, want owned-skip", got)
	}
	if got := actionOf(plan, "alice"); got != ActionAlreadyValid {
		t.Errorf("alice action = %q, want already-valid", got)
	}
	if got := actionOf(plan, "carol"); got != ActionWouldLsign {
		t.Errorf("carol action = %q, want would-lsign", got)
	}
	if !plan.Actionable() {
		t.Error("plan should be actionable (carol needs lsign)")
	}

	// Dry-run Apply must report carol but mutate nothing.
	report, err := tr.Apply(context.Background(), plan, Lsign, true)
	if err != nil {
		t.Fatalf("Apply dry-run: %v", err)
	}
	if !report.DryRun || len(report.Results) != 1 || report.Results[0].Signed {
		t.Fatalf("dry-run report = %#v", report)
	}
}

// TestRealGPGImportSecrets covers the secret-import flow: importing a secret key
// file makes the key yours and ultimate-trusted, while a public-only file is
// imported but left untrusted.
func TestRealGPGImportSecrets(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}
	mint := func(uid string) (secFile, pubFile, fpr string) {
		h := t.TempDir()
		_ = os.Chmod(h, 0o700)
		if out, err := exec.Command(gpgBin, "--homedir", h, "--batch", "--pinentry-mode", "loopback",
			"--passphrase", "", "--quick-generate-key", uid, "default", "default", "0").CombinedOutput(); err != nil {
			t.Skipf("gpg key generation unavailable here (e.g. macOS CI agent/socket limits): %v: %s", err, out)
		}
		dir := t.TempDir()
		secFile = filepath.Join(dir, "sec.asc")
		pubFile = filepath.Join(dir, "pub.asc")
		exec.Command(gpgBin, "--homedir", h, "--batch", "--pinentry-mode", "loopback", "--passphrase", "",
			"--output", secFile, "--export-secret-keys", uid).Run()
		exec.Command(gpgBin, "--homedir", h, "--output", pubFile, "--export", uid).Run()
		out, _ := exec.Command(gpgBin, "--homedir", h, "--with-colons", "--list-keys", uid).Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "fpr:") {
				fpr = strings.Split(line, ":")[9]
				break
			}
		}
		return
	}

	home := t.TempDir()
	_ = os.Chmod(home, 0o700)
	t.Setenv("GNUPGHOME", home)
	mySec, _, myFpr := mint("Me <me@dev>")
	_, theirPub, theirFpr := mint("Them <them@corp>")

	tr := Truster{GPGBinary: gpgBin, StoreRoot: t.TempDir()}

	// Preview distinguishes secret from public.
	plan, err := tr.PlanImportSecrets(context.Background(), []string{mySec, theirPub})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	secret := map[string]bool{}
	for _, k := range plan.Keys {
		secret[k.Fingerprint] = k.HasSecret
	}
	if !secret[myFpr] {
		t.Fatalf("my key not detected as secret: %#v", plan.Keys)
	}
	if secret[theirFpr] {
		t.Fatalf("their public key wrongly flagged as secret")
	}
	if plan.OwnKeyCount() != 1 {
		t.Fatalf("OwnKeyCount = %d, want 1", plan.OwnKeyCount())
	}

	report, err := tr.ApplyImportSecrets(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(report.Trusted) != 1 || report.Trusted[0] != myFpr {
		t.Fatalf("Trusted = %#v, want [%s]", report.Trusted, myFpr)
	}
	trust, _ := exec.Command(gpgBin, "--homedir", home, "--export-ownertrust").Output()
	if !strings.Contains(string(trust), myFpr+":6:") {
		t.Fatalf("my key not ultimate-trusted:\n%s", trust)
	}
	if strings.Contains(string(trust), theirFpr) {
		t.Fatalf("their public-only key was trusted; should be left untrusted:\n%s", trust)
	}
}

// TestRealGPGOwnKeyGetsUltimateTrust covers the new-machine case: a secret key
// imported (not generated) is NOT auto-trusted, so the store reads read-only;
// `passage trust` must set it ultimate (6) — not local-sign it — and never
// downgrade an existing ultimate.
func TestRealGPGOwnKeyGetsUltimateTrust(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}
	home := t.TempDir()
	_ = os.Chmod(home, 0o700)
	t.Setenv("GNUPGHOME", home)

	// Mint a key elsewhere, export the SECRET, import it here (imported secrets
	// are not auto-ultimate, unlike generated ones).
	src := t.TempDir()
	_ = os.Chmod(src, 0o700)
	if out, err := exec.Command(gpgBin, "--homedir", src, "--batch", "--pinentry-mode", "loopback",
		"--passphrase", "", "--quick-generate-key", "Me <me@new>", "default", "default", "0").CombinedOutput(); err != nil {
		t.Skipf("gpg key generation unavailable here (e.g. macOS CI agent/socket limits): %v: %s", err, out)
	}
	secFile := filepath.Join(t.TempDir(), "me.sec")
	if out, err := exec.Command(gpgBin, "--homedir", src, "--batch", "--pinentry-mode", "loopback",
		"--passphrase", "", "--output", secFile, "--export-secret-keys", "me@new").CombinedOutput(); err != nil {
		t.Fatalf("export secret: %v: %s", err, out)
	}
	if out, err := exec.Command(gpgBin, "--homedir", home, "--batch", "--pinentry-mode", "loopback",
		"--passphrase", "", "--import", secFile).CombinedOutput(); err != nil {
		t.Fatalf("import secret: %v: %s", err, out)
	}
	out, _ := exec.Command(gpgBin, "--homedir", home, "--with-colons", "--list-keys", "me@new").Output()
	var ownFpr string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "fpr:") {
			ownFpr = strings.Split(line, ":")[9]
			break
		}
	}
	if ownFpr == "" {
		t.Fatal("no own fingerprint")
	}

	root := t.TempDir()
	writeGPGID(t, filepath.Join(root, "me"), ownFpr)
	tr := Truster{GPGBinary: gpgBin, StoreRoot: root}

	plan, err := tr.PlanRecipients(context.Background(), "me", Lsign)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if got := actionOf(plan, ownFpr); got != ActionWouldOwnTrust {
		t.Fatalf("own untrusted key action = %q, want would-own-trust", got)
	}
	report, err := tr.Apply(context.Background(), plan, Lsign, false)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !report.NowWritable {
		t.Fatalf("scope not writable after own-trust: %#v", report)
	}
	trust, _ := exec.Command(gpgBin, "--homedir", home, "--export-ownertrust").Output()
	if !strings.Contains(string(trust), ownFpr+":6:") {
		t.Fatalf("own key not set to ultimate (6):\n%s", trust)
	}
	// Re-plan: now already valid, nothing to do.
	plan2, _ := tr.PlanRecipients(context.Background(), "me", Lsign)
	if got := actionOf(plan2, ownFpr); got != ActionOwnedSkip {
		t.Fatalf("after trust, action = %q, want owned-skip", got)
	}
}

// TestRealGPGApplyLsignAndNeverDowngrade exercises Apply against a real gpg in a
// throwaway keyring: lsign flips a read-only scope writable; --full sets
// ownertrust=4 on a fresh key but never downgrades an existing ultimate (5).
func TestRealGPGApplyLsignAndNeverDowngrade(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}
	home := t.TempDir()
	_ = os.Chmod(home, 0o700)
	t.Setenv("GNUPGHOME", home)
	run := func(args ...string) string {
		out, err := exec.Command(gpgBin, append([]string{"--homedir", home}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("gpg %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	gen := func(h, uid string) {
		out, err := exec.Command(gpgBin, "--homedir", h, "--batch", "--pinentry-mode", "loopback",
			"--passphrase", "", "--quick-generate-key", uid, "default", "default", "0").CombinedOutput()
		if err != nil {
			t.Skipf("gpg key generation unavailable here (e.g. macOS CI agent/socket limits): %v: %s", err, out)
		}
	}
	fprOf := func(h, uid string) string {
		out, _ := exec.Command(gpgBin, "--homedir", h, "--with-colons", "--list-keys", uid).Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "fpr:") {
				return strings.Split(line, ":")[9]
			}
		}
		t.Fatalf("no fpr for %s", uid)
		return ""
	}
	importPub := func(uid string) string {
		recip := t.TempDir()
		_ = os.Chmod(recip, 0o700)
		gen(recip, uid)
		fpr := fprOf(recip, uid)
		pub := filepath.Join(t.TempDir(), "k.pub")
		if out, err := exec.Command(gpgBin, "--homedir", recip, "--output", pub, "--export", uid).CombinedOutput(); err != nil {
			t.Fatalf("export %s: %v: %s", uid, err, out)
		}
		run("--import", pub)
		return fpr
	}

	gen(home, "Owner <owner@test>")
	ownFpr := fprOf(home, "owner@test")
	aliceFpr := importPub("Alice <alice@corp>")
	bobFpr := importPub("Bob <bob@corp>") // will be pre-trusted ultimate

	root := t.TempDir()
	writeGPGID(t, filepath.Join(root, "shared"), ownFpr, aliceFpr)
	writeGPGID(t, filepath.Join(root, "vip"), ownFpr, bobFpr)
	tr := Truster{GPGBinary: gpgBin, StoreRoot: root}

	// lsign-only flips shared/ writable.
	plan, err := tr.PlanRecipients(context.Background(), "shared", Lsign)
	if err != nil {
		t.Fatalf("plan shared: %v", err)
	}
	report, err := tr.Apply(context.Background(), plan, Lsign, false)
	if err != nil {
		t.Fatalf("apply shared: %v", err)
	}
	if !report.NowWritable {
		t.Fatalf("shared not writable after lsign: %#v", report)
	}
	if trust := run("--export-ownertrust"); strings.Contains(trust, aliceFpr) {
		t.Fatalf("lsign-only must not write ownertrust, got:\n%s", trust)
	}

	// --full must set ownertrust to full (5) on a fresh key, and must never
	// downgrade an existing ultimate (6). gpg ownertrust values: 4=marginal,
	// 5=full, 6=ultimate. vip = own + bob(pre-set ultimate) + charlie(fresh).
	charlieFpr := importPub("Charlie <charlie@corp>")
	writeGPGID(t, filepath.Join(root, "vip"), ownFpr, bobFpr, charlieFpr)
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--yes", "--import-ownertrust")
	cmd.Stdin = strings.NewReader(bobFpr + ":6:\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("preset ownertrust: %v: %s", err, out)
	}
	planVip, err := tr.PlanRecipients(context.Background(), "vip", Full)
	if err != nil {
		t.Fatalf("plan vip: %v", err)
	}
	if _, err := tr.Apply(context.Background(), planVip, Full, false); err != nil {
		t.Fatalf("apply vip: %v", err)
	}
	trust := run("--export-ownertrust")
	if !strings.Contains(trust, bobFpr+":6:") {
		t.Fatalf("never-downgrade violated: bob ultimate (6) not preserved:\n%s", trust)
	}
	if !strings.Contains(trust, charlieFpr+":5:") {
		t.Fatalf("--full did not set fresh key to full (5):\n%s", trust)
	}
	if strings.Contains(trust, charlieFpr+":4:") {
		t.Fatalf("--full wrote marginal (4) instead of full (5):\n%s", trust)
	}
}
