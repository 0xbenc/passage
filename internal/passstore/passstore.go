package passstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/0xbenc/passage/internal/fuzzy"
	"github.com/0xbenc/passage/internal/procutil"
	"github.com/0xbenc/passage/internal/state"
)

const DefaultStoreName = ".password-store"

type Entry struct {
	Path      string `json:"path"`
	Display   string `json:"display"`
	Pinned    bool   `json:"pinned"`
	LastUsed  int64  `json:"last_used"`
	HasMFA    bool   `json:"has_mfa"`
	MFATarget string `json:"mfa_target,omitempty"`
}

type Store struct {
	Root       string
	PassBinary string
	Stdin      io.Reader
	Stderr     io.Writer
}

func ResolveStoreDir(env []string) (string, error) {
	values := envMap(env)
	if dir := strings.TrimSpace(values["PASSWORD_STORE_DIR"]); dir != "" {
		return expandHome(filepath.Clean(dir))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, DefaultStoreName), nil
}

func ResolvePassBinary(env []string) string {
	values := envMap(env)
	if binary := strings.TrimSpace(values["PASSAGE_PASS_BINARY"]); binary != "" {
		return binary
	}
	return "pass"
}

func New(root string, passBinary string) Store {
	return Store{Root: filepath.Clean(root), PassBinary: defaultString(passBinary, "pass")}
}

func (s Store) Discover() ([]string, error) {
	if s.Root == "" {
		return nil, errors.New("password store root is empty")
	}
	info, err := os.Stat(s.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("password store directory %s does not exist", s.Root)
		}
		return nil, fmt.Errorf("stat password store %s: %w", s.Root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("password store path %s is not a directory", s.Root)
	}

	var entries []string
	err = filepath.WalkDir(s.Root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".gpg" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(name) != ".gpg" {
			return nil
		}
		rel, err := filepath.Rel(s.Root, path)
		if err != nil {
			return err
		}
		rel = strings.TrimSuffix(filepath.ToSlash(rel), ".gpg")
		if rel != "" {
			entries = append(entries, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk password store %s: %w", s.Root, err)
	}
	sort.Strings(entries)
	return compactSorted(entries), nil
}

func BuildEntries(paths []string, st state.State) []Entry {
	known := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		known[path] = struct{}{}
	}

	entries := make([]Entry, 0, len(paths))
	for _, path := range paths {
		record := st.Record(path)
		target, hasMFA := MFATarget(path, known)
		entries = append(entries, Entry{
			Path:      path,
			Display:   Display(path),
			Pinned:    record.Pinned,
			LastUsed:  record.LastUsed,
			HasMFA:    hasMFA,
			MFATarget: target,
		})
	}
	SortEntries(entries)
	return entries
}

func SortEntries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if a.LastUsed != b.LastUsed {
			return a.LastUsed > b.LastUsed
		}
		return a.Path < b.Path
	})
}

func FilterEntries(entries []Entry, filter string, mfaOnly bool) []Entry {
	filter = strings.ToLower(strings.TrimSpace(filter))
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if mfaOnly && !entry.HasMFA {
			continue
		}
		if filter != "" {
			path := strings.ToLower(entry.Path)
			display := strings.ToLower(entry.Display)
			if !strings.Contains(path, filter) && !strings.Contains(display, filter) {
				continue
			}
		}
		out = append(out, entry)
	}
	return out
}

// Ranked is one entry surviving a fuzzy filter: its index into the input
// slice, the match score, and the matched rune positions within the entry's
// Display string (nil when the entry matched only via its Path). It is the
// single ranking primitive shared by the interactive picker and the
// single-match auto-run path, so the two can never disagree about which
// entries a filter selects — important for a password manager, where a
// divergence could auto-act on an entry the picker would not have surfaced.
type Ranked struct {
	Index     int
	Score     int
	Positions []int
}

