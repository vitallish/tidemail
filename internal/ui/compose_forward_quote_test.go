package ui

import (
	"strings"
	"testing"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/mailapp"
	tea "github.com/charmbracelet/bubbletea"
)

// forwardFixture forwards a cached HTML message that carries a PDF, with the
// quote marker setting on.
func forwardFixture(t *testing.T) (ComposeModel, db.Message) {
	t.Helper()
	pdf := db.Attachment{Filename: "budget.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.4")}
	_, stored := quoteFixtureHTML(t, `<p>Totals for <b>March</b></p><table><tr><th>Item</th><th>Qty</th></tr><tr><td>Pens</td><td>3</td></tr></table>`, pdf)
	stored.AttachmentData = []db.Attachment{pdf} // the list loads parts; the fixture's stored row does not
	acfg := config.AccountConfig{ID: "a", Name: "Work", User: "me@example.com"}
	c := NewForward(stored, acfg, []config.AccountConfig{acfg}, nil)
	c.bodyInput.SetSize(60, 12)
	return c, stored
}

func TestForwardStartsWithTheMarkerAndRemembersTheOriginal(t *testing.T) {
	c, orig := forwardFixture(t)
	if !strings.Contains(c.bodyInput.Value(), "⁑") || strings.Contains(c.bodyInput.Value(), "Forwarded message") {
		t.Fatalf("body %q", c.bodyInput.Value())
	}
	if c.forwardOf != orig.MessageID || c.inReplyTo != "" || !c.isForward {
		t.Errorf("forwardOf %q inReplyTo %q isForward %v: a forward must not be threaded", c.forwardOf, c.inReplyTo, c.isForward)
	}
	if len(c.attachments) != 1 || c.attachments[0].Name != "budget.pdf" {
		t.Errorf("the original's attachments are still carried over: %+v", c.attachments)
	}
	if c.subjectInput.Value() != "Fwd: Budget" {
		t.Errorf("subject %q", c.subjectInput.Value())
	}
}

func TestForwardWithTheSettingOffKeepsTheTextQuote(t *testing.T) {
	_, orig := forwardFixture(t)
	setComposeReplyMarker(false)
	acfg := config.AccountConfig{ID: "a", Name: "Work", User: "me@example.com"}
	c := NewForward(orig, acfg, []config.AccountConfig{acfg}, nil)
	if c.forwardOf != "" || strings.Contains(c.bodyInput.Value(), "⁑") || !strings.Contains(c.bodyInput.Value(), "---------- Forwarded message ----------") {
		t.Errorf("setting off must give today's text forward: forwardOf %q body %q", c.forwardOf, c.bodyInput.Value())
	}
}

func TestForwardSendResolvesTheMarkerWithAForwardHeader(t *testing.T) {
	c, _ := forwardFixture(t)
	c.toInput.SetValue("bob@example.com")
	msg, err := mailapp.PrepareCompose(c.selectedAccount(), c.toDraftRecord(), c.quoteOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	if msg.InReplyTo != "" {
		t.Errorf("In-Reply-To %q", msg.InReplyTo)
	}
	for _, want := range []string{"Forwarded message", "From: Ann Lee", "Subject: Budget", "<table", "<td>Pens</td>"} {
		if !strings.Contains(msg.HTMLBody, want) {
			t.Errorf("HTML missing %q:\n%s", want, msg.HTMLBody)
		}
	}
	if strings.Contains(msg.HTMLBody, "wrote:") || !strings.Contains(msg.Body, "> Item | Qty") {
		t.Errorf("html:\n%s\ntext:\n%s", msg.HTMLBody, msg.Body)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].Name != "budget.pdf" {
		t.Errorf("attachments: %+v", msg.Attachments)
	}
}

func TestForwardQuoteViewAndExpand(t *testing.T) {
	c, _ := forwardFixture(t)
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyF3}, DefaultKeys)
	if !c.quoteOpen {
		t.Fatalf("F3 did not open the view: %q", c.statusMsg)
	}
	view := c.quoteViewBody()
	for _, want := range []string{"Forwarded message", "From: Ann Lee", "Subject: Budget", "Totals for March"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")}, DefaultKeys)
	body := c.bodyInput.Value()
	if strings.Contains(body, "⁑") || !strings.Contains(body, "---------- Forwarded message ----------") || !strings.Contains(body, "> Totals for **March**") || !strings.Contains(body, "| Pens | 3 |") {
		t.Errorf("expanded forward:\n%s", body)
	}
}

func TestForwardDraftReopensWithItsQuote(t *testing.T) {
	c, orig := forwardFixture(t)
	c.toInput.SetValue("bob@example.com")
	rec := c.toDraftRecord()
	if rec.ForwardOf != orig.MessageID || rec.InReplyTo != "" {
		t.Fatalf("draft record: %+v", rec)
	}
	acfg := config.AccountConfig{ID: "a", Name: "Work", User: "me@example.com"}
	again := NewComposeFromDraft(rec, []config.AccountConfig{acfg}, nil)
	if again.forwardOf != orig.MessageID || !again.isForward || again.softTitle() != c.softTitle() {
		t.Errorf("reopened forward lost its link: forwardOf %q isForward %v title %q", again.forwardOf, again.isForward, again.softTitle())
	}
	if _, err := again.quoteOriginal(); err != nil {
		t.Errorf("the reopened draft cannot find what it forwards: %v", err)
	}
}
