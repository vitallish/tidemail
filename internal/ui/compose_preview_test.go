package ui

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/mailapp"
)

// captureBrowser replaces the browser launcher. It records the configured
// command and how many times a browser was launched.
func captureBrowser(t *testing.T) (opened func() (path, configured string, launches int)) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	var gotPath, gotCmd string
	var launches int
	old := openEmailBrowser
	openEmailBrowser = func(configured, path string) error {
		gotPath, gotCmd = path, configured
		launches++
		return nil
	}
	t.Cleanup(func() { openEmailBrowser = old })
	return func() (string, string, int) { return gotPath, gotCmd, launches }
}

func previewCompose(body string) ComposeModel {
	c := NewCompose(config.AccountConfig{User: "me@example.com", Signature: "Alice"}, nil, nil)
	c.bodyInput.SetValue(body)
	return c
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const previewBody = "Results are **in**:\n\n- first\n- second\n\nSee https://example.com/x\n\n⁂\n"

func TestPreviewDocumentKeepsTheHTMLPartVerbatim(t *testing.T) {
	part := "<p>Hi <b>there</b></p>\n<table>\n</table>\n"
	doc := previewDocument(`Q3 <results> & "more"`, part, time.Time{})
	if !strings.Contains(doc, part) {
		t.Fatalf("the HTML part must appear byte for byte:\n%s", doc)
	}
	for _, want := range []string{"<!doctype html>", `<meta charset="utf-8">`, "Content-Security-Policy", "default-src 'none'",
		"<title>Preview (not sent): Q3 &lt;results&gt; &amp; &#34;more&#34;</title>"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q:\n%s", want, doc)
		}
	}
	if strings.Contains(previewDocument("", part, time.Time{}), "(not sent):") {
		t.Fatal("an empty subject must not leave a dangling colon in the title")
	}
}

func TestStaticPreviewHasNoScript(t *testing.T) {
	doc := previewDocument("s", "<p>x</p>\n", time.Time{})
	if strings.Contains(doc, "<script") || strings.Contains(doc, "script-src") || strings.Contains(doc, "live-until") {
		t.Fatalf("a static page must carry no script or live marker:\n%s", doc)
	}
}

func TestLivePreviewAllowsOnlyItsOwnScript(t *testing.T) {
	until := time.Unix(1_800_000_000, 0)
	doc := previewDocument("s", "<p>x</p>\n<script>alert(1)</script>\n", until)
	sum := sha256.Sum256([]byte(livePreviewScript))
	wantHash := "script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	for _, want := range []string{wantHash, `<meta name="tidemail-live-until" content="1800000000">`, "<script>" + livePreviewScript + "</script>"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q:\n%s", want, doc)
		}
	}
	csp := previewCSP(true)
	if strings.Count(csp, "script-src") != 1 || !strings.HasSuffix(csp, wantHash) || strings.Contains(csp, "'unsafe-eval'") || strings.Contains(csp, "script-src 'unsafe-inline'") {
		t.Fatalf("only the hashed script may run: %s", csp)
	}
	if !strings.Contains(doc, "<p>x</p>\n<script>alert(1)</script>\n") {
		t.Fatal("the draft's own HTML must still appear verbatim (the policy blocks it, the page does not edit it)")
	}
}

func TestPreviewDraftWritesExactlyTheHTMLThatSendingUses(t *testing.T) {
	opened := captureBrowser(t)
	c := previewCompose(previewBody)
	c.subjectInput.SetValue("Results")

	c, cmd := c.previewDraft()
	if cmd == nil || c.isErr || !c.preview.live {
		t.Fatalf("want a live preview: cmd=%v live=%v status=%q", cmd, c.preview.live, c.statusMsg)
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("the first F4 must open the browser and start the tick chain, got %#v", batch)
	}
	if msg, ok := batch[0]().(draftPreviewMsg); !ok || msg.err != nil {
		t.Fatalf("opening failed: %#v", msg)
	}
	path, _, launches := opened()
	if launches != 1 || path != c.preview.path {
		t.Fatalf("launches=%d path=%q want %q", launches, path, c.preview.path)
	}

	sent, err := mailapp.PrepareCompose(config.AccountConfig{User: "me@example.com", Signature: "Alice"},
		db.Draft{To: "reader@example.com", BodyText: previewBody})
	if err != nil {
		t.Fatal(err)
	}
	html := readFile(t, c.preview.path)
	if !strings.Contains(html, sent.HTMLBody) {
		t.Fatalf("preview lacks the exact HTML part that is sent.\npreview:\n%s\nsent:\n%s", html, sent.HTMLBody)
	}
	for _, want := range []string{"<strong>in</strong>", "<ul>\n<li>first</li>", "-- <br>\nAlice", "Preview (not sent): Results"} {
		if !strings.Contains(html, want) {
			t.Fatalf("preview lacks %q", want)
		}
	}
}

