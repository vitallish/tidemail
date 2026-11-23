package clipboard

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestCopyFallsBackToOSC52WhenNoToolFound(t *testing.T) {
	origLook, origWriter := lookPath, osc52Writer
	t.Cleanup(func() { lookPath, osc52Writer = origLook, origWriter })

	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	var buf bytes.Buffer
	osc52Writer = &buf

	if err := Copy("hi"); err != nil {
		t.Fatalf("Copy should succeed via OSC 52 fallback, got %v", err)
	}

	out := buf.String()
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hi"))
	if !strings.HasPrefix(out, want) {
		t.Fatalf("OSC 52 output = %q, want prefix %q", out, want)
	}
}

func TestReadErrorsWhenNoToolFound(t *testing.T) {
	origLook := lookPath
	t.Cleanup(func() { lookPath = origLook })
	lookPath = func(string) (string, error) { return "", errors.New("not found") }

	if _, err := Read(); err == nil {
		t.Fatal("Read should error when no clipboard tool is available")
	}
}

// fakeTool installs a wl-copy that records what it was given, and reports
// whether it ran.
func fakeTool(t *testing.T) (ran func() bool) {
	t.Helper()
	dir := t.TempDir()
	marker := dir + "/ran"
	script := dir + "/wl-copy"
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat > '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(name string) (string, error) {
		if name == "wl-copy" {
			return script, nil
		}
		return "", errors.New("not found")
	}
	return func() bool { _, err := os.Stat(marker); return err == nil }
}

func withEnv(t *testing.T, env map[string]string) {
	t.Helper()
	origGet, origMode := getenv, mode
	t.Cleanup(func() { getenv, mode = origGet, origMode })
	getenv = func(k string) string { return env[k] }
	mode = Auto
}

func TestOverSSHUsesTheTerminalBeforeALocalTool(t *testing.T) {
	ran := fakeTool(t)
	withEnv(t, map[string]string{"SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"})
	origW := osc52Writer
	t.Cleanup(func() { osc52Writer = origW })
	var buf bytes.Buffer
	osc52Writer = &buf
	method, err := CopyVia("hello")
	if err != nil || method != ViaTerminal {
		t.Fatalf("method %q err %v", method, err)
	}
	if ran() {
		t.Error("a local tool would have filled the remote machine's clipboard")
	}
	if !strings.HasPrefix(buf.String(), "\x1b]52;c;") {
		t.Errorf("no OSC 52 written: %q", buf.String())
	}
}

func TestLocalToolFirstWhenNotOverSSH(t *testing.T) {
	ran := fakeTool(t)
	withEnv(t, map[string]string{})
	origW := osc52Writer
	t.Cleanup(func() { osc52Writer = origW })
	var buf bytes.Buffer
	osc52Writer = &buf
	method, err := CopyVia("hello")
	if err != nil || method != ViaLocal || !ran() || buf.Len() != 0 {
		t.Fatalf("method %q err %v ran %v terminal %q", method, err, ran(), buf.String())
	}
}

func TestSetModeOverridesDetection(t *testing.T) {
	fakeTool(t)
	withEnv(t, map[string]string{}) // not over SSH
	SetMode("osc52")
	origW := osc52Writer
	t.Cleanup(func() { osc52Writer = origW })
	var buf bytes.Buffer
	osc52Writer = &buf
	if method, err := CopyVia("x"); err != nil || method != ViaTerminal {
		t.Fatalf("osc52 mode: %q %v", method, err)
	}
	withEnv(t, map[string]string{"SSH_TTY": "/dev/pts/3"})
	SetMode("local")
	buf.Reset()
	if method, err := CopyVia("x"); err != nil || method != ViaLocal || buf.Len() != 0 {
		t.Fatalf("local mode: %q %v %q", method, err, buf.String())
	}
	SetMode("nonsense")
	if mode != Auto {
		t.Errorf("unknown mode should mean auto, got %q", mode)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("tty gone") }

func TestTerminalFailureFallsBackToALocalTool(t *testing.T) {
	ran := fakeTool(t)
	withEnv(t, map[string]string{"SSH_CLIENT": "x"})
	origW := osc52Writer
	t.Cleanup(func() { osc52Writer = origW })
	osc52Writer = failingWriter{}
	if method, err := CopyVia("x"); err != nil || method != ViaLocal || !ran() {
		t.Fatalf("method %q err %v ran %v", method, err, ran())
	}
}

func TestTmuxGetsAPassthroughCopyToo(t *testing.T) {
	withEnv(t, map[string]string{"SSH_CONNECTION": "x", "TMUX": "/tmp/tmux-1000/default,1,0"})
	origW, origL := osc52Writer, lookPath
	t.Cleanup(func() { osc52Writer, lookPath = origW, origL })
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	var buf bytes.Buffer
	osc52Writer = &buf
	if _, err := CopyVia("hi"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "\x1b]52;c;") || !strings.Contains(buf.String(), "\x1bPtmux;") {
		t.Errorf("want a plain escape and a tmux passthrough one: %q", buf.String())
	}
}

func TestOversizedTextIsRefusedNotTruncated(t *testing.T) {
	withEnv(t, map[string]string{"SSH_CONNECTION": "x"})
	origW, origL := osc52Writer, lookPath
	t.Cleanup(func() { osc52Writer, lookPath = origW, origL })
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	var buf bytes.Buffer
	osc52Writer = &buf
	_, err := CopyVia(strings.Repeat("x", maxOSC+1))
	if err == nil || !strings.Contains(err.Error(), "too much") || buf.Len() != 0 {
		t.Fatalf("err %v, wrote %d bytes", err, buf.Len())
	}
}

func TestReadErrorOverSSHPointsAtTheTerminalPaste(t *testing.T) {
	withEnv(t, map[string]string{"SSH_CONNECTION": "x"})
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(string) (string, error) { return "", errors.New("none") }
	if _, err := Read(); err == nil || !strings.Contains(err.Error(), "terminal's own paste") {
		t.Fatalf("err %v", err)
	}
}
