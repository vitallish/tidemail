package mailapp

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
)

var quoteAccount = config.AccountConfig{User: "me@example.com", Signature: "Me"}

func originalWithTable() db.Message {
	return db.Message{
		MessageID: "<orig@x>",
		From:      "Ann Lee <ann@example.com>",
		Date:      time.Date(2026, 3, 10, 9, 5, 0, 0, time.UTC),
		BodyText:  "flattened fallback",
		BodyHTML: `<html><head><style>td{color:red}</style></head><body onload="evil()">
<p>Totals for <b>March</b></p><table border="1"><tr><th>Item</th><th>Qty</th></tr><tr><td>Pens</td><td>3</td></tr></table>
<script>stealCookies()</script><img src="https://t.example/p.gif" width="1" height="1"></body></html>`,
	}
}

func lookupOf(m db.Message) QuoteLookup {
	return func(id string) (db.Message, bool, error) {
		if id == m.MessageID {
			return m, true, nil
		}
		return db.Message{}, false, nil
	}
}

func TestQuoteMarkerBecomesTheOriginalWithItsStructure(t *testing.T) {
	draft := db.Draft{To: "ann@example.com", InReplyTo: "<orig@x>", BodyText: "Thanks, see below.\n\n⁂\n\n⁑\n"}
	msg, err := PrepareCompose(quoteAccount, draft, WithQuote("", lookupOf(originalWithTable())))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Thanks, see below.", `<blockquote type="cite"`, "On Tue, 10 Mar 2026 at 09:05, Ann Lee &lt;ann@example.com&gt; wrote:", "<table", "<th>Item</th>", "<td>Pens</td>", "<b>March</b>"} {
		if !strings.Contains(msg.HTMLBody, want) {
			t.Errorf("HTML missing %q:\n%s", want, msg.HTMLBody)
		}
	}
	for _, bad := range []string{"stealCookies", "onload", "<style", "t.example/p.gif", QuoteMarker, SignatureMarker} {
		if strings.Contains(msg.HTMLBody, bad) {
			t.Errorf("HTML still has %q:\n%s", bad, msg.HTMLBody)
		}
	}
	// The text part: attribution, a "> " quote of the readable text, the signature.
	for _, want := range []string{"Thanks, see below.", "-- \nMe", "On Tue, 10 Mar 2026 at 09:05, Ann Lee <ann@example.com> wrote:", "> Totals for March", "> Item | Qty", "> Pens | 3"} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("text missing %q:\n%s", want, msg.Body)
		}
	}
	if strings.Contains(msg.Body, QuoteMarker) || strings.Contains(msg.Body, "stealCookies") {
		t.Errorf("text part: %s", msg.Body)
	}
	if draft.BodyText != "Thanks, see below.\n\n⁂\n\n⁑\n" {
		t.Error("draft was modified")
	}
}

