package gpgdiag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Checker struct {
	GPGBinary string
	StoreRoot string
}

type DoctorReport struct {
	SchemaVersion int           `json:"schema_version"`
	StoreRoot     string        `json:"store_root"`
	PassOK        bool          `json:"pass_ok"`
	GPGOK         bool          `json:"gpg_ok"`
	Clipboard     []string      `json:"clipboard"`
	Stores        []StoreReport `json:"stores"`
	Warnings      []string      `json:"warnings,omitempty"`
}

type StoreReport struct {
	Label          string   `json:"label"`
	Path           string   `json:"path"`
	GPGIDPath      string   `json:"gpg_id_path"`
	Status         string   `json:"status"`
	RecipientCount int      `json:"recipient_count"`
	Missing        []string `json:"missing,omitempty"`
	Untrusted      []string `json:"untrusted,omitempty"`
	Owned          []string `json:"owned,omitempty"`
	Trusted        []string `json:"trusted,omitempty"`
}

type LocalKey struct {
	Fingerprint string `json:"fingerprint"`
	UID         string `json:"uid"`
	HasSecret   bool   `json:"has_secret"`
	OwnerTrust  string `json:"ownertrust"`
}

func New(storeRoot string) Checker {
	return Checker{GPGBinary: "gpg", StoreRoot: storeRoot}
}

func (c Checker) Doctor(ctx context.Context) DoctorReport {
	report := DoctorReport{
		SchemaVersion: 1,
		StoreRoot:     c.StoreRoot,
		PassOK:        commandExists("pass"),
		GPGOK:         commandExists(c.gpg()),
		Clipboard:     availableClipboardTools(),
	}
	if !report.PassOK {
		report.Warnings = append(report.Warnings, "pass is not available in PATH")
	}
	if !report.GPGOK {
		report.Warnings = append(report.Warnings, "gpg is not available in PATH")
		return report
	}
	stores, err := c.VerifyStores(ctx)
	if err != nil {
		report.Warnings = append(report.Warnings, err.Error())
	} else {
		report.Stores = stores
		for _, store := range stores {
			if store.Status != "ok" {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %s", store.Label, store.Status))
			}
		}
	}
	return report
}

func (c Checker) VerifyStores(ctx context.Context) ([]StoreReport, error) {
	root := c.StoreRoot
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("password store root is empty")
	}
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("password store directory %s does not exist", root)
		}
		return nil, fmt.Errorf("stat password store %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("password store path %s is not a directory", root)
	}

	trust, err := c.ownerTrust(ctx)
	if err != nil {
		return nil, err
	}

	dirs := []string{root}
	labels := []string{"default"}
	children, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read password store %s: %w", root, err)
	}
	anySubGPGID := false
	for _, child := range children {
		if !child.IsDir() {
			continue
		}
		if child.Name() == ".git" || child.Name() == ".gpg" {
			continue
		}
		dir := filepath.Join(root, child.Name())
		dirs = append(dirs, dir)
		labels = append(labels, child.Name())
		if _, err := os.Stat(filepath.Join(dir, ".gpg-id")); err == nil {
			anySubGPGID = true
		}
	}

	rootHasGPGID := fileExists(filepath.Join(root, ".gpg-id"))
	skipRoot := !rootHasGPGID && anySubGPGID
	reports := make([]StoreReport, 0, len(dirs))
	for i, dir := range dirs {
		if i == 0 && skipRoot {
			continue
		}
		report, err := c.verifyOneStore(ctx, labels[i], dir, trust)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func (c Checker) LocalKeys(ctx context.Context) ([]LocalKey, error) {
	trust, err := c.ownerTrust(ctx)
	if err != nil {
		return nil, err
	}
	out, err := c.runGPG(ctx, "--with-colons", "--list-keys")
	if err != nil {
		return nil, err
	}
	var keys []LocalKey
	var current *LocalKey
	pubPending := false
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 10 {
			continue
		}
		switch fields[0] {
		case "pub":
			if current != nil && current.Fingerprint != "" {
				keys = append(keys, *current)
			}
			current = &LocalKey{}
			pubPending = true
		case "uid":
			if current != nil && current.UID == "" {
				current.UID = fields[9]
			}
		case "fpr":
			if current != nil && pubPending {
				current.Fingerprint = fields[9]
				current.OwnerTrust = OwnerTrustLabel(trust[current.Fingerprint])
				current.HasSecret = c.hasSecret(ctx, current.Fingerprint)
				pubPending = false
			}
		}
	}
	if current != nil && current.Fingerprint != "" {
		keys = append(keys, *current)
	}
	return keys, nil
}

