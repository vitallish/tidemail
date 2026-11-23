package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/db"
)

// replyFixture caches one HTML message with a table and returns its cache ID.
func replyFixture(t *testing.T) string {
	t.Helper()
	return replyFixtureWith(t, `<p>Totals</p><table><tr><th>Item</th><th>Qty</th></tr><tr><td>Pens</td><td>3</td></tr></table><script>bad()</script>`)
}

// replyFixtureWith is replyFixture with the original's HTML and cached parts chosen by the test.
func replyFixtureWith(t *testing.T, bodyHTML string, parts ...db.Attachment) string {
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
		BodyHTML: bodyHTML, HasAttachment: len(parts) > 0, AttachmentData: parts})
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
	code, out := runHeadless(t, "draft", "create", "--in-reply-to", id, "--quote", "marker", "--body", "Thanks!", "--json")
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
	code, out := runHeadless(t, "send", "--in-reply-to", id, "--quote", "marker", "--body", "Thanks!", "--dry-run", "--json")
	if code != 0 || !strings.Contains(out, "validated") {
		t.Fatalf("marker quote: code=%d %s", code, out)
	}
	// Unknown message, bad ID, and bad mode are refused with distinct errors.
	for args, want := range map[string]string{
		"--in-reply-to cache:99999 --quote none --body x --dry-run --json":    "not_found",
		"--in-reply-to nonsense --quote none --body x --dry-run --json":       "invalid_arguments",
		"--in-reply-to " + id + " --quote sideways --body x --dry-run --json": "invalid_arguments",
	} {
		if code, out := runHeadless(t, append([]string{"send"}, strings.Fields(args)...)...); code == 0 || !strings.Contains(out, want) {
			t.Errorf("%q: code=%d %s", args, code, out)
		}
	}
}

