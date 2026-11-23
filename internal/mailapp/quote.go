package mailapp

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/inlineimg"
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
func BuildQuote(original db.Message) Quote { return buildQuote(original, nil, false) }

// BuildForwardQuote is BuildQuote for a forward: the heading is the forwarded-
// message header block (From, Date, Subject, To, CC) instead of "wrote:".
func BuildForwardQuote(original db.Message) Quote { return buildQuote(original, nil, true) }

// buildQuote is BuildQuote with a resolver for the original's cid: images (nil
// keeps the policy of dropping them down to their alt text).
func buildQuote(original db.Message, cid func(contentID string) (string, bool), forward bool) Quote {
	var fragment string
	if strings.TrimSpace(original.BodyHTML) != "" {
		fragment = quotehtml.SanitizeWith(original.BodyHTML, quotehtml.Options{CID: cid})
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
	heading := attribution(original)
	if forward {
		heading = forwardHeader(original)
	}
	return Quote{
		Attribution: neutral(heading),
		HTML:        neutral(fragment),
		Text:        neutral(text),
	}
}

// forwardHeader is the block that opens a forwarded message, as the composer's
// text forward writes it.
func forwardHeader(m db.Message) string {
	var b strings.Builder
	b.WriteString("---------- Forwarded message ----------\nFrom: " + m.From)
	if !m.Date.IsZero() {
		b.WriteString("\nDate: " + m.Date.Format(time.RFC1123Z))
	}
	b.WriteString("\nSubject: " + m.Subject)
	if m.To != "" {
		b.WriteString("\nTo: " + m.To)
	}
	if m.CC != "" {
		b.WriteString("\nCC: " + m.CC)
	}
	return b.String()
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
	return `<div class="tidemail_quote"><p>` + strings.ReplaceAll(html.EscapeString(q.Attribution), "\n", "<br>") + `</p>` +
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

// ExpandedMarkdown is the quote as ordinary editable Markdown: the attribution,
// then the original with every line prefixed "> " (tables as pipe tables). It is
// what "expand the quote" produces in the composer and what the command line puts
// in a reply with --quote text.
func (q Quote) ExpandedMarkdown() string {
	md, err := quotehtml.Markdown(q.HTML)
	if err != nil || strings.TrimSpace(md) == "" {
		md = q.Text
	}
	var b strings.Builder
	b.WriteString(strings.ReplaceAll(q.Attribution, "\n", "  \n") + "\n\n") // a multi-line heading (forward) keeps its lines
	for _, line := range strings.Split(strings.TrimRight(md, "\n"), "\n") {
		b.WriteString(strings.TrimRight("> "+line, " ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ComposeOption adjusts PrepareCompose and ComposeHTML.
type ComposeOption func(*composeOptions)

type composeOptions struct {
	replyTo string // the Message-ID the marker is resolved from: the reply's In-Reply-To, or the forwarded message's own
	forward bool   // replyTo is a forwarded message: the heading is the forward header block
	lookup  QuoteLookup
	// quoteImages loads the original's cached image parts; nil means the quote
	// keeps only the alt text of its inline images.
	quoteImages QuoteImageLoader
}

// QuoteImageLoader returns the cached image parts of a quoted message (the ones
// with a Content-ID or an image type), with their bytes.
type QuoteImageLoader func(original db.Message) ([]db.Attachment, error)

// WithQuoteImages makes the quote carry the original's inline (cid:) images: they
// are attached to the reply as related parts and the quote's HTML points at
// them. Without it, or when a part is missing, unsupported (webp, svg), or over
// the quote's size budget, the image stays as its alt text.
func WithQuoteImages(load QuoteImageLoader) ComposeOption {
	return func(o *composeOptions) { o.quoteImages = load }
}

// NewQuoteImageLoader reads the parts from the local cache.
func NewQuoteImageLoader(database *db.DB) QuoteImageLoader {
	return func(original db.Message) ([]db.Attachment, error) {
		return database.GetImageParts(original.ID)
	}
}

// WithQuote lets a body's QuoteMarker be resolved: lookup finds the original by
// the Message-ID the reply answers. For PrepareCompose the draft's own
// In-Reply-To is used when replyTo is empty.
func WithQuote(replyTo string, lookup QuoteLookup) ComposeOption {
	return func(o *composeOptions) { o.replyTo, o.lookup = replyTo, lookup }
}

// WithForwardQuote is WithQuote for a forward: forwardOf is the Message-ID of the
// message being forwarded. PrepareCompose uses the draft's own ForwardOf when
// neither option is given.
func WithForwardQuote(forwardOf string, lookup QuoteLookup) ComposeOption {
	return func(o *composeOptions) { o.replyTo, o.forward, o.lookup = forwardOf, true, lookup }
}

func applyOptions(opts []ComposeOption) composeOptions {
	var o composeOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// resolveQuote finds the quote for a body that contains the marker.
func (o composeOptions) resolveQuote(images *inlineimg.Resolver) (*Quote, error) {
	if o.lookup == nil || strings.TrimSpace(o.replyTo) == "" {
		return nil, fmt.Errorf("%s has nothing to quote here: this is neither a reply nor a forward, so delete %s", QuoteMarker, QuoteMarker)
	}
	original, ok, err := o.lookup(o.replyTo)
	if err != nil {
		return nil, err
	}
	if !ok {
		verb := "replying to"
		if o.forward {
			verb = "forwarding"
		}
		return nil, fmt.Errorf("the message you are %s is no longer in the cache: expand the quote (or delete %s) before sending", verb, QuoteMarker)
	}
	var cid func(string) (string, bool)
	if o.quoteImages != nil && images != nil {
		parts, err := o.quoteImages(original)
		if err != nil {
			return nil, fmt.Errorf("cannot read the quoted message's images: %w", err)
		}
		byCID := map[string]db.Attachment{}
		for _, p := range parts {
			if key := cidKey(p.ContentID); key != "" {
				byCID[key] = p
			}
		}
		cid = func(contentID string) (string, bool) {
			part, ok := byCID[cidKey(contentID)]
			if !ok {
				return "", false
			}
			return images.EmbedQuoteImage(part.Filename, part.Data)
		}
	}
	q := buildQuote(original, cid, o.forward)
	return &q, nil
}

func cidKey(id string) string { return strings.ToLower(strings.Trim(strings.TrimSpace(id), "<>")) }
