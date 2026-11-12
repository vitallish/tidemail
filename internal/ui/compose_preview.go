package ui

import (
	"strings"

	"github.com/allisonhere/tidemail/internal/mailapp"
	tea "github.com/charmbracelet/bubbletea"
)

// composeBrowser is the user's [display] browser command, injected from config
// the same way editorVimMode is. Empty means the system default browser.
var composeBrowser string

// setComposeBrowser sets the browser command used by the compose preview.
func setComposeBrowser(cmd string) { composeBrowser = strings.TrimSpace(cmd) }

// draftPreviewMsg reports the outcome of opening the draft preview.
type draftPreviewMsg struct{ err error }

// previewDraft opens the HTML part this draft would be sent with in the
// browser. It builds the HTML through the same function sending uses, so what
// is shown is what goes out. It neither saves nor sends, and recipients are not
// required, so a draft can be previewed before it is addressed.
func (c ComposeModel) previewDraft() (ComposeModel, tea.Cmd) {
	html, err := mailapp.ComposeHTML(c.selectedAccount(), c.bodyInput.Value())
	if err != nil {
		c.statusMsg = err.Error()
		c.isErr = true
		return c, nil
	}
	subject, browserCmd := c.subjectInput.Value(), composeBrowser
	c.statusMsg = "Opening preview in browser…"
	c.isErr = false
	return c, func() tea.Msg {
		return draftPreviewMsg{err: openDraftPreview(subject, html, browserCmd)}
	}
}
