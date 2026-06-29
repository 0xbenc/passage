package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/0xbenc/passage/internal/fsutil"
)

const SchemaVersion = 1

type Record struct {
	Path     string `json:"path"`
	Pinned   bool   `json:"pinned"`
	LastUsed int64  `json:"last_used"`
}

type State struct {
	SchemaVersion    int      `json:"schema_version"`
	Records          []Record `json:"records"`
	LastIntroVersion string   `json:"last_intro_version,omitempty"`
}

type LoadResult struct {
	State       State
	Path        string
	Migrated    bool
	OldPath     string
	Diagnostics []string
}

func Empty() State {
	return State{SchemaVersion: SchemaVersion}
}

func ResolveDir(env []string) (string, error) {
	values := envMap(env)
	if dir := strings.TrimSpace(values["PASSAGE_STATE_DIR"]); dir != "" {
		return expandHome(filepath.Clean(dir))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "passage"), nil
	}
	if xdg := strings.TrimSpace(values["XDG_STATE_HOME"]); xdg != "" {
		return expandHome(filepath.Join(xdg, "passage"))
	}
	return filepath.Join(home, ".local", "state", "passage"), nil
}

func Load(dir string) (LoadResult, error) {
	path := filepath.Join(dir, "state.json")
	result := LoadResult{Path: path}
	data, err := os.ReadFile(path)
	if err == nil {
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			return result, fmt.Errorf("parse state %s: %w", path, err)
		}
		if st.SchemaVersion == 0 {
			st.SchemaVersion = SchemaVersion
		}
		if st.SchemaVersion != SchemaVersion {
			return result, fmt.Errorf("unsupported state schema_version %d", st.SchemaVersion)
		}
		result.State = st.normalized()
		return result, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("read state %s: %w", path, err)
	}

	st, oldPath, diags := loadLegacy()
	result.State = st.normalized()
	result.Diagnostics = diags
	if oldPath != "" {
		result.Migrated = true
		result.OldPath = oldPath
	}
	return result, nil
}

func (s State) Save(path string) error {
	st := s.normalized()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')
	_, err = fsutil.AtomicWriteFile(path, data, fsutil.WriteOptions{
		Mode:         0o600,
		Backup:       true,
		BackupPrefix: "passage-state",
	})
	if err != nil {
		return fmt.Errorf("write state %s: %w", path, err)
	}
	return nil
}

func (s State) Record(path string) Record {
	for _, record := range s.Records {
		if record.Path == path {
			return record
		}
	}
	return Record{Path: path}
}

func (s *State) Touch(path string, now int64) {
	idx := s.index(path)
	if idx < 0 {
		s.Records = append(s.Records, Record{Path: path, LastUsed: now})
		return
	}
	s.Records[idx].LastUsed = now
}

func (s *State) SetPinned(path string, pinned bool) {
	idx := s.index(path)
	if idx < 0 {
		s.Records = append(s.Records, Record{Path: path, Pinned: pinned})
		return
	}
	s.Records[idx].Pinned = pinned
}

func (s *State) SetLastIntroVersion(version string) {
	s.LastIntroVersion = version
}

func (s *State) TogglePinned(path string) bool {
	idx := s.index(path)
	if idx < 0 {
		s.Records = append(s.Records, Record{Path: path, Pinned: true})
		return true
	}
	s.Records[idx].Pinned = !s.Records[idx].Pinned
	return s.Records[idx].Pinned
}

func (s *State) ClearPins() {
	for i := range s.Records {
		s.Records[i].Pinned = false
	}
}

func (s *State) ClearRecents() {
	for i := range s.Records {
		s.Records[i].LastUsed = 0
	}
}

func (s State) index(path string) int {
	for i, record := range s.Records {
		if record.Path == path {
			return i
		}
	}
	return -1
}

func (s State) normalized() State {
	// LastIntroVersion is a top-level field, not derived from Records, so it must
	// be carried through here or it would be dropped on every Load/Save.
	out := State{SchemaVersion: SchemaVersion, LastIntroVersion: s.LastIntroVersion}
	seen := map[string]int{}
	for _, record := range s.Records {
		record.Path = strings.TrimSpace(record.Path)
		if record.Path == "" {
			continue
		}
		if idx, ok := seen[record.Path]; ok {
			if record.Pinned {
				out.Records[idx].Pinned = true
			}
			if record.LastUsed > out.Records[idx].LastUsed {
				out.Records[idx].LastUsed = record.LastUsed
			}
			continue
		}
		seen[record.Path] = len(out.Records)
		out.Records = append(out.Records, record)
	}
	return out
}

func loadLegacy() (State, string, []string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Empty(), "", []string{"could not resolve home for legacy state migration"}
	}
	base := filepath.Join(home, ".local", "state")
	if xdg := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); xdg != "" {
		base = xdg
	}
	candidates := []string{
		filepath.Join(base, "bash-zoo", "passage", "state.tsv"),
		filepath.Join(base, "bash-zoo", "pass-browse", "state.tsv"),
	}
	var diagnostics []string
	for _, candidate := range candidates {
		st, ok, diag := readLegacyTSV(candidate)
		if diag != "" {
			diagnostics = append(diagnostics, diag)
		}
		if ok {
			return st, candidate, diagnostics
		}
	}
	return Empty(), "", diagnostics
}

func readLegacyTSV(path string) (State, bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Empty(), false, ""
		}
		return Empty(), false, fmt.Sprintf("could not read legacy state %s: %v", path, err)
	}
	st := Empty()
	for lineNo, raw := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		parts := strings.Split(raw, "\t")
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		record := Record{Path: parts[0]}
		if len(parts) > 1 {
			record.Pinned = strings.TrimSpace(parts[1]) == "1"
		}
		if len(parts) > 2 {
			usedText := strings.TrimSpace(parts[2])
			if usedText != "" {
				used, err := strconv.ParseInt(usedText, 10, 64)
				if err != nil {
					return Empty(), false, fmt.Sprintf("legacy state %s:%d has invalid timestamp", path, lineNo+1)
				}
				record.LastUsed = used
			}
		}
		st.Records = append(st.Records, record)
	}
	return st.normalized(), true, ""
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
