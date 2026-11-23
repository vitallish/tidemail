package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/mailapp"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// quoteFixture opens a reply to a cached HTML message with a table, with the
// quote marker setting on and the lookup reading the test database.
func quoteFixture(t *testing.T) (ComposeModel, db.Message) {
	t.Helper()
	database, err := db.OpenPath(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	acc, _ := database.AddAccount("", "Work", "")
	box, _ := database.UpsertMailbox(db.Mailbox{AccountID: acc, Name: "INBOX"})
	original := db.Message{
		MailboxID: box, UID: 1, MessageID: "<orig@x>", Subject: "Budget", From: "Ann Lee <ann@example.com>",
		Date:     time.Date(2026, 3, 10, 9, 5, 0, 0, time.UTC),
		BodyText: "Totals\nItem Qty\nPens 3",
		BodyHTML: `<p>Totals for <b>March</b></p><table><tr><th>Item</th><th>Qty</th></tr><tr><td>Pens</td><td>3</td></tr></table>`,
	}
	if err := database.UpsertMessage(original); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := database.MessageByMessageID("<orig@x>")
	if err != nil || !ok {
		t.Fatal("fixture message missing", err)
	}
	setComposeQuoteDB(database)
	setComposeReplyMarker(true)
	t.Cleanup(func() { setComposeQuoteDB(nil); setComposeReplyMarker(false) })
	acfg := config.AccountConfig{ID: "a", Name: "Work", User: "me@example.com"}
	c := NewReply(stored, acfg, []config.AccountConfig{acfg}, nil)
	c.bodyInput.SetSize(60, 12)
	return c, stored
}

func TestReplyStartsWithTheMarkerWhenTheSettingIsOn(t *testing.T) {
	c, _ := quoteFixture(t)
	body := c.bodyInput.Value()
	if !strings.Contains(body, "\n"+mailapp.QuoteMarker+"\n") || strings.Contains(body, "wrote:") || strings.Contains(body, "> ") {
		t.Fatalf("body %q: want the marker and no pasted quote", body)
	}
	if !strings.Contains(body, mailapp.SignatureMarker) {
		t.Errorf("the signature marker must still be there: %q", body)
	}
	setComposeReplyMarker(false)
	acfg := config.AccountConfig{ID: "a", Name: "Work", User: "me@example.com"}
	old := NewReply(db.Message{MessageID: "<o@x>", From: "Bob", BodyText: "hello"}, acfg, []config.AccountConfig{acfg}, nil)
	if got := old.bodyInput.Value(); strings.Contains(got, mailapp.QuoteMarker) || !strings.Contains(got, "> hello") {
		t.Errorf("with the setting off a reply must keep today's text quote: %q", got)
	}
}

func TestSendingResolvesTheMarkerFromTheCachedOriginal(t *testing.T) {
	c, _ := quoteFixture(t)
	c.toInput.SetValue("ann@example.com")
	msg, err := mailapp.PrepareCompose(c.selectedAccount(), c.toDraftRecord(), quoteOption(c.inReplyTo))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.HTMLBody, "<table") || !strings.Contains(msg.HTMLBody, "Ann Lee") || !strings.Contains(msg.Body, "> Item | Qty") {
		t.Errorf("html:\n%s\ntext:\n%s", msg.HTMLBody, msg.Body)
	}
	// Without the lookup (e.g. a send path that forgot it) the marker is an error,
	// never silently dropped or sent as a glyph.
	if _, err := mailapp.PrepareCompose(c.selectedAccount(), c.toDraftRecord()); err == nil || strings.Contains(err.Error(), "panic") {
		t.Errorf("no lookup: %v", err)
	}
}

func TestMarkerLineIsDrawnAsALabel(t *testing.T) {
	c, _ := quoteFixture(t)
	plain := c.bodyInput.View()
	labelled := labelQuoteMarker(plain, c.bodyInput.PlaceholderStyle, 60)
	if !strings.Contains(labelled, "quoted message") || strings.Contains(labelled, mailapp.QuoteMarker) {
		t.Fatalf("not labelled:\n%q", labelled)
	}
	pl, ll := strings.Split(plain, "\n"), strings.Split(labelled, "\n")
	if len(pl) != len(ll) {
		t.Fatalf("line count changed %d -> %d", len(pl), len(ll))
	}
	for i := range pl {
		if strings.Contains(pl[i], mailapp.QuoteMarker) {
			if w := lipgloss.Width(ll[i]); w != lipgloss.Width(quoteMarkerLabel) {
				t.Errorf("label line is %d columns, want %d", w, lipgloss.Width(quoteMarkerLabel))
			}
		} else if pl[i] != ll[i] {
			t.Errorf("line %d changed: %q -> %q", i, pl[i], ll[i])
		}
	}
	// Too narrow for the label, or no marker: returned untouched.
	if got := labelQuoteMarker(plain, c.bodyInput.PlaceholderStyle, 10); got != plain {
		t.Error("a too-narrow editor must keep the plain glyph")
	}
	if got := labelQuoteMarker("just text", c.bodyInput.PlaceholderStyle, 60); got != "just text" {
		t.Errorf("%q", got)
	}
}

