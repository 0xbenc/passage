package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyProviderOrderWayland(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	makeTool(t, dir, "wl-copy", "exit 1")
	makeTool(t, dir, "xclip", "/bin/cat > \"$CLIP_OUT\"")
	out := filepath.Join(dir, "clip.txt")
	t.Setenv("CLIP_OUT", out)
	res, err := Copy(context.Background(), []byte("secret"))
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if res.Tool != "xclip" {
		t.Fatalf("tool = %s, want xclip fallback after wl-copy failure", res.Tool)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "secret" {
		t.Fatalf("clipboard data = %q", data)
	}
}

func TestCopyNoTool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PASSAGE_NO_OSC52", "1")
	_, err := Copy(context.Background(), []byte("secret"))
	if err == nil {
		t.Fatal("Copy succeeded without tools")
	}
}

func TestCopyFallsBackToOSC52(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tty := filepath.Join(t.TempDir(), "tty")
	if err := os.WriteFile(tty, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	withOSC52TTY(t, tty)

	res, err := Copy(context.Background(), []byte("secret"))
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if res.Tool != "osc52" {
		t.Fatalf("tool = %s, want osc52", res.Tool)
	}
	data, err := os.ReadFile(tty)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "\x1b]52;c;c2VjcmV0\a" {
		t.Fatalf("osc52 = %q", data)
	}
}

func TestCopyFallsBackToOSC52Tmux(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("TMUX", "/tmp/tmux")
	tty := filepath.Join(t.TempDir(), "tty")
	if err := os.WriteFile(tty, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	withOSC52TTY(t, tty)

	res, err := Copy(context.Background(), []byte("secret"))
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if res.Tool != "osc52-tmux" {
		t.Fatalf("tool = %s, want osc52-tmux", res.Tool)
	}
	data, err := os.ReadFile(tty)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := "\x1bPtmux;\x1b\x1b]52;c;c2VjcmV0\a\x1b\\"
	if string(data) != want {
		t.Fatalf("osc52 tmux = %q, want %q", data, want)
	}
}

func makeTool(t *testing.T, dir string, name string, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	data := []byte("#!/bin/sh\n" + body + "\n")
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
}

func withOSC52TTY(t *testing.T, path string) {
	t.Helper()
	old := osc52TTYPath
	osc52TTYPath = path
	t.Cleanup(func() {
		osc52TTYPath = old
	})
}