func TestPreviewDraftNeedsNoRecipient(t *testing.T) {
	captureBrowser(t)
	c, cmd := previewCompose("Just **text**\n").previewDraft()
	if cmd == nil || c.isErr {
		t.Fatalf("an unaddressed draft must preview: status=%q", c.statusMsg)
	}
	if html := readFile(t, c.preview.path); !strings.Contains(html, "<strong>text</strong>") {
		t.Fatalf("html = %q", html)
	}
}

func TestPreviewDraftReportsABadBodyAndWritesNothing(t *testing.T) {
	opened := captureBrowser(t)
	c, cmd := previewCompose("a\n\n⁂\n\nb\n\n⁂\n").previewDraft()
	if cmd != nil || !c.isErr || !strings.Contains(c.statusMsg, "⁂") || c.preview.path != "" || c.preview.live {
		t.Fatalf("want the marker error and no preview: cmd=%v status=%q state=%+v", cmd, c.statusMsg, c.preview)
	}
	if _, _, n := opened(); n != 0 {
		t.Fatal("nothing may be opened for an invalid body")
	}
}

func TestPreviewDraftUsesTheConfiguredBrowser(t *testing.T) {
	opened := captureBrowser(t)
	t.Cleanup(func() { setComposeBrowser("") })
	setComposeBrowser("  firefox --new-window ")
	_, cmd := previewCompose("hi\n").previewDraft()
	cmd().(tea.BatchMsg)[0]()
	if _, configured, n := opened(); n != 1 || configured != "firefox --new-window" {
		t.Fatalf("configured browser = %q launches=%d", configured, n)
	}
}

func TestPreviewKeyShowsStatusAndSecondPressDoesNotStartAnotherTick(t *testing.T) {
	opened := captureBrowser(t)
	c := previewCompose("hello\n")
	c, cmd, exit := c.Update(tea.KeyMsg{Type: tea.KeyF4}, DefaultKeys)
	if exit || cmd == nil {
		t.Fatalf("F4 must start the preview: exit=%v cmd=%v", exit, cmd)
	}
	batch := cmd().(tea.BatchMsg)
	c, _, _ = c.Update(batch[0](), DefaultKeys)
	if c.isErr || c.statusMsg != "Preview opened in browser" {
		t.Fatalf("status = %q (err=%v)", c.statusMsg, c.isErr)
	}

	c, cmd, _ = c.Update(tea.KeyMsg{Type: tea.KeyF4}, DefaultKeys)
	if _, isBatch := cmd().(tea.BatchMsg); isBatch {
		t.Fatal("a second F4 must reopen the page without starting a second tick chain")
	}
	if _, _, n := opened(); n != 2 {
		t.Fatalf("the second F4 should open the browser again, launches = %d", n)
	}
}

func TestPreviewFailureIsShown(t *testing.T) {
	c := previewCompose("hi\n")
	c, _, _ = c.Update(draftPreviewMsg{err: os.ErrPermission}, DefaultKeys)
	if !c.isErr || !strings.Contains(c.statusMsg, "preview:") {
		t.Fatalf("status = %q (err=%v)", c.statusMsg, c.isErr)
	}
}