func TestQuoteMarkerPositionDecidesTopOrInlinePosting(t *testing.T) {
	lookup := WithQuote("<orig@x>", lookupOf(originalWithTable()))
	// Quote first, reply under it (bottom posting), signature last.
	msg, err := PrepareCompose(quoteAccount, db.Draft{To: "a@example.com", BodyText: "⁑\n\nMy answer\n\n⁂\n"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	q, a, s := strings.Index(msg.Body, "wrote:"), strings.Index(msg.Body, "My answer"), strings.Index(msg.Body, "-- \nMe")
	if q < 0 || q >= a || a >= s {
		t.Errorf("text order quote=%d answer=%d signature=%d:\n%s", q, a, s, msg.Body)
	}
	qh, ah := strings.Index(msg.HTMLBody, "<blockquote"), strings.Index(msg.HTMLBody, "My answer")
	if qh < 0 || qh >= ah {
		t.Errorf("HTML order quote=%d answer=%d", qh, ah)
	}
	// No marker, no quote.
	msg, err = PrepareCompose(quoteAccount, db.Draft{To: "a@example.com", BodyText: "Just this"}, lookup)
	if err != nil || strings.Contains(msg.HTMLBody, "blockquote") || strings.Contains(msg.Body, "wrote:") {
		t.Errorf("unquoted reply: %v %q", err, msg.HTMLBody)
	}
	// A quote without a signature marker.
	msg, err = PrepareCompose(quoteAccount, db.Draft{To: "a@example.com", BodyText: "Hi\n\n⁑"}, lookup)
	if err != nil || !strings.Contains(msg.HTMLBody, "<table") || strings.Contains(msg.Body, "-- \n") {
		t.Errorf("quote only: %v %q", err, msg.Body)
	}
}

func TestQuoteMarkerMisuseIsRefused(t *testing.T) {
	lookup := WithQuote("<orig@x>", lookupOf(originalWithTable()))
	for name, body := range map[string]string{
		"two":        "⁑\n\n⁑",
		"inline":     "see ⁑ here",
		"in a list":  "- ⁑",
		"in a fence": "```\n⁑\n```",
		"in a quote": "> ⁑",
		"no blank":   "Hello\n⁑",
		"entity":     "&#x2051;",
	} {
		if _, err := PrepareCompose(quoteAccount, db.Draft{To: "a@example.com", BodyText: body}, lookup); err == nil {
			t.Errorf("%s: accepted %q", name, body)
		}
	}
	// Not a reply: nothing to quote, said plainly.
	_, err := PrepareCompose(quoteAccount, db.Draft{To: "a@example.com", BodyText: "⁑"})
	if err == nil || !strings.Contains(err.Error(), "not a reply") {
		t.Errorf("no reply context: %v", err)
	}
	// The original left the cache.
	_, err = PrepareCompose(quoteAccount, db.Draft{To: "a@example.com", InReplyTo: "<gone@x>", BodyText: "⁑"}, WithQuote("", lookupOf(originalWithTable())))
	if err == nil || !strings.Contains(err.Error(), "no longer in the cache") {
		t.Errorf("missing original: %v", err)
	}
	// A failing lookup is an error, not an empty quote.
	boom := errors.New("db closed")
	if _, err = PrepareCompose(quoteAccount, db.Draft{To: "a@example.com", InReplyTo: "<x@x>", BodyText: "⁑"}, WithQuote("", func(string) (db.Message, bool, error) { return db.Message{}, false, boom })); !errors.Is(err, boom) {
		t.Errorf("lookup failure: %v", err)
	}
}

func TestQuotedMailCannotForgeMarkers(t *testing.T) {
	forged := originalWithTable()
	forged.BodyHTML = "<p>look ⁑ and ⁂ here</p>"
	msg, err := PrepareCompose(quoteAccount, db.Draft{To: "a@example.com", InReplyTo: "<orig@x>", BodyText: "Hi\n\n⁑"}, WithQuote("", lookupOf(forged)))
	if err != nil {
		t.Fatalf("a reserved glyph in the original must not break sending: %v", err)
	}
	if strings.Contains(msg.HTMLBody+msg.Body, QuoteMarker) || strings.Contains(msg.HTMLBody+msg.Body, SignatureMarker) {
		t.Error("a marker leaked from the quoted message")
	}
}

func TestTextOnlyOriginalIsQuotedAsParagraphs(t *testing.T) {
	plain := db.Message{MessageID: "<p@x>", From: "Bob", BodyText: "Line one\nline two\n\nSecond <para> & more"}
	msg, err := PrepareCompose(quoteAccount, db.Draft{To: "a@example.com", InReplyTo: "<p@x>", BodyText: "OK\n\n⁑"}, WithQuote("", lookupOf(plain)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.HTMLBody, "<p>Line one<br>line two</p><p>Second &lt;para&gt; &amp; more</p>") {
		t.Errorf("HTML: %s", msg.HTMLBody)
	}
	if !strings.Contains(msg.Body, "Bob wrote:\n> Line one\n> line two\n>\n> Second <para> & more") {
		t.Errorf("text: %q", msg.Body)
	}
}

func TestComposeHTMLPreviewResolvesTheQuoteToo(t *testing.T) {
	html, _, err := ComposeHTML(quoteAccount, "Hi\n\n⁑", nil, WithQuote("<orig@x>", lookupOf(originalWithTable())))
	if err != nil || !strings.Contains(html, "<table") {
		t.Fatalf("preview: %v %s", err, html)
	}
	if _, _, err := ComposeHTML(quoteAccount, "Hi\n\n⁑", nil); err == nil {
		t.Error("a preview with a marker and no reply context must say so")
	}
}