func TestF3ShowsTheQuotedMessageReadOnlyAndYCopiesIt(t *testing.T) {
	c, _ := quoteFixture(t)
	var copied string
	old := copyQuoteText
	copyQuoteText = func(s string) error { copied = s; return nil }
	t.Cleanup(func() { copyQuoteText = old })
	before := c.bodyInput.Value()

	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyF3}, DefaultKeys)
	if !c.quoteOpen {
		t.Fatalf("F3 did not open the view: %q", c.statusMsg)
	}
	view := c.quoteViewBody()
	for _, want := range []string{"Ann Lee", "wrote:", "Totals for March", "Item | Qty", "Pens | 3"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	// Typing into the view must not touch the message.
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")}, DefaultKeys)
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}, DefaultKeys)
	if copied == "" || !strings.Contains(copied, "Pens | 3") || !strings.Contains(c.statusMsg, "copied") {
		t.Errorf("copied %q status %q", copied, c.statusMsg)
	}
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyEsc}, DefaultKeys)
	if c.quoteOpen || c.bodyInput.Value() != before {
		t.Errorf("closing changed the message: open=%v body %q", c.quoteOpen, c.bodyInput.Value())
	}
}

func TestF3WithoutAMarkerOrAnOriginalSaysSo(t *testing.T) {
	c, _ := quoteFixture(t)
	c.bodyInput.SetValue("no marker here")
	if c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyF3}, DefaultKeys); c.quoteOpen || !strings.Contains(c.statusMsg, "no quoted-message marker") {
		t.Errorf("no marker: open=%v status=%q", c.quoteOpen, c.statusMsg)
	}
	c, _ = quoteFixture(t)
	setComposeQuoteDB(nil)
	if c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyF3}, DefaultKeys); c.quoteOpen || !c.isErr {
		t.Errorf("no original: open=%v err=%v status=%q", c.quoteOpen, c.isErr, c.statusMsg)
	}
}

func TestEPressedInTheViewExpandsTheQuoteIntoEditableMarkdown(t *testing.T) {
	c, _ := quoteFixture(t)
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyF3}, DefaultKeys)
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")}, DefaultKeys)
	body := c.bodyInput.Value()
	if c.quoteOpen || strings.Contains(body, mailapp.QuoteMarker) {
		t.Fatalf("expand left the marker or the view: open=%v body %q", c.quoteOpen, body)
	}
	for _, want := range []string{"Ann Lee <ann@example.com> wrote:", "> Totals for **March**", "> | Item | Qty |", "> | --- | --- |", "> | Pens | 3 |", mailapp.SignatureMarker} {
		if !strings.Contains(body, want) {
			t.Errorf("expanded body missing %q:\n%s", want, body)
		}
	}
	if !c.dirty {
		t.Error("expanding must mark the draft changed")
	}
	// From here on it is ordinary text: it sends as a Markdown blockquote with a
	// real table, with no lookup needed.
	c.toInput.SetValue("ann@example.com")
	msg, err := mailapp.PrepareCompose(c.selectedAccount(), c.toDraftRecord())
	if err != nil || !strings.Contains(msg.HTMLBody, "<blockquote>") || !strings.Contains(msg.HTMLBody, "<table") {
		t.Errorf("expanded quote sends as %v\n%s", err, msg.HTMLBody)
	}
	// Nothing left to expand now.
	if c = c.expandQuote(); !strings.Contains(c.statusMsg, "no quoted-message marker") {
		t.Errorf("second expand: %q", c.statusMsg)
	}
}

func TestQuoteMarkerIsNotSentToTheGrammarChecker(t *testing.T) {
	reply, quote := splitReplyAndQuote("Thanks!\n\n" + mailapp.SignatureMarker + "\n\n" + mailapp.QuoteMarker + "\n")
	if strings.Contains(reply, mailapp.QuoteMarker) || !strings.Contains(quote, mailapp.QuoteMarker) {
		t.Errorf("reply %q quote %q: the marker belongs with the quote", reply, quote)
	}
	if reply+quote != "Thanks!\n\n"+mailapp.SignatureMarker+"\n\n"+mailapp.QuoteMarker+"\n" {
		t.Errorf("split does not round-trip: %q + %q", reply, quote)
	}
}

func TestCtrlZRightAfterExpandingPutsTheMarkerBack(t *testing.T) {
	c, _ := quoteFixture(t)
	c.focusedField = composeFieldBody
	original := c.bodyInput.Value()
	c = c.expandQuote()
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyCtrlZ}, DefaultKeys)
	if c.bodyInput.Value() != original || !strings.Contains(c.statusMsg, "restored") {
		t.Fatalf("undo gave %q (status %q)", c.bodyInput.Value(), c.statusMsg)
	}
	// After any further edit the stale text must not come back.
	c = c.expandQuote()
	c.bodyInput.SetValue(c.bodyInput.Value() + "typed after")
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyCtrlZ}, DefaultKeys)
	if !strings.Contains(c.bodyInput.Value(), "typed after") && strings.Contains(c.bodyInput.Value(), mailapp.QuoteMarker) {
		t.Errorf("an edited body was overwritten by the pre-expansion text: %q", c.bodyInput.Value())
	}
}