func startedPreview(t *testing.T, body string) ComposeModel {
	t.Helper()
	captureBrowser(t)
	c, cmd := previewCompose(body).previewDraft()
	if cmd == nil || !c.preview.live {
		t.Fatalf("setup: preview did not start: %q", c.statusMsg)
	}
	return c
}

func TestLiveTickRewritesOnChangeOnly(t *testing.T) {
	c := startedPreview(t, "first draft\n")
	path := c.preview.path
	before := readFile(t, path)

	// Unchanged draft: nothing is rewritten, even though the tick keeps going.
	if err := os.WriteFile(path, []byte("SENTINEL"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, cmd, _ := c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if cmd == nil {
		t.Fatal("the tick chain must continue while live")
	}
	if got := readFile(t, path); got != "SENTINEL" {
		t.Fatalf("an unchanged draft must not rewrite the page, got %q", got)
	}

	// Edit the body and the subject: the next tick rewrites with both.
	c.bodyInput.SetValue("second draft **bold**\n")
	c.subjectInput.SetValue("New subject")
	c, _, _ = c.Update(draftPreviewTickMsg{}, DefaultKeys)
	after := readFile(t, path)
	if !strings.Contains(after, "second draft <strong>bold</strong>") || !strings.Contains(after, "Preview (not sent): New subject") {
		t.Fatalf("edit not reflected:\n%s", after)
	}
	if after == before {
		t.Fatal("page should have changed")
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("the temporary file must not be left behind")
	}
}

func TestLiveTickHeartbeatKeepsTheDeadlineAhead(t *testing.T) {
	c := startedPreview(t, "same\n")
	path := c.preview.path
	if err := os.WriteFile(path, []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.preview.wrote = time.Now().Add(-2 * livePreviewHeartbeat)
	c, _, _ = c.Update(draftPreviewTickMsg{}, DefaultKeys)
	got := readFile(t, path)
	if !strings.Contains(got, "tidemail-live-until") || !strings.Contains(got, "same") {
		t.Fatalf("an overdue heartbeat must rewrite the unchanged draft:\n%s", got)
	}
	if time.Since(c.preview.wrote) > time.Minute {
		t.Fatal("the write time must be refreshed")
	}
}

func TestLiveTickKeepsLastGoodPageWhileBodyIsInvalid(t *testing.T) {
	c := startedPreview(t, "good draft\n")
	path := c.preview.path
	c.bodyInput.SetValue("a\n\n⁂\n\nb\n\n⁂\n") // two markers: does not convert
	c, cmd, _ := c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if cmd == nil || c.isErr {
		t.Fatalf("an invalid body mid-typing must not stop or flag the live preview: cmd=%v status=%q", cmd, c.statusMsg)
	}
	if got := readFile(t, path); !strings.Contains(got, "good draft") {
		t.Fatalf("the last good page must stay:\n%s", got)
	}
	// Fixing the body brings the page up to date.
	c.bodyInput.SetValue("fixed draft\n")
	c, _, _ = c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if got := readFile(t, path); !strings.Contains(got, "fixed draft") {
		t.Fatalf("the page must follow the fix:\n%s", got)
	}
}

func TestStaleTickIsIgnoredWhenNotLive(t *testing.T) {
	captureBrowser(t)
	c := previewCompose("x\n")
	c, cmd, _ := c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if cmd != nil || c.preview.path != "" {
		t.Fatalf("a tick from an earlier composer must end its chain and write nothing: cmd=%v state=%+v", cmd, c.preview)
	}
}

func TestLiveTickStopsAndReportsWhenTheFileCannotBeWritten(t *testing.T) {
	c := startedPreview(t, "x\n")
	if err := os.RemoveAll(c.preview.path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	// Make the temp path a non-empty directory so the write fails.
	if err := os.MkdirAll(c.preview.path+".tmp/sub", 0o700); err != nil {
		t.Fatal(err)
	}
	c.bodyInput.SetValue("changed\n")
	c, _, _ = c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if c.preview.live || !c.isErr || !strings.Contains(c.statusMsg, "live preview stopped") {
		t.Fatalf("a write failure must stop the live preview and say so: live=%v status=%q", c.preview.live, c.statusMsg)
	}
}
