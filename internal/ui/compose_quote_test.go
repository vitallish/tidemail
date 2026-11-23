package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/mailapp"
	"github.com/allisonhere/tidemail/internal/smtp"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// quoteFixture opens a reply to a cached HTML message with a table, with the
// quote marker setting on and the lookup reading the test database.
func quoteFixture(t *testing.T) (ComposeModel, db.Message) {
	t.Helper()
	return quoteFixtureHTML(t, `<p>Totals for <b>March</b></p><table><tr><th>Item</th><th>Qty</th></tr><tr><td>Pens</td><td>3</td></tr></table>`)
}

// quoteFixtureHTML is quoteFixture with the original's HTML body chosen by the test.
func quoteFixtureHTML(t *testing.T, bodyHTML string, parts ...db.Attachment) (ComposeModel, db.Message) {
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
		Date:          time.Date(2026, 3, 10, 9, 5, 0, 0, time.UTC),
		BodyText:      "Totals\nItem Qty\nPens 3",
		BodyHTML:      bodyHTML,
		HasAttachment: len(parts) > 0, AttachmentData: parts,
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
	msg, err := mailapp.PrepareCompose(c.selectedAccount(), c.toDraftRecord(), c.quoteOptions()...)
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

// A long line or an unbroken URL used to be cut off at the right edge of the
// read-only view (#28). It must wrap, stay reachable by scrolling, and the copy
// key must still hand over the original text without added line breaks.
func TestQuoteViewWrapsLongLinesInsteadOfCuttingThem(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("segment/", 15) + "END"
	long := strings.Repeat("a long sentence that keeps going ", 6)
	c, _ := quoteFixtureHTML(t, "<p>"+long+"</p><p>"+url+"</p>")
	var copied string
	old := copyQuoteText
	copyQuoteText = func(s string) error { copied = s; return nil }
	t.Cleanup(func() { copyQuoteText = old })
	c.bodyInput.SetSize(40, 6)

	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyF3}, DefaultKeys)
	rows := c.quoteRows()
	for _, row := range rows {
		if w := ansi.StringWidth(row); w > 40 {
			t.Fatalf("row is %d cells wide, text area is 40: %q", w, row)
		}
	}
	if joined := strings.Join(rows, ""); !strings.Contains(joined, url) {
		t.Errorf("the URL must survive wrapping, row by row, in order:\n%s", strings.Join(rows, "\n"))
	}
	// Scrolling to the end shows the tail that used to be cut off.
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")}, DefaultKeys)
	if view := c.quoteViewBody(); !strings.Contains(view, "END") {
		t.Errorf("end of the view does not show the URL tail:\n%s", view)
	}
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}, DefaultKeys)
	if !strings.Contains(copied, url) {
		t.Errorf("copy must hand over the unwrapped URL, got %q", copied)
	}
}

func uiTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 24, 12))
	for x := 0; x < 24; x++ {
		img.SetNRGBA(x, 3, color.NRGBA{uint8(x * 10), 40, 200, 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// The quote's inline images are opt-in per message, with a Settings default (#19).
func TestQuoteInlineImagesToggleControlsWhatIsSent(t *testing.T) {
	chart := db.Attachment{Filename: "chart.png", ContentType: "image/png", ContentID: "chart@x", Inline: true, Data: uiTestPNG(t)}
	c, _ := quoteFixtureHTML(t, `<p>Look: <img src="cid:chart@x" alt="the chart"></p>`, chart)
	c.toInput.SetValue("ann@example.com")
	if c.quoteImages {
		t.Fatal("a message starts with the Settings default, which is off")
	}
	send := func(c ComposeModel) smtp.OutgoingMessage {
		t.Helper()
		msg, err := mailapp.PrepareCompose(c.selectedAccount(), c.toDraftRecord(), c.quoteOptions()...)
		if err != nil {
			t.Fatal(err)
		}
		return msg
	}
	if msg := send(c); len(msg.InlineImages) != 0 || !strings.Contains(msg.HTMLBody, "the chart") {
		t.Fatalf("off: want alt text only, got %d parts\n%s", len(msg.InlineImages), msg.HTMLBody)
	}
	// F10 in the message body switches it on for this message only.
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyF10}, DefaultKeys)
	if !c.quoteImages || !strings.Contains(c.statusMsg, "included") {
		t.Fatalf("f10 did not switch it on: %v %q", c.quoteImages, c.statusMsg)
	}
	if msg := send(c); len(msg.InlineImages) != 1 || !strings.Contains(msg.HTMLBody, "cid:"+msg.InlineImages[0].ContentID) {
		t.Fatalf("on: want the image attached and referenced, got %d parts\n%s", len(msg.InlineImages), msg.HTMLBody)
	}
	// Inside the F3 view the plain key does the same and the footer says which.
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyF3}, DefaultKeys)
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")}, DefaultKeys)
	if c.quoteImages || !strings.Contains(ansi.Strip(c.quotePanel(120, newManagerChrome(120, CatppuccinMocha, false))), "quote images off") {
		t.Errorf("i in the quote view must toggle and show the state: on=%v", c.quoteImages)
	}
}

func TestQuoteImagesDefaultComesFromSettingsAndNeedsAMarker(t *testing.T) {
	c, _ := quoteFixture(t)
	setComposeQuoteImages(true)
	t.Cleanup(func() { setComposeQuoteImages(false) })
	acfg := config.AccountConfig{ID: "a", Name: "Work", User: "me@example.com"}
	if fresh := NewCompose(acfg, []config.AccountConfig{acfg}, nil); !fresh.quoteImages {
		t.Error("a new message must start from [display] compose_reply_quote_images")
	}
	c.bodyInput.SetValue("no marker here")
	c.quoteImages = false
	c, _, _ = c.Update(tea.KeyMsg{Type: tea.KeyF10}, DefaultKeys)
	if c.quoteImages || !strings.Contains(c.statusMsg, "no quoted-message marker") {
		t.Errorf("without a marker the key must say so and change nothing: %v %q", c.quoteImages, c.statusMsg)
	}
}

func TestQuoteImagesSettingIsOffByDefaultAndSaves(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Display.ComposeReplyQuoteImages {
		t.Fatal("compose_reply_quote_images must start off")
	}
	s := newSettings(cfg, settingsUpdateState{})
	s.setActiveSection(ssEditor)
	s.setFocusedPane(settingsPaneDetail)
	s.setFocusedField(sfComposeReplyQuoteImages)
	on, _, _ := s.Update(tea.KeyMsg{Type: tea.KeySpace}, DefaultKeys)
	if !on.ApplyTo(cfg).Display.ComposeReplyQuoteImages {
		t.Fatal("Space must turn it on")
	}
	if !newSettings(on.ApplyTo(cfg), settingsUpdateState{}).ApplyTo(config.DefaultConfig()).Display.ComposeReplyQuoteImages {
		t.Fatal("an enabled setting must be read from config")
	}
}
