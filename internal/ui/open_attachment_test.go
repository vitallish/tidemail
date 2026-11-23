package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/mailapp"
	tea "github.com/charmbracelet/bubbletea"
)

// stubOpen replaces the opener so no test starts a real program, and records
// what would have been opened.
func stubOpen(t *testing.T, err error) *[]db.Attachment {
	t.Helper()
	var opened []db.Attachment
	orig := openAttachmentFile
	openAttachmentFile = func(_ *db.DB, a db.Attachment, _ mailapp.OpenAttachmentOptions) (string, error) {
		opened = append(opened, a)
		return "/tmp/x/" + a.Filename, err
	}
	t.Cleanup(func() { openAttachmentFile = orig })
	return &opened
}

func contentModelWithAttachments(t *testing.T, atts ...db.Attachment) Model {
	t.Helper()
	m, _, _ := newOrderedModel(t, nil, "")
	m.contentMessageID = 1
	m.contentAttachments = atts
	m.focused = paneContent
	return m
}

func pressOpenKey(m Model, r rune) (Model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return next.(Model), cmd
}

func TestOpenAttachmentKeyOpensTheOnlyAttachmentDirectly(t *testing.T) {
	opened := stubOpen(t, nil)
	m := contentModelWithAttachments(t,
		db.Attachment{ID: 1, Filename: "plan.pdf", Size: 10},
		db.Attachment{ID: 2, Filename: "logo.png", Inline: true},
	)
	m, cmd := pressOpenKey(m, 'w')
	if m.overlay != overlayNone {
		t.Fatalf("a single attachment needs no chooser, overlay = %v", m.overlay)
	}
	if cmd == nil {
		t.Fatal("expected an open command")
	}
	msg, ok := cmd().(AttachmentOpenedMsg)
	if !ok || msg.Err != nil || msg.Name != "plan.pdf" {
		t.Fatalf("got %#v", msg)
	}
	if len(*opened) != 1 || (*opened)[0].ID != 1 {
		t.Fatalf("opened %+v", *opened)
	}
}

func TestOpenAttachmentKeyListsSeveralAndOpensTheChosenOne(t *testing.T) {
	opened := stubOpen(t, nil)
	m := contentModelWithAttachments(t,
		db.Attachment{ID: 1, Filename: "a.pdf"},
		db.Attachment{ID: 2, Filename: "b.csv"},
	)
	m, _ = pressOpenKey(m, 'w')
	if m.overlay != overlayOpenAttach || len(m.openAttach.items) != 2 {
		t.Fatalf("overlay = %v, items = %d", m.overlay, len(m.openAttach.items))
	}
	if view := m.renderOpenAttachPicker(60, 20, newManagerChrome(60, m.styles.Theme, m.styles.PlainUI)); !strings.Contains(view, "a.pdf") || !strings.Contains(view, "b.csv") {
		t.Fatalf("chooser does not list both attachments:\n%s", view)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.overlay != overlayNone || cmd == nil {
		t.Fatalf("overlay = %v, cmd nil = %v", m.overlay, cmd == nil)
	}
	cmd()
	if len(*opened) != 1 || (*opened)[0].ID != 2 {
		t.Fatalf("opened %+v, want attachment 2", *opened)
	}
}

func TestOpenAttachmentChooserEscCancels(t *testing.T) {
	opened := stubOpen(t, nil)
	m := contentModelWithAttachments(t, db.Attachment{ID: 1, Filename: "a"}, db.Attachment{ID: 2, Filename: "b"})
	m, _ = pressOpenKey(m, 'w')
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.overlay != overlayNone || cmd != nil || len(*opened) != 0 {
		t.Fatalf("esc must close without opening (overlay %v, opened %d)", m.overlay, len(*opened))
	}
}

func TestOpenAttachmentWithNothingToOpenSaysSo(t *testing.T) {
	stubOpen(t, nil)
	for name, m := range map[string]Model{
		"none":        contentModelWithAttachments(t),
		"inline only": contentModelWithAttachments(t, db.Attachment{ID: 1, Filename: "logo.png", Inline: true}),
	} {
		m, _ = pressOpenKey(m, 'w')
		if m.overlay != overlayNone || !strings.Contains(m.statusMsg, "no attachment") {
			t.Errorf("%s: overlay %v, status %q", name, m.overlay, m.statusMsg)
		}
	}
}

func TestOpenAttachmentKeyIgnoredOutsideTheReader(t *testing.T) {
	opened := stubOpen(t, nil)
	m := contentModelWithAttachments(t, db.Attachment{ID: 1, Filename: "a.pdf"})
	m.focused = paneMessages
	m, cmd := pressOpenKey(m, 'w')
	if cmd != nil || m.overlay != overlayNone || len(*opened) != 0 {
		t.Fatal("w must only act while the reader is focused")
	}
}

func TestAttachmentOpenedMsgReportsFailure(t *testing.T) {
	m, _, _ := newOrderedModel(t, nil, "")
	next, _ := m.Update(AttachmentOpenedMsg{Name: "a.pdf", Err: errors.New("no handler")})
	if got := next.(Model).statusMsg; !strings.Contains(got, "open attachment failed") || !strings.Contains(got, "no handler") {
		t.Fatalf("status = %q", got)
	}
	next, _ = m.Update(AttachmentOpenedMsg{Name: "a.pdf"})
	if got := next.(Model).statusMsg; got != "opened a.pdf" {
		t.Fatalf("status = %q", got)
	}
}

func TestCommandPaletteOffersOpenAttachment(t *testing.T) {
	stubOpen(t, nil)
	m := contentModelWithAttachments(t, db.Attachment{ID: 1, Filename: "a.pdf"})
	var found bool
	for _, item := range m.commandItems() {
		if item.id == "open-attachment" {
			found = true
			if !item.enabled {
				t.Error("open-attachment should be enabled with an attachment open")
			}
		}
	}
	if !found {
		t.Fatal("palette has no open-attachment entry")
	}
	got, cmd := m.executeCommand("open-attachment")
	if got.(Model).overlay != overlayNone || cmd == nil {
		t.Fatal("palette entry did not start the open")
	}
	m.contentAttachments = nil
	for _, item := range m.commandItems() {
		if item.id == "open-attachment" && item.enabled {
			t.Error("open-attachment must be disabled without attachments")
		}
	}
}
