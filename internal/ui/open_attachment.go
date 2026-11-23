package ui

import (
	"fmt"
	"strings"

	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/mailapp"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// openAttachPicker is the list shown when a message has several attachments.
type openAttachPicker struct {
	items  []db.Attachment
	cursor int
}

// AttachmentOpenedMsg reports the outcome of opening one attachment.
type AttachmentOpenedMsg struct {
	Name string
	Err  error
}

// openAttachmentFile is mailapp.OpenAttachment, replaceable in tests so they
// never launch a real program.
var openAttachmentFile = mailapp.OpenAttachment

// startOpenAttachment opens the open message's only attachment, or lists them
// when there are several. Embedded (inline) images are not offered: they are
// part of the message text.
func (m Model) startOpenAttachment() (tea.Model, tea.Cmd) {
	items := mailapp.OpenableAttachments(m.contentAttachments)
	if m.contentMessageID == 0 || len(items) == 0 {
		m.setStatus("no attachment to open", false)
		return m, m.clearStatusCmd()
	}
	if len(items) == 1 {
		return m, m.openAttachmentCmd(items[0])
	}
	m.openAttach = openAttachPicker{items: items}
	m.overlay = overlayOpenAttach
	return m, nil
}

// openAttachmentCmd reads the contents at the point of use (the reading pane
// keeps metadata only) and opens a private copy.
func (m Model) openAttachmentCmd(a db.Attachment) tea.Cmd {
	database := m.db
	opts := mailapp.OpenAttachmentOptions{Command: m.cfg.Display.AttachmentOpenCmd}
	return func() tea.Msg {
		_, err := openAttachmentFile(database, a, opts)
		return AttachmentOpenedMsg{Name: mailapp.AttachmentFileName(a), Err: err}
	}
}

func (m Model) handleOpenAttachPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case keyMatches(msg, m.keys.Cancel):
		m.openAttach = openAttachPicker{}
		m.overlay = overlayNone
	case keyMatches(msg, m.keys.Up):
		if m.openAttach.cursor > 0 {
			m.openAttach.cursor--
		}
	case keyMatches(msg, m.keys.Down):
		if m.openAttach.cursor < len(m.openAttach.items)-1 {
			m.openAttach.cursor++
		}
	case keyMatches(msg, m.keys.Confirm):
		if m.openAttach.cursor < 0 || m.openAttach.cursor >= len(m.openAttach.items) {
			return m, nil
		}
		a := m.openAttach.items[m.openAttach.cursor]
		m.openAttach = openAttachPicker{}
		m.overlay = overlayNone
		return m, m.openAttachmentCmd(a)
	}
	return m, nil
}

func (m Model) renderOpenAttachPicker(width, height int, chrome managerChrome) string {
	heading := lipgloss.NewStyle().
		Background(chrome.baseBg).
		Foreground(chrome.muted).
		Width(width).
		Padding(0, 2).
		Render(clampView("opens a temporary copy with your default program", max(1, width-4), 1, chrome.baseBg))

	labelW := max(1, width-2) // minus the 2-cell rail
	listH := max(1, height-6)
	start := 0
	if m.openAttach.cursor >= listH {
		start = m.openAttach.cursor - listH + 1
	}
	end := min(start+listH, len(m.openAttach.items))

	rows := make([]string, 0, listH)
	for i := start; i < end; i++ {
		a := m.openAttach.items[i]
		label := fmt.Sprintf("📄 %s  (%s)", mailapp.AttachmentFileName(a), formatFileSize(a.Size))
		cell := lipgloss.NewStyle().Background(chrome.baseBg).Foreground(chrome.text).Render(" " + truncate(label, max(1, labelW-1)))
		rows = append(rows, softRail(chrome, i == m.openAttach.cursor, chrome.baseBg)+padStyled(cell, labelW, chrome.baseBg))
	}
	for len(rows) < listH {
		rows = append(rows, lipgloss.NewStyle().Background(chrome.baseBg).Width(width).Render(""))
	}
	hints := renderSoftHints(width, chrome, "↵", "open", "esc", "cancel")
	return lipgloss.JoinVertical(lipgloss.Left, heading, lipgloss.JoinVertical(lipgloss.Left, rows...), hints)
}

// attachmentOpenedStatus words the result for the status line.
func attachmentOpenedStatus(msg AttachmentOpenedMsg) (string, bool) {
	if msg.Err != nil {
		return "open attachment failed: " + strings.TrimSpace(msg.Err.Error()), true
	}
	return "opened " + msg.Name, false
}