// Rank filters entries by a fuzzy query and orders the survivors. An empty
// filter keeps every (mfa-eligible) entry in the input order, preserving the
// store's pin/recency ordering. A non-empty filter keeps only fuzzy matches,
// ordered by: pinned first, then score, then recency, then original order.
// Positions index into Display so a caller can highlight the matched runes of
// the visible title.
func Rank(entries []Entry, filter string, mfaOnly bool) []Ranked {
	filter = strings.TrimSpace(filter)
	out := make([]Ranked, 0, len(entries))
	for i, entry := range entries {
		if mfaOnly && !entry.HasMFA {
			continue
		}
		if filter == "" {
			out = append(out, Ranked{Index: i})
			continue
		}
		dispRes, dispOK := fuzzy.Match(filter, entry.Display)
		pathRes, pathOK := fuzzy.Match(filter, entry.Path)
		if !dispOK && !pathOK {
			continue
		}
		ranked := Ranked{Index: i}
		matched := false
		if dispOK {
			matched = true
			ranked.Score = dispRes.Score
			ranked.Positions = dispRes.Positions
		}
		if pathOK && (!matched || pathRes.Score > ranked.Score) {
			ranked.Score = pathRes.Score
			// Highlight only the visible Display; if the entry matched
			// solely through its raw Path there are no display positions.
			if !dispOK {
				ranked.Positions = nil
			}
		}
		out = append(out, ranked)
	}
	if filter != "" {
		sort.SliceStable(out, func(a, b int) bool {
			ea, eb := entries[out[a].Index], entries[out[b].Index]
			if ea.Pinned != eb.Pinned {
				return ea.Pinned
			}
			if out[a].Score != out[b].Score {
				return out[a].Score > out[b].Score
			}
			if ea.LastUsed != eb.LastUsed {
				return ea.LastUsed > eb.LastUsed
			}
			return out[a].Index < out[b].Index
		})
	}
	return out
}

func MFATarget(path string, known map[string]struct{}) (string, bool) {
	if path == "mfa" || strings.HasSuffix(path, "/mfa") {
		return path, true
	}
	sibling := path + "/mfa"
	if _, ok := known[sibling]; ok {
		return sibling, true
	}
	return "", false
}

func Display(path string) string {
	return strings.ReplaceAll(path, "/", " | ")
}

func FirstLine(content []byte) []byte {
	if idx := bytes.IndexByte(content, '\n'); idx >= 0 {
		return append([]byte(nil), content[:idx]...)
	}
	return append([]byte(nil), content...)
}

func (s Store) Show(ctx context.Context, entry string) ([]byte, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil, errors.New("entry is empty")
	}
	cmd := exec.CommandContext(ctx, s.PassBinary, "show", "--", entry)
	procutil.ConfigureCommandCancellation(cmd)
	cmd.Env = withEnv(os.Environ(), append([]string{"PASSWORD_STORE_DIR=" + s.Root}, gpgTTYEnv()...)...)
	cmd.Stdin = s.Stdin
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if s.Stderr != nil {
		cmd.Stderr = io.MultiWriter(s.Stderr, &stderr)
	}
	err := cmd.Run()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("pass show %s canceled: %w", entry, ctxErr)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("pass show %s failed: %s", entry, msg)
		}
		return nil, fmt.Errorf("pass show %s failed: %w", entry, err)
	}
	return stdout.Bytes(), nil
}

func TouchNow() int64 {
	return time.Now().Unix()
}

func SetupGPGTTY(ctx context.Context, env []string) {
	if runtime.GOOS == "windows" {
		return
	}
	tty := strings.TrimSpace(os.Getenv("GPG_TTY"))
	if tty == "" {
		if out, err := exec.CommandContext(ctx, "tty").Output(); err == nil {
			tty = strings.TrimSpace(string(out))
			if tty != "" && tty != "not a tty" {
				_ = os.Setenv("GPG_TTY", tty)
			}
		}
	}
	if _, err := exec.LookPath("gpg-connect-agent"); err == nil {
		cmd := exec.CommandContext(ctx, "gpg-connect-agent", "updatestartuptty", "/bye")
		cmd.Env = append(os.Environ(), env...)
		_ = cmd.Run()
	}
}

func gpgTTYEnv() []string {
	if tty := strings.TrimSpace(os.Getenv("GPG_TTY")); tty != "" {
		return []string{"GPG_TTY=" + tty}
	}
	return nil
}

func withEnv(env []string, values ...string) []string {
	out := append([]string(nil), env...)
	for _, value := range values {
		key, _, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		replaced := false
		prefix := key + "="
		for i, existing := range out {
			if strings.HasPrefix(existing, prefix) {
				out[i] = value
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, value)
		}
	}
	return out
}

func compactSorted(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func envMap(env []string) map[string]string {
	if env == nil {
		env = os.Environ()
	}
	out := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

func expandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
