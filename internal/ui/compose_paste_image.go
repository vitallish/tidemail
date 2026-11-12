package ui

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/allisonhere/tidemail/internal/clipimage"
	tea "github.com/charmbracelet/bubbletea"
)

// clipboardTimeout bounds one look at the system clipboard.
const clipboardTimeout = 5 * time.Second

// ClipboardImageMsg carries an image read from the system clipboard. Forced is
// true when the user asked for the image (the palette command) rather than
// pressing Ctrl+V.
type ClipboardImageMsg struct {
	Image  clipimage.Image
	Err    error
	Forced bool
}

// clipEnv is the clipboard environment. It is a variable so tests can supply a
// fake clipboard and never touch the real one.
var clipEnv = func() clipimage.Env { return clipimage.DefaultEnv(os.Getenv) }

// clipboardPasteCmd and clipboardImageCmd are variables for the same reason as
// clipboardReadCmd: tests replace them.
var (
	clipboardPasteCmd = pasteClipboardCmd
	clipboardImageCmd = readClipboardImageCmd
)

// pasteClipboardCmd is what Ctrl+V does in compose. When the clipboard holds an
// image and no text it reads the image; in every other case, including when the
// clipboard cannot be inspected, it pastes text exactly as before.
func pasteClipboardCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
		defer cancel()
		env := clipEnv()
		if kind, err := env.Probe(ctx); err == nil && kind.ImageOnly() {
			img, err := env.Read(ctx)
			return ClipboardImageMsg{Image: img, Err: err}
		}
		return clipboardReadCmd()()
	}
}

// readClipboardImageCmd reads the clipboard image on request (the palette).
func readClipboardImageCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
		defer cancel()
		img, err := clipEnv().Read(ctx)
		return ClipboardImageMsg{Image: img, Err: err, Forced: true}
	}
}

var pastedName = regexp.MustCompile(`^image-\d+\.(png|jpg|gif)$`)

// nextImageName returns image-N.ext for the smallest N not already attached.
func (c ComposeModel) nextImageName(ext string) string {
	taken := map[string]bool{}
	for _, a := range c.attachments {
		taken[strings.ToLower(a.Name)] = true
	}
	for n := 1; ; n++ {
		if name := fmt.Sprintf("image-%d.%s", n, ext); !taken[name] {
			return name
		}
	}
}

// pasteImage attaches a clipboard image and writes ![](name) at the cursor in
// the body, moving focus there first. The same pixels pasted again reuse the
// earlier pasted attachment. It returns the status line to show.
func (c ComposeModel) pasteImage(img clipimage.Image) (ComposeModel, string) {
	sum := sha256.Sum256(img.Data)
	name := ""
	for _, a := range c.attachments {
		if pastedName.MatchString(a.Name) && sha256.Sum256(a.Data) == sum {
			name = a.Name
			break
		}
	}
	status := ""
	if name == "" {
		name = c.nextImageName(img.Ext)
		c.attachments = append(c.attachments, attachmentFile{Name: name, Data: img.Data})
		status = fmt.Sprintf("pasted %s (%s)", name, humanSize(len(img.Data)))
	} else {
		status = fmt.Sprintf("%s is already attached; added another reference", name)
	}
	c.insertIntoBody("![](" + name + ")")
	return c, status
}

// insertIntoBody writes text into the message body at the cursor as pasted
// text (so Vim mode inserts it rather than running it as commands), after moving
// focus to the body.
func (c *ComposeModel) insertIntoBody(text string) {
	c.toInput.Blur()
	c.ccInput.Blur()
	c.bccInput.Blur()
	c.subjectInput.Blur()
	c.focusedField = composeFieldBody
	c.bodyInput.Focus()
	if c.bodyInput.vimMode() {
		c.bodyInput.EnterInsert()
	}
	c.bodyInput, _ = c.bodyInput.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: true})
}
