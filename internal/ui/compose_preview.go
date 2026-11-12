package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/allisonhere/tidemail/internal/mailapp"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	// livePreviewTick is how often an open preview checks the draft for changes.
	livePreviewTick = time.Second
	// livePreviewHeartbeat rewrites an unchanged preview so its live-until time
	// keeps moving ahead while the composer is open.
	livePreviewHeartbeat = time.Minute
	// livePreviewTTL is how long a written page keeps reloading itself. After
	// the composer closes (and stops rewriting) the page goes quiet by then.
	livePreviewTTL = 3 * time.Minute
)

// composeBrowser is the user's [display] browser command, injected from config
// the same way editorVimMode is. Empty means the system default browser.
var composeBrowser string

// setComposeBrowser sets the browser command used by the compose preview.
func setComposeBrowser(cmd string) { composeBrowser = strings.TrimSpace(cmd) }

// draftPreviewMsg reports the outcome of opening the draft preview.
type draftPreviewMsg struct{ err error }

// draftPreviewTickMsg drives the live preview while the composer is open.
type draftPreviewTickMsg struct{}

// livePreview is the state of the draft preview opened with F4.
type livePreview struct {
	path    string    // the file the browser has open; empty until the first F4
	live    bool      // the composer keeps rewriting it
	subject string    // what was last written, to spot changes cheaply
	body    string    // (the raw draft text, so unchanged ticks do no conversion)
	wrote   time.Time // when the file was last written
}

func previewTick() tea.Cmd {
	return tea.Tick(livePreviewTick, func(time.Time) tea.Msg { return draftPreviewTickMsg{} })
}

// previewDraft writes the HTML part this draft would be sent with to a file,
// opens it in the browser and keeps it up to date while the composer is open.
// The HTML comes from the function sending uses, so what is shown is what goes
// out. It neither saves nor sends, and recipients are not required, so a draft
// can be previewed before it is addressed. Pressing it again rewrites the page
// and opens it again, for a closed tab.
func (c ComposeModel) previewDraft() (ComposeModel, tea.Cmd) {
	body, subject := c.bodyInput.Value(), c.subjectInput.Value()
	html, err := mailapp.ComposeHTML(c.selectedAccount(), body)
	if err != nil {
		c.statusMsg = err.Error()
		c.isErr = true
		return c, nil
	}
	if c.preview.path == "" {
		path, err := newPreviewPath()
		if err != nil {
			c.statusMsg = fmt.Sprintf("preview: %v", err)
			c.isErr = true
			return c, nil
		}
		c.preview.path = path
	}
	if err := c.writePreview(html, subject, body); err != nil {
		c.statusMsg = fmt.Sprintf("preview: %v", err)
		c.isErr = true
		return c, nil
	}
	c.statusMsg = "Opening preview in browser…"
	c.isErr = false
	path, browserCmd := c.preview.path, composeBrowser
	open := func() tea.Msg {
		if err := openEmailBrowser(browserCmd, path); err != nil {
			return draftPreviewMsg{err: fmt.Errorf("could not launch browser: %w", err)}
		}
		return draftPreviewMsg{}
	}
	if c.preview.live {
		return c, open // a tick chain is already running
	}
	c.preview.live = true
	return c, tea.Batch(open, previewTick())
}

// writePreview rewrites the preview file and records what it contains.
func (c *ComposeModel) writePreview(html, subject, body string) error {
	now := time.Now()
	if err := writePreviewFile(c.preview.path, previewDocument(subject, html, now.Add(livePreviewTTL))); err != nil {
		return err
	}
	c.preview.subject, c.preview.body, c.preview.wrote = subject, body, now
	return nil
}

// refreshLivePreview rewrites the preview when the draft changed since the last
// write, or when the heartbeat is due. A body that does not convert (two
// signature markers while typing) leaves the last good page in place.
func (c ComposeModel) refreshLivePreview() ComposeModel {
	body, subject := c.bodyInput.Value(), c.subjectInput.Value()
	changed := body != c.preview.body || subject != c.preview.subject
	if !changed && time.Since(c.preview.wrote) < livePreviewHeartbeat {
		return c
	}
	html, err := mailapp.ComposeHTML(c.selectedAccount(), body)
	if err != nil {
		c.preview.subject, c.preview.body = subject, body // do not retry until it changes
		return c
	}
	if err := c.writePreview(html, subject, body); err != nil {
		c.preview.live = false
		c.statusMsg = fmt.Sprintf("live preview stopped: %v", err)
		c.isErr = true
	}
	return c
}
