package ui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// composeFilePickerCmd is the user's [display] file_picker_cmd, injected from
// config the same way editorVimMode is. Empty means the built-in picker.
var composeFilePickerCmd string

// setComposeFilePickerCmd sets the external file picker command line used by
// Alt+F in compose.
func setComposeFilePickerCmd(cmd string) { composeFilePickerCmd = strings.TrimSpace(cmd) }

// chooserPlaceholder is replaced in file_picker_cmd by the path of the temp
// file the command writes its chosen paths to.
const chooserPlaceholder = "{chooser}"

// filePickerDoneMsg reports the result of an external file picker run.
type filePickerDoneMsg struct {
	paths  []string
	runErr error // the command failed to start or exited non-zero
	rdErr  error // the chooser file could not be read
}

// externalPickerSupported reports whether file_picker_cmd can run here. The
// command is run with sh -c, so Windows keeps the built-in picker.
func externalPickerSupported() bool { return runtime.GOOS != "windows" }

// shellQuote quotes s as one word for sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pickerCommandLine substitutes the shell-quoted chooser path into template.
func pickerCommandLine(template, chooserPath string) string {
	return strings.ReplaceAll(template, chooserPlaceholder, shellQuote(chooserPath))
}

// readChooserFile returns the non-blank lines of the chooser file, which the
// picker writes one path per line.
func readChooserFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		paths = append(paths, line)
	}
	return paths, nil
}

// runFilePickerCmd suspends the TUI, runs the configured picker and returns a
// filePickerDoneMsg. The caller has checked that composeFilePickerCmd is set,
// contains the placeholder and is supported here.
func runFilePickerCmd(template string) (tea.Cmd, error) {
	f, err := os.CreateTemp("", "tidemail-chooser-*")
	if err != nil {
		return nil, err
	}
	chooser := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(chooser)
		return nil, err
	}
	cmd := exec.Command("sh", "-c", pickerCommandLine(template, chooser))
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		paths, rdErr := readChooserFile(chooser)
		_ = os.Remove(chooser)
		return filePickerDoneMsg{paths: paths, runErr: runErr, rdErr: rdErr}
	}), nil
}

// startAttachPick handles Alt+F: the external picker when one is configured,
// otherwise the built-in browser in the user's home directory.
func (c ComposeModel) startAttachPick() (ComposeModel, tea.Cmd) {
	if composeFilePickerCmd != "" && externalPickerSupported() {
		if !strings.Contains(composeFilePickerCmd, chooserPlaceholder) {
			c.statusMsg = "file_picker_cmd must contain " + chooserPlaceholder
			c.isErr = true
			return c, nil
		}
		cmd, err := runFilePickerCmd(composeFilePickerCmd)
		if err != nil {
			c.statusMsg = fmt.Sprintf("file picker: %v", err)
			c.isErr = true
			return c, nil
		}
		return c, cmd
	}
	home, err := os.UserHomeDir()
	startDir := home
	if err != nil {
		startDir = "/"
	}
	c.openPicker(startDir)
	return c, nil
}

// attachPicked attaches every path the external picker chose and reports the
// outcome in the status line. A path that fails does not stop the others.
func (c ComposeModel) attachPicked(msg filePickerDoneMsg) ComposeModel {
	if len(msg.paths) == 0 {
		switch {
		case msg.runErr != nil:
			c.statusMsg = fmt.Sprintf("file picker: %v", msg.runErr)
			c.isErr = true
		case msg.rdErr != nil:
			c.statusMsg = fmt.Sprintf("file picker: %v", msg.rdErr)
			c.isErr = true
		default:
			c.statusMsg = "no file chosen"
			c.isErr = false
		}
		return c
	}
	var names []string
	var failed []string
	for _, p := range msg.paths {
		af, err := c.attachPath(p)
		if err != nil {
			failed = append(failed, err.Error())
			continue
		}
		names = append(names, af.Name)
	}
	switch {
	case len(failed) > 0 && len(names) == 0:
		c.statusMsg = failed[0]
		c.isErr = true
	case len(failed) > 0:
		c.statusMsg = fmt.Sprintf("attached %d, failed %d: %s", len(names), len(failed), failed[0])
		c.isErr = true
	case len(names) == 1:
		c.statusMsg = "attached: " + names[0]
		c.isErr = false
	default:
		c.statusMsg = fmt.Sprintf("attached %d files", len(names))
		c.isErr = false
	}
	return c
}
