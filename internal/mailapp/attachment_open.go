package mailapp

import (
	"errors"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/allisonhere/tidemail/internal/browser"
	"github.com/allisonhere/tidemail/internal/db"
)

// OpenedAttachmentMaxAge is how long a copy made for opening stays on disk. The
// program that opens it may read the file long after its launcher exits (a PDF
// viewer reloads on change), so copies are pruned by age, not deleted at once.
const OpenedAttachmentMaxAge = 24 * time.Hour

// openedDirPrefix names the per-open folders inside the private directory. The
// prune only touches folders with this prefix.
const openedDirPrefix = "open-"

// OpenAttachmentOptions says how to open one attachment. The zero value uses
// the platform default opener and the per-user temporary directory.
type OpenAttachmentOptions struct {
	// Command is attachment_open_cmd: a shell command line run with sh -c in which
	// {file} stands for the copy's path (appended when absent). Empty uses the
	// platform default opener.
	Command string
	// Dir overrides the private directory holding copies (tests).
	Dir string
	// Run starts the opener. It must not wait for the program to exit.
	Run func(name string, args ...string) error
	// DefaultOpen opens a path with the platform default (tests).
	DefaultOpen func(path string) error
	// Now overrides the clock used for pruning (tests).
	Now func() time.Time
}

// OpenableAttachments are the parts a person would open from a message: those
// that are not embedded in the body. An embedded image is part of the text.
func OpenableAttachments(atts []db.Attachment) []db.Attachment {
	var out []db.Attachment
	for _, a := range atts {
		if !a.Inline {
			out = append(out, a)
		}
	}
	return out
}

// AttachmentPrivateDir returns the owner-only directory that holds copies made
// for opening, creating it on first use. It is per-user, so other local users
// cannot read the copies or plant files for the opener.
func AttachmentPrivateDir() (string, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("tidemail-attachments-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("create attachment directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("check attachment directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("attachment directory %s is not private", dir)
	}
	return dir, nil
}

// PruneOpenedAttachments removes copies older than OpenedAttachmentMaxAge from
// the default private directory. Called at startup and before each open.
func PruneOpenedAttachments() {
	if dir, err := AttachmentPrivateDir(); err == nil {
		pruneOpened(dir, time.Now(), OpenedAttachmentMaxAge)
	}
}

func pruneOpened(dir string, now time.Time, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), openedDirPrefix) {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > maxAge {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// AttachmentFileName is the name a copy is given: the attachment's own name, cut
// down to a single path element so it cannot point elsewhere, with an extension
// from the content type when the name has none (openers choose by extension).
func AttachmentFileName(a db.Attachment) string {
	name := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, a.Filename)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		name = "attachment"
	}
	if filepath.Ext(name) == "" && a.ContentType != "" {
		if mt, _, err := mime.ParseMediaType(a.ContentType); err == nil {
			if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
				name += exts[0]
			}
		}
	}
	return name
}

// WriteAttachmentCopy writes data to a fresh folder under dir (0700) and
// returns the file's path. The file keeps its name, which matters to openers.
func WriteAttachmentCopy(dir string, a db.Attachment, data []byte) (string, error) {
	sub, err := os.MkdirTemp(dir, openedDirPrefix+"*")
	if err != nil {
		return "", fmt.Errorf("create folder for %s: %w", a.Filename, err)
	}
	path := filepath.Join(sub, AttachmentFileName(a))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		_ = os.RemoveAll(sub)
		return "", fmt.Errorf("write %s: %w", a.Filename, err)
	}
	return path, nil
}

// OpenAttachment writes one attachment to a private temporary copy and opens it
// with the configured command or the platform default, without waiting for the
// program to exit. It returns the copy's path. database may be nil when a.Data
// is already filled in (tests, CLI fixtures).
func OpenAttachment(database *db.DB, a db.Attachment, opts OpenAttachmentOptions) (string, error) {
	data := a.Data
	if len(data) == 0 && database != nil && a.ID != 0 {
		var err error
		if data, err = database.GetAttachmentData(a.ID); err != nil {
			return "", fmt.Errorf("read %s: %w", a.Filename, err)
		}
	}
	if len(data) == 0 {
		return "", fmt.Errorf("%s has no content to open", AttachmentFileName(a))
	}
	dir := opts.Dir
	if dir == "" {
		var err error
		if dir, err = AttachmentPrivateDir(); err != nil {
			return "", err
		}
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	pruneOpened(dir, now(), OpenedAttachmentMaxAge)
	path, err := WriteAttachmentCopy(dir, a, data)
	if err != nil {
		return "", err
	}
	if err := launchAttachment(path, opts); err != nil {
		_ = os.RemoveAll(filepath.Dir(path))
		return "", err
	}
	return path, nil
}

func launchAttachment(path string, opts OpenAttachmentOptions) error {
	cmd := strings.TrimSpace(opts.Command)
	if cmd == "" {
		if opts.DefaultOpen != nil {
			return opts.DefaultOpen(path)
		}
		return browser.OpenLocalFile("", path)
	}
	if runtime.GOOS == "windows" && opts.Run == nil {
		return errors.New("attachment_open_cmd needs sh and is not supported on Windows; clear it to use the default program")
	}
	run := opts.Run
	if run == nil {
		run = startDetached
	}
	if !strings.Contains(cmd, "{file}") {
		cmd += " {file}"
	}
	return run("sh", "-c", strings.ReplaceAll(cmd, "{file}", shellQuote(path)))
}

// startDetached starts the program and reaps it in the background, so a
// long-running viewer neither blocks TideMail nor is left as a zombie.
func startDetached(name string, args ...string) error {
	c := exec.Command(name, args...)
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	return nil
}

// shellQuote wraps s in single quotes for sh, so a file name with spaces or
// metacharacters stays one argument.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
