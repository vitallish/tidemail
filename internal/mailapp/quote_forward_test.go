package mailapp

import (
	"strings"
	"testing"

	"github.com/allisonhere/tidemail/internal/db"
)

func forwardDraft() db.Draft {
	return db.Draft{To: "bob@example.com", Subject: "Fwd: Budget", ForwardOf: "<orig@x>", BodyText: "FYI\n\n\u2051\n"}
}

func forwardOriginal() db.Message {
	m := originalWithTable()
	m.Subject = "Budget"
	m.To = "me@example.com, team@example.com"
	m.CC = "boss@example.com"
	return m
}

// A forward resolves the marker from the draft's ForwardOf, never from In-Reply-To (#20).
func TestForwardMarkerBecomesTheOriginalWithAForwardHeader(t *testing.T) {
	d := forwardDraft()
	msg, err := PrepareCompose(quoteAccount, d, WithQuote("", lookupOf(forwardOriginal())))
	if err != nil {
		t.Fatal(err)
	}
	if msg.InReplyTo != "" {
		t.Errorf("a forward must not be threaded into the original's conversation: In-Reply-To %q", msg.InReplyTo)
	}
	for _, want := range []string{"FYI", "---------- Forwarded message ----------<br>From: Ann Lee &lt;ann@example.com&gt;<br>Date: Tue, 10 Mar 2026 09:05:00 +0000<br>Subject: Budget<br>To: me@example.com, team@example.com<br>CC: boss@example.com", "<table", "<td>Pens</td>"} {
		if !strings.Contains(msg.HTMLBody, want) {
			t.Errorf("HTML missing %q:\n%s", want, msg.HTMLBody)
		}
	}
	if strings.Contains(msg.HTMLBody, "wrote:") {
		t.Errorf("a forward is not a reply:\n%s", msg.HTMLBody)
	}
	for _, want := range []string{"FYI", "---------- Forwarded message ----------\nFrom: Ann Lee <ann@example.com>\nDate: Tue, 10 Mar 2026 09:05:00 +0000\nSubject: Budget", "> Item | Qty", "> Pens | 3"} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("text missing %q:\n%s", want, msg.Body)
		}
	}
}

func TestForwardWithoutTheOriginalInTheCacheSaysForwarding(t *testing.T) {
	d := forwardDraft()
	d.ForwardOf = "<gone@x>"
	_, err := PrepareCompose(quoteAccount, d, WithQuote("", lookupOf(forwardOriginal())))
	if err == nil || !strings.Contains(err.Error(), "you are forwarding is no longer in the cache") {
		t.Fatalf("err = %v", err)
	}
	// And a marker with nothing to resolve from, in a plain new message, still refuses.
	d = forwardDraft()
	d.ForwardOf = ""
	if _, err = PrepareCompose(quoteAccount, d, WithQuote("", lookupOf(forwardOriginal()))); err == nil || !strings.Contains(err.Error(), "neither a reply nor a forward") {
		t.Fatalf("err = %v", err)
	}
}

func TestWithForwardQuoteOverridesTheDraftsKey(t *testing.T) {
	d := forwardDraft()
	d.ForwardOf = "" // as the CLI does: the key comes from the option
	msg, err := PrepareCompose(quoteAccount, d, WithForwardQuote("<orig@x>", lookupOf(forwardOriginal())))
	if err != nil || !strings.Contains(msg.HTMLBody, "Forwarded message") {
		t.Fatalf("%v\n%s", err, msg.HTMLBody)
	}
}

func TestForwardExpandsToTheHeaderBlockAndQuotedLines(t *testing.T) {
	md := BuildForwardQuote(forwardOriginal()).ExpandedMarkdown()
	for _, want := range []string{"---------- Forwarded message ----------  \nFrom: Ann Lee <ann@example.com>  \n", "Subject: Budget  \nTo: me@example.com, team@example.com  \nCC: boss@example.com\n\n> Totals", "> | Item | Qty |"} {
		if !strings.Contains(md, want) {
			t.Errorf("expanded markdown missing %q:\n%s", want, md)
		}
	}
	// The reply form is unchanged.
	if reply := BuildQuote(forwardOriginal()).ExpandedMarkdown(); !strings.HasPrefix(reply, "On Tue, 10 Mar 2026 at 09:05, Ann Lee") || strings.Contains(reply, "Forwarded") {
		t.Errorf("reply expansion changed:\n%s", reply)
	}
}
