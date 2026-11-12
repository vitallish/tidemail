package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allisonhere/tidemail/internal/config"
)

func TestPickerCommandLineQuotesChooserPath(t *testing.T) {
	got := pickerCommandLine("yazi --chooser-file={chooser}", "/tmp/it's here/x")
	want := `yazi --chooser-file='/tmp/it'\''s here/x'`
	if got != want {
		t.Fatalf("pickerCommandLine = %q, want %q", got, want)
	}
}

func TestReadChooserFile(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		content string
		want    []string
	}{
		"empty":         {"", nil},
		"one":           {"/a/b.pdf\n", []string{"/a/b.pdf"}},
		"no newline":    {"/a/b.pdf", []string{"/a/b.pdf"}},
		"several":       {"/a/b.pdf\n/c/d e.csv\n", []string{"/a/b.pdf", "/c/d e.csv"}},
		"blank lines":   {"\n/a/b.pdf\n\n  \n/c.txt\n\n", []string{"/a/b.pdf", "/c.txt"}},
		"crlf":          {"/a/b.pdf\r\n/c.txt\r\n", []string{"/a/b.pdf", "/c.txt"}},
		"leading space": {" /a/b.pdf\n", []string{" /a/b.pdf"}}, // names may start with a space
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, strings.ReplaceAll(name, " ", "_"))
			if err := os.WriteFile(p, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := readChooserFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	if _, err := readChooserFile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected an error for a missing chooser file")
	}
}

func TestAttachPickedAttachesAllAndReportsFailures(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.csv")
	for p, body := range map[string]string{a: "aaa", b: "x,y\n1,2\n"} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("several files", func(t *testing.T) {
		c := NewCompose(config.AccountConfig{}, nil, nil)
		c = c.attachPicked(filePickerDoneMsg{paths: []string{a, b}})
		if len(c.attachments) != 2 || c.attachments[0].Name != "a.txt" || c.attachments[1].Name != "b.csv" {
			t.Fatalf("attachments = %+v", c.attachments)
		}
		if c.isErr || c.statusMsg != "attached 2 files" {
			t.Fatalf("status = %q (err=%v)", c.statusMsg, c.isErr)
		}
	})

	t.Run("one missing among valid", func(t *testing.T) {
		c := NewCompose(config.AccountConfig{}, nil, nil)
		c = c.attachPicked(filePickerDoneMsg{paths: []string{a, filepath.Join(dir, "gone"), b}})
		if len(c.attachments) != 2 {
			t.Fatalf("a failing path must not stop the others, got %d attachments", len(c.attachments))
		}
		if !c.isErr || !strings.Contains(c.statusMsg, "failed 1") {
			t.Fatalf("status = %q (err=%v)", c.statusMsg, c.isErr)
		}
	})

	t.Run("directory in list", func(t *testing.T) {
		c := NewCompose(config.AccountConfig{}, nil, nil)
		c = c.attachPicked(filePickerDoneMsg{paths: []string{dir}})
		if len(c.attachments) != 0 || !c.isErr {
			t.Fatalf("a directory must not attach: %+v, status %q", c.attachments, c.statusMsg)
		}
	})
}

func TestAttachPickedWithNoPaths(t *testing.T) {
	c := NewCompose(config.AccountConfig{}, nil, nil)

	got := c.attachPicked(filePickerDoneMsg{})
	if got.isErr || got.statusMsg != "no file chosen" {
		t.Fatalf("quit without choosing: status %q (err=%v)", got.statusMsg, got.isErr)
	}

	got = c.attachPicked(filePickerDoneMsg{runErr: errors.New("exit status 127")})
	if !got.isErr || !strings.Contains(got.statusMsg, "exit status 127") {
		t.Fatalf("failed command must be shown: status %q (err=%v)", got.statusMsg, got.isErr)
	}

	got = c.attachPicked(filePickerDoneMsg{rdErr: errors.New("no such file")})
	if !got.isErr || !strings.Contains(got.statusMsg, "no such file") {
		t.Fatalf("unreadable chooser file must be shown: status %q (err=%v)", got.statusMsg, got.isErr)
	}
}

func TestStartAttachPickBuiltInWhenUnset(t *testing.T) {
	t.Cleanup(func() { setComposeFilePickerCmd("") })
	setComposeFilePickerCmd("")
	c := NewCompose(config.AccountConfig{}, nil, nil)
	c, cmd := c.startAttachPick()
	if cmd != nil || !c.picker.active {
		t.Fatalf("unset file_picker_cmd must open the built-in picker (cmd=%v active=%v)", cmd, c.picker.active)
	}
}

func TestStartAttachPickRejectsCommandWithoutPlaceholder(t *testing.T) {
	if !externalPickerSupported() {
		t.Skip("external picker is Unix only")
	}
	t.Cleanup(func() { setComposeFilePickerCmd("") })
	setComposeFilePickerCmd("yazi")
	c := NewCompose(config.AccountConfig{}, nil, nil)
	c, cmd := c.startAttachPick()
	if cmd != nil || c.picker.active || !c.isErr || !strings.Contains(c.statusMsg, "{chooser}") {
		t.Fatalf("want an error naming {chooser}: cmd=%v active=%v status=%q", cmd, c.picker.active, c.statusMsg)
	}
}

func TestStartAttachPickReturnsExecCommandWhenSet(t *testing.T) {
	if !externalPickerSupported() {
		t.Skip("external picker is Unix only")
	}
	t.Cleanup(func() { setComposeFilePickerCmd("") })
	setComposeFilePickerCmd("true {chooser}")
	c := NewCompose(config.AccountConfig{}, nil, nil)
	c, cmd := c.startAttachPick()
	if cmd == nil || c.picker.active {
		t.Fatalf("set file_picker_cmd must return an exec command and leave the built-in picker closed (cmd=%v active=%v)", cmd, c.picker.active)
	}
}

func TestComposeUpdateHandlesFilePickerDone(t *testing.T) {
	p := filepath.Join(t.TempDir(), "n.txt")
	if err := os.WriteFile(p, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCompose(config.AccountConfig{}, nil, nil)
	c, _, exit := c.Update(filePickerDoneMsg{paths: []string{p}}, DefaultKeys)
	if exit || len(c.attachments) != 1 || c.attachments[0].Name != "n.txt" {
		t.Fatalf("exit=%v attachments=%+v", exit, c.attachments)
	}
}
