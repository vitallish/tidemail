package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/allisonhere/tidemail/internal/clipboard"
	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/mailapp"
)

var (
	// composeReplyMarker is [display] compose_reply_marker, injected from config
	// like composeBrowser: new replies start with the quote marker instead of a
	// pasted "> " quote.
	composeReplyMarker bool
	// composeQuoteLookup finds the message a reply answers, for the quote marker.
	composeQuoteLookup mailapp.QuoteLookup
)

func setComposeReplyMarker(on bool) { composeReplyMarker = on }

// copyQuoteText writes to the system clipboard; a variable so tests never touch it.
var copyQuoteText = clipboard.Copy

// setComposeQuoteDB makes quote lookups read from database.
func setComposeQuoteDB(database *db.DB) {
	if database == nil {
		composeQuoteLookup = nil
		return
	}
	composeQuoteLookup = func(messageID string) (db.Message, bool, error) {
		return database.MessageByMessageID(messageID)
	}
}

// quoteOption is the compose option that lets the marker be resolved.
func quoteOption(replyTo string) mailapp.ComposeOption {
	return mailapp.WithQuote(strings.TrimSpace(replyTo), composeQuoteLookup)
}

// ── the quote marker inside the composer ────────────────────────────────────

// quoteMarkerLabel is how the composer draws the marker line.
const quoteMarkerLabel = "⁅ quoted message ⁆"

// hasQuoteMarker reports whether the body still holds the quote marker.
func (c ComposeModel) hasQuoteMarker() bool {
	return strings.Contains(c.bodyInput.Value(), mailapp.QuoteMarker)
}

// quoteOriginal finds the message this reply answers.
func (c ComposeModel) quoteOriginal() (db.Message, error) {
	if composeQuoteLookup == nil || strings.TrimSpace(c.inReplyTo) == "" {
		return db.Message{}, fmt.Errorf("this is not a reply, so there is no message to quote")
	}
	original, ok, err := composeQuoteLookup(strings.TrimSpace(c.inReplyTo))
	if err != nil {
		return db.Message{}, err
	}
	if !ok {
		return db.Message{}, fmt.Errorf("the message you are replying to is no longer in the cache")
	}
	return original, nil
}

// openQuoteView shows the quoted message read-only, to read or copy without
// putting it in the message.
func (c ComposeModel) openQuoteView() ComposeModel {
	if !c.hasQuoteMarker() {
		c.statusMsg, c.isErr = "no quoted-message marker in this message", false
		return c
	}
	original, err := c.quoteOriginal()
	if err != nil {
		c.statusMsg, c.isErr = err.Error(), true
		return c
	}
	q := mailapp.BuildQuote(original)
	c.quoteText = q.Attribution + "\n\n" + q.Text
	c.quoteLines = strings.Split(c.quoteText, "\n")
	c.quoteTop = 0
	c.quoteOpen = true
	return c
}

// updateQuoteView handles keys while the quoted message is shown.
func (c ComposeModel) updateQuoteView(km tea.KeyMsg, keys KeyMap) (ComposeModel, tea.Cmd, bool) {
	page := max(1, c.bodyInput.h-1)
	last := max(0, len(c.quoteLines)-c.bodyInput.h)
	switch {
	case keyMatches(km, keys.Cancel), km.String() == "q", km.String() == "f3":
		c.quoteOpen = false
	case keyMatches(km, keys.Down), km.String() == "down":
		c.quoteTop = min(last, c.quoteTop+1)
	case keyMatches(km, keys.Up), km.String() == "up":
		c.quoteTop = max(0, c.quoteTop-1)
	case km.String() == "pgdown", km.String() == " ":
		c.quoteTop = min(last, c.quoteTop+page)
	case km.String() == "pgup":
		c.quoteTop = max(0, c.quoteTop-page)
	case km.String() == "home", km.String() == "g":
		c.quoteTop = 0
	case km.String() == "end", km.String() == "G":
		c.quoteTop = last
	case km.String() == "y", km.String() == "c":
		if err := copyQuoteText(c.quoteText); err != nil {
			c.statusMsg, c.isErr = "could not copy: "+err.Error(), true
		} else {
			c.statusMsg, c.isErr = "quoted message copied", false
		}
	case km.String() == "e":
		c.quoteOpen = false
		c = c.expandQuote()
	}
	return c, nil, false
}

