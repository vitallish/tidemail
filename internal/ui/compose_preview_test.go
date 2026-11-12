package ui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/mailapp"
)

// captureBrowser replaces the browser launcher and returns the file contents it
// was asked to open, plus the command it was configured with.
func captureBrowser(t *testing.T) (opened func() (html, configured string, ok bool)) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	var gotCmd, gotHTML string
	var called bool
	old := openEmailBrowser
	openEmailBrowser = func(configured, path string) error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		gotCmd, gotHTML, called = configured, string(b), true
		return nil
	}
	t.Cleanup(func() { openEmailBrowser = old })
	return func() (string, string, bool) { return gotHTML, gotCmd, called }
}

func previewCompose(body string) ComposeModel {
	c := NewCompose(config.AccountConfig{User: "me@example.com", Signature: "Alice"}, nil, nil)
	c.bodyInput.SetValue(body)
	return c
}

const previewBody = "Results are **in**:\n\n- first\n- second\n\nSee https://example.com/x\n\n⁂\n"

func TestPreviewDocumentKeepsTheHTMLPartVerbatim(t *testing.T) {
	part := "<p>Hi <b>there</b></p>\n<table>\n</table>\n"
	doc := previewDocument(`Q3 <results> & "more"`, part)
	if !strings.Contains(doc, part) {
		t.Fatalf("the HTML part must appear byte for byte:\n%s", doc)
	}
	for _, want := range []string{"<!doctype html>", `<meta charset="utf-8">`, "Content-Security-Policy", "default-src 'none'",
		"<title>Preview (not sent): Q3 &lt;results&gt; &amp; &#34;more&#34;</title>"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q:\n%s", want, doc)
		}
	}
	if strings.Contains(previewDocument("", part), "(not sent):") {
		t.Fatal("an empty subject must not leave a dangling colon in the title")
	}
}

func TestPreviewDraftOpensExactlyTheHTMLThatSendingUses(t *testing.T) {
	opened := captureBrowser(t)
	c := previewCompose(previewBody)
	c.subjectInput.SetValue("Results")

	c, cmd := c.previewDraft()
	if cmd == nil || c.isErr {
		t.Fatalf("want a command and no error, got cmd=%v status=%q", cmd, c.statusMsg)
	}
	if msg, ok := cmd().(draftPreviewMsg); !ok || msg.err != nil {
		t.Fatalf("preview failed: %#v", msg)
	}
	html, _, ok := opened()
	if !ok {
		t.Fatal("the browser was not launched")
	}

	sent, err := mailapp.PrepareCompose(config.AccountConfig{User: "me@example.com", Signature: "Alice"},
		db.Draft{To: "reader@example.com", BodyText: previewBody})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, sent.HTMLBody) {
		t.Fatalf("preview lacks the exact HTML part that is sent.\npreview:\n%s\nsent:\n%s", html, sent.HTMLBody)
	}
	for _, want := range []string{"<strong>in</strong>", "<ul>\n<li>first</li>", "-- <br>\nAlice"} {
		if !strings.Contains(html, want) {
			t.Fatalf("preview lacks %q (bold, list and signature must be shown)", want)
		}
	}
}

func TestPreviewDraftNeedsNoRecipient(t *testing.T) {
	opened := captureBrowser(t)
	c := previewCompose("Just **text**\n")
	c, cmd := c.previewDraft()
	if cmd == nil || c.isErr {
		t.Fatalf("an unaddressed draft must preview: status=%q", c.statusMsg)
	}
	cmd()
	if html, _, ok := opened(); !ok || !strings.Contains(html, "<strong>text</strong>") {
		t.Fatalf("html = %q ok=%v", html, ok)
	}
}

func TestPreviewDraftReportsABadBodyAndOpensNothing(t *testing.T) {
	opened := captureBrowser(t)
	c := previewCompose("a\n\n⁂\n\nb\n\n⁂\n")
	c, cmd := c.previewDraft()
	if cmd != nil || !c.isErr || !strings.Contains(c.statusMsg, "⁂") {
		t.Fatalf("want the marker error and no command: cmd=%v status=%q err=%v", cmd, c.statusMsg, c.isErr)
	}
	if _, _, ok := opened(); ok {
		t.Fatal("nothing may be opened for an invalid body")
	}
}

func TestPreviewDraftUsesTheConfiguredBrowser(t *testing.T) {
	opened := captureBrowser(t)
	t.Cleanup(func() { setComposeBrowser("") })
	setComposeBrowser("  firefox --new-window ")
	_, cmd := previewCompose("hi\n").previewDraft()
	cmd()
	if _, configured, ok := opened(); !ok || configured != "firefox --new-window" {
		t.Fatalf("configured browser = %q ok=%v", configured, ok)
	}
}

func TestPreviewKeyAndPaletteRunTheSameAction(t *testing.T) {
	opened := captureBrowser(t)
	c := previewCompose("hello\n")
	c, cmd, exit := c.Update(tea.KeyMsg{Type: tea.KeyF4}, DefaultKeys)
	if exit || cmd == nil {
		t.Fatalf("F4 must start the preview: exit=%v cmd=%v", exit, cmd)
	}
	msg := cmd()
	c, _, _ = c.Update(msg, DefaultKeys)
	if c.isErr || c.statusMsg != "Preview opened in browser" {
		t.Fatalf("status = %q (err=%v)", c.statusMsg, c.isErr)
	}
	if _, _, ok := opened(); !ok {
		t.Fatal("the browser was not launched")
	}
}

func TestPreviewFailureIsShown(t *testing.T) {
	c := previewCompose("hi\n")
	c, _, _ = c.Update(draftPreviewMsg{err: os.ErrPermission}, DefaultKeys)
	if !c.isErr || !strings.Contains(c.statusMsg, "preview:") {
		t.Fatalf("status = %q (err=%v)", c.statusMsg, c.isErr)
	}
}
