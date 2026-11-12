package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/db"
)

// replyFixture caches one HTML message with a table and returns its cache ID.
func replyFixture(t *testing.T) string {
	t.Helper()
	mailFixture(t)
	database, err := db.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	acc, _ := database.AddAccount("stable", "Work", "")
	box, _ := database.UpsertMailbox(db.Mailbox{AccountID: acc, Name: "INBOX"})
	err = database.UpsertMessage(db.Message{MailboxID: box, UID: 5, MessageID: "<orig@x>", References: "<root@x>", Subject: "Budget", From: "Ann Lee <ann@example.com>",
		ReplyTo: "ann.reply@example.com", Date: time.Date(2026, 3, 10, 9, 5, 0, 0, time.UTC), BodyText: "flat",
		BodyHTML: `<p>Totals</p><table><tr><th>Item</th><th>Qty</th></tr><tr><td>Pens</td><td>3</td></tr></table><script>bad()</script>`})
	if err != nil {
		t.Fatal(err)
	}
	id, err := database.MessageIDByUID(box, 5)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("cache:%d", id)
}

func runHeadless(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := RunHeadless(args, strings.NewReader(""), &out, &errs)
	return code, out.String() + errs.String()
}

func TestDraftCreateReplyKeepsTheMarkerAndThreading(t *testing.T) {
	id := replyFixture(t)
	code, out := runHeadless(t, "draft", "create", "--reply-to", id, "--body", "Thanks!", "--json")
	if code != 0 || !strings.Contains(out, `"drafted"`) {
		t.Fatalf("code=%d %s", code, out)
	}
	database, _ := db.Open()
	defer database.Close()
	drafts, err := database.ListDrafts("stable", "Work", "me@example.com")
	if err != nil || len(drafts) != 1 {
		t.Fatalf("drafts %v %v", drafts, err)
	}
	d := drafts[0]
	if d.InReplyTo != "<orig@x>" || d.References != "<root@x> <orig@x>" || d.Subject != "Re: Budget" || d.To != "ann.reply@example.com" {
		t.Errorf("threading: %+v", d)
	}
	if !strings.Contains(d.BodyText, "Thanks!") || !strings.Contains(d.BodyText, "⁂") || !strings.Contains(d.BodyText, "⁑") || strings.Contains(d.BodyText, "Totals") {
		t.Errorf("body %q: want the text, both markers, and the original NOT copied into the draft", d.BodyText)
	}
}

func TestSendReplyDryRunResolvesTheQuote(t *testing.T) {
	id := replyFixture(t)
	code, out := runHeadless(t, "send", "--reply-to", id, "--body", "Thanks!", "--dry-run", "--json")
	if code != 0 || !strings.Contains(out, "validated") {
		t.Fatalf("marker quote: code=%d %s", code, out)
	}
	// Unknown message, bad ID, and bad mode are refused with distinct errors.
	for args, want := range map[string]string{
		"--reply-to cache:99999 --body x --dry-run --json":                 "not_found",
		"--reply-to nonsense --body x --dry-run --json":                    "invalid_arguments",
		"--reply-to " + id + " --quote sideways --body x --dry-run --json": "invalid_arguments",
	} {
		if code, out := runHeadless(t, append([]string{"send"}, strings.Fields(args)...)...); code == 0 || !strings.Contains(out, want) {
			t.Errorf("%q: code=%d %s", args, code, out)
		}
	}
}

func TestReplyQuoteTextAndNone(t *testing.T) {
	id := replyFixture(t)
	if code, out := runHeadless(t, "draft", "create", "--reply-to", id, "--quote", "text", "--body", "Thanks!", "--json"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	if code, out := runHeadless(t, "draft", "create", "--reply-to", id, "--quote", "none", "--body", "No quote", "--json"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	database, _ := db.Open()
	defer database.Close()
	drafts, _ := database.ListDrafts("stable", "Work", "me@example.com")
	bodies := map[string]string{}
	for _, d := range drafts {
		bodies[strings.SplitN(d.BodyText, "\n", 2)[0]] = d.BodyText
	}
	text, none := bodies["Thanks!"], bodies["No quote"]
	for _, want := range []string{"Ann Lee <ann@example.com> wrote:", "> Totals", "> | Item | Qty |", "> | Pens | 3 |"} {
		if !strings.Contains(text, want) {
			t.Errorf("--quote text missing %q in %q", want, text)
		}
	}
	if strings.Contains(text, "⁑") || strings.Contains(text, "bad()") {
		t.Errorf("--quote text: %q", text)
	}
	if strings.Contains(none, "wrote:") || strings.Contains(none, "⁑") {
		t.Errorf("--quote none: %q", none)
	}
}

func TestReplyToNeedsMessagesReadForAgents(t *testing.T) {
	id := replyFixture(t)
	code, out := runHeadless(t, "draft", "create", "--reply-to", id, "--body", "x", "--agent", "--json")
	if code != 3 || !strings.Contains(out, "messages.read") {
		t.Fatalf("agent without messages.read: code=%d %s", code, out)
	}
	var v struct {
		OK bool `json:"ok"`
	}
	_ = json.Unmarshal([]byte(out), &v)
	if v.OK {
		t.Error("must not succeed")
	}
}