// quoteViewBody is the text area while the quoted message is shown.
func (c ComposeModel) quoteViewBody() string {
	h := max(1, c.bodyInput.h)
	rows := make([]string, 0, h)
	for i := 0; i < h; i++ {
		if n := c.quoteTop + i; n < len(c.quoteLines) {
			rows = append(rows, c.quoteLines[n])
		} else {
			rows = append(rows, "")
		}
	}
	return strings.Join(rows, "\n")
}

// quotePanel is the footer while the quoted message is shown.
func (c ComposeModel) quotePanel(width int, chrome managerChrome) string {
	base := lipgloss.NewStyle().Background(chrome.baseBg)
	head := base.Foreground(chrome.muted).Render("  quoted message (read only; the reply is unchanged)  ") +
		base.Foreground(chrome.muted).Render(fmt.Sprintf("line %d/%d", min(len(c.quoteLines), c.quoteTop+1), len(c.quoteLines)))
	keyStyle, lblStyle := base.Foreground(chrome.text), base.Foreground(chrome.muted)
	var parts []string
	for _, p := range [][2]string{{"↑↓", "scroll"}, {"y", "copy"}, {"e", "expand into the message (cannot be undone as a quote)"}, {"esc", "close"}} {
		parts = append(parts, keyStyle.Render(p[0])+lblStyle.Render(" "+p[1]))
	}
	hints := base.Render("  ") + strings.Join(parts, base.Render("   "))
	blank := padStyled("", width, chrome.baseBg)
	return strings.Join([]string{padStyled(head, width, chrome.baseBg), padStyled(hints, width, chrome.baseBg), blank}, "\n")
}

// expandQuote replaces the marker with the quoted message as ordinary Markdown
// text ("> " quote, tables as pipe tables) that can be edited freely. It is
// one-way: the marker, and with it the original's HTML, is gone afterwards.
func (c ComposeModel) expandQuote() ComposeModel {
	body := c.bodyInput.Value()
	lines := strings.Split(body, "\n")
	at := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == mailapp.QuoteMarker {
			at = i
			break
		}
	}
	if at < 0 {
		c.statusMsg, c.isErr = "no quoted-message marker to expand", false
		return c
	}
	original, err := c.quoteOriginal()
	if err != nil {
		c.statusMsg, c.isErr = err.Error(), true
		return c
	}
	quoted := strings.Split(mailapp.BuildQuote(original).ExpandedMarkdown(), "\n")
	lines = append(lines[:at], append(quoted, lines[at+1:]...)...)
	c.expandedFrom = body
	c.bodyInput.SetValue(strings.Join(lines, "\n"))
	c.expandedTo = c.bodyInput.Value()
	c.dirty = true
	c.statusMsg, c.isErr = "quote expanded into the message; Ctrl+Z right now puts the marker back", false
	return c
}

// undoExpandQuote puts the marker back, but only while the body is exactly what
// expanding produced: the editor's own undo cannot see a whole-body replacement,
// and after any further edit the earlier text is no longer a safe thing to restore.
func (c ComposeModel) undoExpandQuote() (ComposeModel, bool) {
	if c.expandedFrom == "" || c.bodyInput.Value() != c.expandedTo {
		return c, false
	}
	c.bodyInput.SetValue(c.expandedFrom)
	c.expandedFrom, c.expandedTo = "", ""
	c.dirty = true
	c.statusMsg, c.isErr = "quote marker restored", false
	return c, true
}

// labelQuoteMarker draws the marker line as a dim label. The editor does not
// pad its lines (the composer does, afterwards), so the label only has to fit
// within width; a narrower editor keeps the plain glyph.
func labelQuoteMarker(view string, style lipgloss.Style, width int) string {
	if !strings.Contains(view, mailapp.QuoteMarker) || lipgloss.Width(quoteMarkerLabel) > width {
		return view
	}
	label := style.Render(quoteMarkerLabel)
	lines := strings.Split(view, "\n")
	for i, ln := range lines {
		if pos := strings.Index(ln, mailapp.QuoteMarker); pos >= 0 {
			lines[i] = ln[:pos] + label + ln[pos+len(mailapp.QuoteMarker):]
		}
	}
	return strings.Join(lines, "\n")
}