// Without --quote the flag is upstream's: thread only, nothing else filled in.
func TestInReplyToAloneOnlyThreads(t *testing.T) {
	id := replyFixture(t)
	if code, out := runHeadless(t, "draft", "create", "--in-reply-to", id, "--to", "bob@example.com", "--subject", "Own subject", "--body", "Hi", "--json"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	database, _ := db.Open()
	defer database.Close()
	drafts, _ := database.ListDrafts("stable", "Work", "me@example.com")
	if len(drafts) != 1 {
		t.Fatalf("drafts %v", drafts)
	}
	d := drafts[0]
	if d.InReplyTo != "<orig@x>" || d.References != "<root@x> <orig@x>" || d.Subject != "Own subject" || d.To != "bob@example.com" || d.BodyText != "Hi" {
		t.Errorf("--in-reply-to alone must only thread: %+v", d)
	}
	if code, out := runHeadless(t, "draft", "create", "--quote", "text", "--body", "x", "--json"); code == 0 || !strings.Contains(out, "--quote needs --in-reply-to") {
		t.Errorf("--quote without --in-reply-to: code=%d %s", code, out)
	}
}

func TestReplyQuoteTextAndNone(t *testing.T) {
	id := replyFixture(t)
	if code, out := runHeadless(t, "draft", "create", "--in-reply-to", id, "--quote", "text", "--body", "Thanks!", "--json"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	if code, out := runHeadless(t, "draft", "create", "--in-reply-to", id, "--quote", "none", "--body", "No quote", "--json"); code != 0 {
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
	code, out := runHeadless(t, "draft", "create", "--in-reply-to", id, "--quote", "marker", "--body", "x", "--agent", "--json")
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

func cliTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 4), uint8(y * 4), uint8(x*y) & 0xff, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func mimeBytesOf(t *testing.T, out string) int {
	t.Helper()
	var v struct {
		Data struct {
			Bytes int `json:"mime_bytes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Data.Bytes == 0 {
		t.Fatalf("no mime_bytes in %s (%v)", out, err)
	}
	return v.Data.Bytes
}

func TestQuoteImagesFlagsCarryTheOriginalsInlineImage(t *testing.T) {
	id := replyFixtureWith(t, `<p>Chart: <img src="cid:c@x" alt="chart"></p>`,
		db.Attachment{Filename: "c.png", ContentType: "image/png", ContentID: "c@x", Inline: true, Data: cliTestPNG(t)})
	dry := func(extra ...string) int {
		args := append([]string{"send", "--in-reply-to", id, "--quote", "marker", "--body", "x", "--dry-run", "--json"}, extra...)
		code, out := runHeadless(t, args...)
		if code != 0 {
			t.Fatalf("%v: %d %s", extra, code, out)
		}
		return mimeBytesOf(t, out)
	}
	without, off, with := dry(), dry("--no-quote-images"), dry("--quote-images")
	if without != off {
		t.Errorf("the default is off: %d vs %d bytes", without, off)
	}
	if with <= without+500 {
		t.Errorf("--quote-images must attach the image: %d bytes with, %d without", with, without)
	}
	for args, want := range map[string]string{
		"--quote-images --no-quote-images": "cannot be combined",
		"--quote-images --quote text":      "--quote marker",
		"--no-quote-images --quote none":   "--quote marker",
	} {
		code, out := runHeadless(t, append([]string{"send", "--in-reply-to", id, "--body", "x", "--dry-run", "--json"}, strings.Fields(args)...)...)
		if code == 0 || !strings.Contains(out, want) {
			t.Errorf("%q: code=%d %s", args, code, out)
		}
	}
}

// --forward (#20): Fwd: subject, the original's attachments, no threading, the
// original quoted by marker (default), text or not at all.
func TestForwardCLIBuildsAForwardWithoutThreading(t *testing.T) {
	id := replyFixtureWith(t, `<p>Totals</p><table><tr><th>Item</th><th>Qty</th></tr><tr><td>Pens</td><td>3</td></tr></table>`,
		db.Attachment{Filename: "budget.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.4")})
	code, out := runHeadless(t, "draft", "create", "--forward", id, "--to", "bob@example.com", "--body", "FYI", "--json")
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	database, _ := db.Open()
	defer database.Close()
	drafts, err := database.ListDrafts("stable", "Work", "me@example.com")
	if err != nil || len(drafts) != 1 {
		t.Fatalf("drafts %v %v", drafts, err)
	}
	d := drafts[0]
	if d.ForwardOf != "<orig@x>" || d.InReplyTo != "" || d.References != "" || d.Subject != "Fwd: Budget" || d.To != "bob@example.com" {
		t.Errorf("forward draft: %+v", d)
	}
	if !strings.Contains(d.BodyText, "FYI") || !strings.Contains(d.BodyText, "⁑") || strings.Contains(d.BodyText, "Totals") {
		t.Errorf("marker body expected, original not copied: %q", d.BodyText)
	}
	if len(d.Attachments) != 1 || d.Attachments[0].Filename != "budget.pdf" {
		t.Errorf("the original's attachment must come along: %+v", d.Attachments)
	}
}

func TestForwardCLIQuoteModesAndRefusals(t *testing.T) {
	id := replyFixture(t)
	if code, out := runHeadless(t, "send", "--forward", id, "--to", "bob@example.com", "--body", "FYI", "--dry-run", "--json"); code != 0 || !strings.Contains(out, "validated") {
		t.Fatalf("marker is the default: %d %s", code, out)
	}
	if code, out := runHeadless(t, "draft", "create", "--forward", id, "--quote", "text", "--to", "bob@example.com", "--body", "FYI", "--json"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	database, _ := db.Open()
	defer database.Close()
	drafts, _ := database.ListDrafts("stable", "Work", "me@example.com")
	if len(drafts) != 1 || !strings.Contains(drafts[0].BodyText, "---------- Forwarded message ----------") || !strings.Contains(drafts[0].BodyText, "> | Pens | 3 |") {
		t.Errorf("text mode: %+v", drafts)
	}
	for args, want := range map[string]string{
		"--forward " + id + " --in-reply-to " + id:         "cannot be combined",
		"--forward cache:99999":                            "not_found",
		"--forward nonsense":                               "invalid_arguments",
		"--forward " + id + " --quote sideways":            "invalid_arguments",
		"--forward " + id + " --quote none --quote-images": "--quote marker",
	} {
		code, out := runHeadless(t, append([]string{"send", "--to", "bob@example.com", "--body", "x", "--dry-run", "--json"}, strings.Fields(args)...)...)
		if code == 0 || !strings.Contains(out, want) {
			t.Errorf("%q: code=%d %s", args, code, out)
		}
	}
}

func TestForwardCLINeedsMessagesReadForAgents(t *testing.T) {
	id := replyFixture(t)
	code, out := runHeadless(t, "draft", "create", "--forward", id, "--to", "bob@example.com", "--body", "x", "--agent", "--json")
	if code != 3 || !strings.Contains(out, "messages.read") {
		t.Fatalf("agent without messages.read: code=%d %s", code, out)
	}
}
