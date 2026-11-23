// Package clipboard provides system-clipboard access shared by the TUI and the
// editor harness. Copy uses local clipboard tools and an OSC 52 terminal escape,
// in an order that depends on where TideMail runs: over SSH a local tool would
// fill the remote machine's clipboard, so the terminal escape goes first there.
// Read uses local tools only — OSC 52 reads are unreliable and usually disabled,
// so paste-in over remote sessions should rely on the terminal's own bracketed
// paste.
package clipboard

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/aymanbagabas/go-osc52/v2"
)

// Mode chooses how Copy behaves. The zero value is Auto.
type Mode string

const (
	Auto   Mode = "auto"  // terminal escape first over SSH, local tools first otherwise
	OSC52  Mode = "osc52" // terminal escape first, local tools as the fallback
	Local  Mode = "local" // local tools first, terminal escape as the fallback
	maxOSC      = 100_000 // bytes of text sent as one terminal escape; many terminals refuse more
)

// Method says how a copy was delivered, so the UI can avoid claiming more than
// it knows: a terminal escape cannot be confirmed.
type Method string

const (
	ViaLocal    Method = "local"
	ViaTerminal Method = "terminal"
)

// Seams for tests: override to force the no-tool fallback path and to capture
// the emitted OSC 52 sequence without touching the real terminal.
var (
	lookPath    = exec.LookPath
	osc52Writer io.Writer // nil: /dev/tty, else stderr
	mode        = Auto
	getenv      = os.Getenv
)

// SetMode applies [display] clipboard. Unknown values mean Auto.
func SetMode(m string) {
	switch Mode(strings.ToLower(strings.TrimSpace(m))) {
	case OSC52:
		mode = OSC52
	case Local:
		mode = Local
	default:
		mode = Auto
	}
}

// OverSSH reports whether this process runs inside an SSH session, judged from
// the environment it started with. A tmux server that was started outside SSH
// and attached later does not carry these variables; set [display] clipboard =
// "osc52" there.
func OverSSH() bool {
	return getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" || getenv("SSH_CLIENT") != ""
}

// Copy writes text to the system clipboard; see CopyVia.
func Copy(text string) error {
	_, err := CopyVia(text)
	return err
}

// CopyVia writes text to the system clipboard and reports how. It tries wl-copy,
// xclip, xsel and pbcopy, and an OSC 52 escape written to the controlling
// terminal; which comes first depends on the mode (see Mode). It returns an
// error only when every mechanism fails.
func CopyVia(text string) (Method, error) {
	terminal, closeTerminal := terminalWriter()
	defer closeTerminal()
	return copyTo(text, terminal)
}

// CopyTo is Copy with the terminal escape sent to the interactive terminal the
// caller supplies.
func CopyTo(text string, terminal io.Writer) error {
	_, err := copyTo(text, terminal)
	return err
}

func copyTo(text string, terminal io.Writer) (Method, error) {
	terminalFirst := mode == OSC52 || (mode == Auto && OverSSH())
	var terminalErr error
	if terminalFirst {
		if terminalErr = writeOSC52(text, terminal); terminalErr == nil {
			return ViaTerminal, nil
		}
	}
	if copyLocal(text) {
		return ViaLocal, nil
	}
	if !terminalFirst {
		if terminalErr = writeOSC52(text, terminal); terminalErr == nil {
			return ViaTerminal, nil
		}
	}
	return "", fmt.Errorf("no clipboard tool found and the terminal escape failed: %w", terminalErr)
}

func copyLocal(text string) bool {
	candidates := [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
		{"pbcopy"},
	}
	for _, args := range candidates {
		path, err := lookPath(args[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, args[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return true
		}
	}
	return false
}

// writeOSC52 sends the escape. Under tmux it is sent twice: plain, which tmux
// forwards when `set-clipboard on`, and wrapped in a DCS passthrough, which
// reaches the terminal when `allow-passthrough on`. Either setting is enough
// and the second copy is harmless. GNU screen gets its own wrapper.
func writeOSC52(text string, terminal io.Writer) error {
	if len(text) > maxOSC {
		return fmt.Errorf("%d KB is too much for a terminal clipboard escape (limit %d KB); save it to a file instead", len(text)/1000, maxOSC/1000)
	}
	seq := osc52.New(text)
	if _, err := seq.WriteTo(terminal); err != nil {
		return err
	}
	switch {
	case getenv("TMUX") != "":
		_, err := seq.Tmux().WriteTo(terminal)
		return err
	case strings.HasPrefix(getenv("TERM"), "screen"):
		_, err := seq.Screen().WriteTo(terminal)
		return err
	}
	return nil
}

// terminalWriter is where the escape goes: the test seam if set, else the
// controlling terminal (so it does not depend on where stderr points), else
// stderr.
func terminalWriter() (io.Writer, func()) {
	if osc52Writer != nil {
		return osc52Writer, func() {}
	}
	if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		return tty, func() { _ = tty.Close() }
	}
	return os.Stderr, func() {}
}

// Read returns the system clipboard contents via wl-paste, xclip, xsel, or
// pbpaste. There is no OSC 52 fallback for reads.
func Read() (string, error) {
	candidates := [][]string{
		{"wl-paste", "--no-newline"},
		{"xclip", "-selection", "clipboard", "-out"},
		{"xsel", "--clipboard", "--output"},
		{"pbpaste"},
	}
	for _, args := range candidates {
		path, err := lookPath(args[0])
		if err != nil {
			continue
		}
		out, err := exec.Command(path, args[1:]...).Output()
		if err == nil {
			return string(out), nil
		}
	}
	if OverSSH() {
		return "", fmt.Errorf("no clipboard tool found; over SSH paste with your terminal's own paste (for example Ctrl+Shift+V)")
	}
	return "", fmt.Errorf("no clipboard tool found (wl-paste/xclip/xsel/pbpaste)")
}
