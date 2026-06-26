package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionsMentionCommands(t *testing.T) {
	root := repoRoot(t)
	files := []string{
		filepath.Join(root, "completions", "passage.bash"),
		filepath.Join(root, "completions", "passage.zsh"),
		filepath.Join(root, "completions", "passage.fish"),
	}
	commands := []string{
		"list", "show", "copy", "reveal", "totp", "mfa",
		"pin", "unpin", "clear-recents", "clear-pins", "clear-clipboard",
		"doctor", "access", "trust", "keys", "theme", "version", "help",
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			text := string(data)
			for _, command := range commands {
				if !strings.Contains(text, command) {
					t.Fatalf("%s missing command %q", file, command)
				}
			}
		})
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("could not find repo root")
		}
		dir = next
	}
}
