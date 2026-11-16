package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/mailapp"
	"github.com/allisonhere/tidemail/internal/previewserver"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	// previewTickEvery is how often the live preview checks the draft for
	// changes. It also keeps the preview server from idling out.
	previewTickEvery = time.Second
	// previewServerIdle is how long the preview server survives without a tick,
	// which is how it ends once the composer that owns it has closed.
	previewServerIdle = 10 * time.Second
)

var (
	// composeBrowser is the user's [display] browser command, injected from
	// config the same way editorVimMode is. Empty means the system default.
	composeBrowser string
	// composePreviewServer is [display] compose_preview_server: F4 serves a live
	// preview from a local server instead of writing a static file.
	composePreviewServer bool
)

// setComposeBrowser sets the browser command used by the compose preview.
func setComposeBrowser(cmd string) { composeBrowser = strings.TrimSpace(cmd) }

// setComposePreviewServer turns the live preview server on or off for new previews.
func setComposePreviewServer(on bool) { composePreviewServer = on }

// startPreviewServer is a variable so tests can make the server fail to start.
var startPreviewServer = func() (*previewserver.Server, error) {
	return previewserver.Start(previewServerIdle)
}

// draftPreviewMsg reports the outcome of opening the draft preview. On success
// note, when set, replaces the usual status text.
type draftPreviewMsg struct {
	err  error
	note string
}

// draftPreviewTickMsg drives the live preview while the composer is open.
type draftPreviewTickMsg struct{}

// livePreview is the state of the draft preview opened with F4.
type livePreview struct {
	path    string                // static mode: the file the browser has open
	srv     *previewserver.Server // server mode: the running preview server
	live    bool                  // a tick chain is running (server mode)
	subject string                // what the server last received, to spot changes
	body    string                // (raw draft text, so an unchanged tick converts nothing)
	atts    int                   // attachment count then (attaching or removing changes the images)
	problem string                // the image problems last reported, to say so only on a change
}

func previewTick() tea.Cmd {
	return tea.Tick(previewTickEvery, func(time.Time) tea.Msg { return draftPreviewTickMsg{} })
}

// openPreviewCmd opens target (a file path or a URL) in the configured browser.
func openPreviewCmd(target, note string) tea.Cmd {
	browserCmd := composeBrowser
	return func() tea.Msg {
		if err := openEmailBrowser(browserCmd, target); err != nil {
			return draftPreviewMsg{err: fmt.Errorf("could not launch browser: %w", err)}
		}
		return draftPreviewMsg{note: note}
	}
}

// previewDraft shows the HTML part this draft would be sent with in the
// browser. The HTML comes from the function sending uses, so what is shown is
// what goes out. It neither saves nor sends, and recipients are not required.
//
// By default it writes a static file and opens it; pressing it again rewrites the
// file and opens it again. With compose_preview_server on it serves the page
// from a local server instead and keeps it updated while the composer is open;
// if the server cannot start it falls back to the static file and says so.
func (c ComposeModel) previewDraft() (ComposeModel, tea.Cmd) {
	body, subject := c.bodyInput.Value(), c.subjectInput.Value()
	html, problems, err := mailapp.ComposeHTML(c.selectedAccount(), body, c.draftAttachments())
	if err != nil {
		c.statusMsg = err.Error()
		c.isErr = true
		return c, nil
	}
	c.preview.problem = strings.Join(problems, "; ")
	fallback := ""
	if composePreviewServer {
		srv, err := c.ensurePreviewServer()
		if err == nil {
			srv.Set(previewTitle(subject), html)
			c.preview.subject, c.preview.body, c.preview.atts = subject, body, len(c.attachments)
			c.statusMsg, c.isErr = "Opening live preview in browser…", false
			open := openPreviewCmd(srv.URL(), withProblems("Live preview opened in browser; it updates as you type", problems))
			if c.preview.live {
				return c, open // a tick chain is already running
			}
			c.preview.live = true
			return c, tea.Batch(open, previewTick())
		}
		fallback = fmt.Sprintf("Preview opened as a static file (live preview server unavailable: %v)", err)
	}
	if c.preview.path == "" {
		path, err := newPreviewPath()
		if err != nil {
			c.statusMsg, c.isErr = fmt.Sprintf("preview: %v", err), true
			return c, nil
		}
		c.preview.path = path
	}
	if err := writePreviewFile(c.preview.path, previewDocument(subject, html)); err != nil {
		c.statusMsg, c.isErr = fmt.Sprintf("preview: %v", err), true
		return c, nil
	}
	c.statusMsg, c.isErr = "Opening preview in browser…", false
	note := fallback
	if note == "" {
		note = "Preview opened in browser; press " + DefaultKeys.PreviewDraft.Help().Key + " again to refresh it"
	}
	return c, openPreviewCmd(c.preview.path, withProblems(note, problems))
}

// ensurePreviewServer returns the composer's running preview server, starting
// one when there is none or it has shut down.
func (c *ComposeModel) ensurePreviewServer() (*previewserver.Server, error) {
	if c.preview.srv != nil && c.preview.srv.Alive() {
		return c.preview.srv, nil
	}
	srv, err := startPreviewServer()
	if err != nil {
		return nil, err
	}
	c.preview.srv = srv
	return srv, nil
}

// refreshLivePreview pushes the draft to the preview server when it changed. It
// also touches the server, which keeps it from idling out. A body that does not
// convert (two signature markers while typing) leaves the last good page up.
func (c ComposeModel) refreshLivePreview() ComposeModel {
	srv := c.preview.srv
	if srv == nil || !srv.Alive() {
		c.preview.live = false
		c.statusMsg, c.isErr = "live preview stopped; press "+DefaultKeys.PreviewDraft.Help().Key+" to start it again", true
		return c
	}
	srv.Touch()
	body, subject := c.bodyInput.Value(), c.subjectInput.Value()
	if body == c.preview.body && subject == c.preview.subject && len(c.attachments) == c.preview.atts {
		return c
	}
	c.preview.subject, c.preview.body, c.preview.atts = subject, body, len(c.attachments) // do not retry until it changes again
	html, problems, err := mailapp.ComposeHTML(c.selectedAccount(), body, c.draftAttachments())
	if err != nil {
		return c
	}
	srv.Set(previewTitle(subject), html)
	// Say so when an image problem appears or goes away, not on every tick.
	if problem := strings.Join(problems, "; "); problem != c.preview.problem {
		c.preview.problem = problem
		if problem != "" {
			c.statusMsg, c.isErr = "preview: "+problem, true
		} else {
			c.statusMsg, c.isErr = "preview: images fixed", false
		}
	}
	return c
}

// withProblems appends image problems to a status note.
func withProblems(note string, problems []string) string {
	if len(problems) == 0 {
		return note
	}
	return note + ", but " + strings.Join(problems, "; ")
}

// draftAttachments lists the attached files as the draft's attachments, which
// the preview resolves image references against.
func (c ComposeModel) draftAttachments() []db.DraftAttachment {
	out := make([]db.DraftAttachment, 0, len(c.attachments))
	for _, a := range c.attachments {
		out = append(out, db.DraftAttachment{Filename: a.Name, Data: a.Data})
	}
	return out
}