func (c Checker) verifyOneStore(ctx context.Context, label string, dir string, trust map[string]string) (StoreReport, error) {
	report := StoreReport{
		Label:     label,
		Path:      dir,
		GPGIDPath: filepath.Join(dir, ".gpg-id"),
	}
	data, err := os.ReadFile(report.GPGIDPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			report.Status = "no .gpg-id"
			return report, nil
		}
		return report, fmt.Errorf("read %s: %w", report.GPGIDPath, err)
	}
	ids := parseGPGID(data)
	report.RecipientCount = len(ids)
	if len(ids) == 0 {
		report.Status = "empty .gpg-id"
		return report, nil
	}
	for _, id := range ids {
		switch c.recipientStatus(ctx, id, trust) {
		case "owned":
			report.Owned = append(report.Owned, id)
		case "trusted":
			report.Trusted = append(report.Trusted, id)
		case "untrusted":
			report.Untrusted = append(report.Untrusted, id)
		default:
			report.Missing = append(report.Missing, id)
		}
	}
	switch {
	case len(report.Missing) > 0:
		report.Status = "missing recipients"
	case len(report.Untrusted) > 0:
		report.Status = "untrusted recipients"
	default:
		report.Status = "ok"
	}
	return report, nil
}

func (c Checker) recipientStatus(ctx context.Context, id string, trust map[string]string) string {
	if c.hasSecret(ctx, id) {
		return "owned"
	}
	out, err := c.runGPG(ctx, "--batch", "--with-colons", "--list-keys", id)
	if err != nil || strings.TrimSpace(out) == "" {
		return "missing"
	}
	fps := primaryFingerprints(out)
	if len(fps) == 0 {
		return "missing"
	}
	for _, fp := range fps {
		if level := trust[fp]; level == "4" || level == "5" {
			return "trusted"
		}
	}
	return "untrusted"
}

func (c Checker) hasSecret(ctx context.Context, id string) bool {
	_, err := c.runGPG(ctx, "--batch", "--quiet", "--list-secret-keys", id)
	return err == nil
}

func (c Checker) ownerTrust(ctx context.Context) (map[string]string, error) {
	out, err := c.runGPG(ctx, "--export-ownertrust")
	if err != nil {
		return nil, err
	}
	trust := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) >= 2 && parts[0] != "" {
			trust[parts[0]] = parts[1]
		}
	}
	return trust, nil
}

func (c Checker) runGPG(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.gpg(), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gpg %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (c Checker) gpg() string {
	if strings.TrimSpace(c.GPGBinary) == "" {
		return "gpg"
	}
	return c.GPGBinary
}

func parseGPGID(data []byte) []string {
	var ids []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			ids = append(ids, line)
		}
	}
	return ids
}

func primaryFingerprints(colons string) []string {
	var fps []string
	pubPending := false
	for _, line := range strings.Split(colons, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 10 {
			continue
		}
		switch fields[0] {
		case "pub":
			pubPending = true
		case "fpr":
			if pubPending {
				fps = append(fps, fields[9])
				pubPending = false
			}
		}
	}
	return fps
}

func OwnerTrustLabel(level string) string {
	switch level {
	case "5":
		return "ultimate"
	case "4":
		return "full"
	case "3":
		return "marginal"
	case "2":
		return "never"
	case "1":
		return "unknown"
	default:
		return "unset"
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func availableClipboardTools() []string {
	var tools []string
	for _, name := range []string{"pbcopy", "wl-copy", "xclip", "xsel"} {
		if commandExists(name) {
			tools = append(tools, name)
		}
	}
	return tools
}
