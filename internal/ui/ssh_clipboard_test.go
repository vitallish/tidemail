package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/allisonhere/tidemail/internal/clipboard"
	"github.com/charmbracelet/x/ansi"
)

func TestStatusBarShowsSSHBadgeOnlyOverSSH(t *testing.T) {
	m := expandableModel(t)
	orig := overSSH
	t.Cleanup(func() { overSSH = orig })
	for _, c := range []struct {
		ssh  bool
		want bool
	}{{true, true}, {false, false}} {
		overSSH = func() bool { return c.ssh }
		bar := ansi.Strip(m.renderStatusBar())
		if got := strings.Contains(bar, "ssh"); got != c.want {
			t.Errorf("over ssh=%v: badge shown=%v in %q", c.ssh, got, bar)
		}
	}
}

func TestCopyStatusSaysWhenOnlyTheTerminalWasAsked(t *testing.T) {
	m := expandableModel(t)
	for method, want := range map[clipboard.Method]string{
		clipboard.ViaLocal:    "copied to clipboard",
		clipboard.ViaTerminal: "sent to your terminal's clipboard",
	} {
		next, _ := m.Update(ClipboardCopiedMsg{Method: method})
		if got := next.(Model).statusMsg; got != want {
			t.Errorf("%s: status %q, want %q", method, got, want)
		}
	}
	next, _ := m.Update(ClipboardCopiedMsg{Err: errors.New("boom")})
	if got := next.(Model).statusMsg; !strings.Contains(got, "copy failed: boom") {
		t.Errorf("status %q", got)
	}
}
