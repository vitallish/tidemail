package mailapp

import (
	"fmt"
	"html"
	"strings"

	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/quotehtml"
)

// QuoteMarker is reserved in a reply's body, like SignatureMarker: alone on a
// line it stands for "the message being replied to goes here". It is resolved
// when the reply is previewed or sent, so the user can move it (top, bottom or
// inline posting) or delete it (no quote), and the quote keeps the original's
// HTML structure instead of the flattened text a "> " quote has.
const QuoteMarker = "⁑"

// quoteSafeRune replaces a literal QuoteMarker inside quoted mail, so a glyph in
// someone else's message can never become a marker.
const quoteSafeRune = "⁎"

// Quote is the message being replied to, ready to be placed in a reply.
type Quote struct {
	Attribution string // "On <date>, <sender> wrote:"
	HTML        string // sanitized fragment
	Text        string // plain text, unquoted (no "> " prefixes)
}

// QuoteLookup finds the cached message with the given Message-ID (a reply's
// In-Reply-To). ok is false when it is not in the cache.
type QuoteLookup func(messageID string) (original db.Message, ok bool, err error)

// BuildQuote prepares original for quoting: its HTML sanitized (or its text
// turned into paragraphs when it has no HTML), the plain-text form, and an
// attribution line.
func BuildQuote(original db.Message) Quote {
	var fragment string
	if strings.TrimSpace(original.BodyHTML) != "" {
		fragment = quotehtml.Sanitize(original.BodyHTML)
	}
	text := ""
	if fragment != "" {
		text = quotehtml.PlainText(fragment)
	}
	if strings.TrimSpace(text) == "" {
		fragment = quotehtml.FromText(original.BodyText)
		text = strings.TrimSpace(strings.ReplaceAll(original.BodyText, "\r\n", "\n"))
	}
	neutral := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, QuoteMarker, quoteSafeRune), SignatureMarker, "⁎")
	}
	return Quote{
		Attribution: neutral(attribution(original)),
		HTML:        neutral(fragment),
		Text:        neutral(text),
	}
}

func attribution(m db.Message) string {
	who := strings.TrimSpace(m.From)
	if who == "" {
		who = "the sender"
	}
	if m.Date.IsZero() {
		return who + " wrote:"
	}
	return "On " + m.Date.Format("Mon, 2 Jan 2006 at 15:04") + ", " + who + " wrote:"
}

// quoteHTML is the HTML placed at the marker.
func quoteHTML(q Quote) string {
	return `<div class="tidemail_quote"><p>` + html.EscapeString(q.Attribution) + `</p>` +
		`<blockquote type="cite" style="margin: 0 0 0 0.8ex; border-left: 2px solid #ccc; padding-left: 1ex">` +
		q.HTML + `</blockquote></div>`
}

// quoteText is the plain-text form placed at the marker: the attribution and the
// original with every line prefixed "> ".
func quoteText(q Quote) string {
	var b strings.Builder
	b.WriteString(q.Attribution + "\n")
	for _, line := range strings.Split(strings.TrimRight(q.Text, "\n"), "\n") {
		b.WriteString(strings.TrimRight("> "+line, " ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ComposeOption adjusts PrepareCompose and ComposeHTML.
type ComposeOption func(*composeOptions)

type composeOptions struct {
	replyTo string
	lookup  QuoteLookup
}

// WithQuote lets a body's QuoteMarker be resolved: lookup finds the original by
// the Message-ID the reply answers. For PrepareCompose the draft's own
// In-Reply-To is used when replyTo is empty.
func WithQuote(replyTo string, lookup QuoteLookup) ComposeOption {
	return func(o *composeOptions) { o.replyTo, o.lookup = replyTo, lookup }
}

func applyOptions(opts []ComposeOption) composeOptions {
	var o composeOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// resolveQuote finds the quote for a body that contains the marker.
func (o composeOptions) resolveQuote() (*Quote, error) {
	if o.lookup == nil || strings.TrimSpace(o.replyTo) == "" {
		return nil, fmt.Errorf("%s has nothing to quote here: this is not a reply, so delete %s", QuoteMarker, QuoteMarker)
	}
	original, ok, err := o.lookup(o.replyTo)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("the message you are replying to is no longer in the cache: expand the quote (or delete %s) before sending", QuoteMarker)
	}
	q := BuildQuote(original)
	return &q, nil
}
