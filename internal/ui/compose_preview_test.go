package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/mailapp"
	"github.com/allisonhere/tidemail/internal/previewserver"
)

// browserLog records what the browser launcher was asked to open.
type browserLog struct {
	targets    []string
	configured string
}

func captureBrowser(t *testing.T) *browserLog {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	log := &browserLog{}
	old := openEmailBrowser
	openEmailBrowser = func(configured, target string) error {
		log.configured = configured
		log.targets = append(log.targets, target)
		return nil
	}
	t.Cleanup(func() { openEmailBrowser = old })
	return log
}

func serverMode(t *testing.T) {
	t.Helper()
	setComposePreviewServer(true)
	t.Cleanup(func() { setComposePreviewServer(false) })
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

// httpGet fetches path (a URL) the way a browser on this machine would.
func httpGet(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func serverDoc(t *testing.T, srv *previewserver.Server) (title, html string) {
	t.Helper()
	var d struct{ Title, HTML string }
	if err := json.Unmarshal([]byte(httpGet(t, srv.URL()+"doc")), &d); err != nil {
		t.Fatal(err)
	}
	return d.Title, d.HTML
}

const previewBody = "Results are **in**:\n\n- first\n- second\n\nSee https://example.com/x\n\n⁂\n"

func sentHTML(t *testing.T, body string) string {
	t.Helper()
	sent, err := mailapp.PrepareCompose(config.AccountConfig{User: "me@example.com", Signature: "Alice"},
		db.Draft{To: "reader@example.com", BodyText: body})
	if err != nil {
		t.Fatal(err)
	}
	return sent.HTMLBody
}

// ---- static page (the default) -------------------------------------------

func TestPreviewDocumentKeepsTheHTMLPartVerbatimAndRunsNoScript(t *testing.T) {
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
	if strings.Contains(doc, "<script") || strings.Contains(doc, "script-src") || strings.Contains(doc, "refresh") || strings.Contains(doc, "reload") {
		t.Fatalf("the static page must carry no script and never refresh itself:\n%s", doc)
	}
	if strings.Contains(previewDocument("", part), "(not sent):") {
		t.Fatal("an empty subject must not leave a dangling colon in the title")
	}
}

func TestPreviewDraftWritesExactlyTheHTMLThatSendingUses(t *testing.T) {
	log := captureBrowser(t)
	c := previewCompose(previewBody)
	c.subjectInput.SetValue("Results")

	c, cmd := c.previewDraft()
	if cmd == nil || c.isErr || c.preview.live || c.preview.srv != nil {
		t.Fatalf("static mode: want one command, no tick chain, no server: cmd=%v state=%+v status=%q", cmd, c.preview, c.statusMsg)
	}
	if _, isBatch := cmd().(tea.BatchMsg); isBatch {
		t.Fatal("static mode must not start a tick chain")
	}
	if len(log.targets) != 1 || log.targets[0] != c.preview.path {
		t.Fatalf("browser targets = %v, want [%s]", log.targets, c.preview.path)
	}
	html := readFile(t, c.preview.path)
	if !strings.Contains(html, sentHTML(t, previewBody)) {
		t.Fatalf("preview lacks the exact HTML part that is sent:\n%s", html)
	}
	for _, want := range []string{"<strong>in</strong>", "<ul>\n<li>first</li>", "-- <br>\nAlice", "Preview (not sent): Results"} {
		if !strings.Contains(html, want) {
			t.Fatalf("preview lacks %q", want)
		}
	}
}

func TestStaticPreviewNeverRewritesItself(t *testing.T) {
	captureBrowser(t)
	c, _ := previewCompose("first draft\n").previewDraft()
	path := c.preview.path
	before := readFile(t, path)
	c.bodyInput.SetValue("changed text\n")
	// A stray tick (there is no timer in static mode) and any amount of typing change nothing on disk.
	c, cmd, _ := c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if cmd != nil {
		t.Fatalf("static mode has no tick chain, got a command")
	}
	if got := readFile(t, path); got != before || strings.Contains(got, "changed text") {
		t.Fatalf("the file must only change when F4 is pressed:\n%s", got)
	}
}

func TestPressingF4AgainRewritesTheSameFileAndOpensIt(t *testing.T) {
	log := captureBrowser(t)
	c, first := previewCompose("first draft\n").previewDraft()
	first() // the browser launch runs as a command
	path := c.preview.path
	c.bodyInput.SetValue("second draft\n")
	c, cmd, _ := c.Update(tea.KeyMsg{Type: tea.KeyF4}, DefaultKeys)
	cmd()
	if c.preview.path != path {
		t.Fatalf("the file name must stay stable so an open tab can be reloaded: %q -> %q", path, c.preview.path)
	}
	if got := readFile(t, path); !strings.Contains(got, "second draft") || strings.Contains(got, "first draft") {
		t.Fatalf("F4 must rewrite the file:\n%s", got)
	}
	if len(log.targets) != 2 {
		t.Fatalf("F4 opens the browser each time, targets = %v", log.targets)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("the temporary file must not be left behind")
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
	log := captureBrowser(t)
	c, cmd := previewCompose("a\n\n⁂\n\nb\n\n⁂\n").previewDraft()
	if cmd != nil || !c.isErr || !strings.Contains(c.statusMsg, "⁂") || c.preview.path != "" {
		t.Fatalf("want the marker error and no preview: cmd=%v status=%q state=%+v", cmd, c.statusMsg, c.preview)
	}
	if len(log.targets) != 0 {
		t.Fatal("nothing may be opened for an invalid body")
	}
}

func TestPreviewDraftUsesTheConfiguredBrowser(t *testing.T) {
	log := captureBrowser(t)
	t.Cleanup(func() { setComposeBrowser("") })
	setComposeBrowser("  firefox ")
	_, cmd := previewCompose("hi\n").previewDraft()
	cmd()
	if log.configured != "firefox" {
		t.Fatalf("configured browser = %q", log.configured)
	}
}

func TestPreviewKeyShowsAStatusThatNamesTheRefreshKey(t *testing.T) {
	captureBrowser(t)
	c := previewCompose("hello\n")
	c, cmd, exit := c.Update(tea.KeyMsg{Type: tea.KeyF4}, DefaultKeys)
	if exit || cmd == nil {
		t.Fatalf("F4 must start the preview: exit=%v cmd=%v", exit, cmd)
	}
	c, _, _ = c.Update(cmd(), DefaultKeys)
	if c.isErr || !strings.Contains(c.statusMsg, "opened") || !strings.Contains(c.statusMsg, "f4 again") {
		t.Fatalf("status = %q (err=%v)", c.statusMsg, c.isErr)
	}
}

func TestPreviewFailureIsShown(t *testing.T) {
	c := previewCompose("hi\n")
	c, _, _ = c.Update(draftPreviewMsg{err: os.ErrPermission}, DefaultKeys)
	if !c.isErr || !strings.Contains(c.statusMsg, "preview:") {
		t.Fatalf("status = %q (err=%v)", c.statusMsg, c.isErr)
	}
}

// ---- live preview server (opt-in) ----------------------------------------

func TestServerModeServesTheExactHTMLAndWritesNoFile(t *testing.T) {
	log := captureBrowser(t)
	serverMode(t)
	c := previewCompose(previewBody)
	c.subjectInput.SetValue("Results")

	c, cmd := c.previewDraft()
	srv := c.preview.srv
	if srv == nil {
		t.Fatalf("server mode must start a server: status=%q", c.statusMsg)
	}
	t.Cleanup(srv.Close)
	if cmd == nil || !c.preview.live || c.preview.path != "" {
		t.Fatalf("want a live preview and no file: cmd=%v state=%+v", cmd, c.preview)
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("the first F4 must open the browser and start the tick chain, got %#v", batch)
	}
	batch[0]()
	if len(log.targets) != 1 || log.targets[0] != srv.URL() || !strings.HasPrefix(log.targets[0], "http://127.0.0.1:") {
		t.Fatalf("the browser must be sent to the loopback URL, targets = %v", log.targets)
	}
	title, html := serverDoc(t, srv)
	if html != sentHTML(t, previewBody) || title != "Preview (not sent): Results" {
		t.Fatalf("served doc differs from the sent HTML:\ntitle=%q\n%s", title, html)
	}
	if page := httpGet(t, srv.URL()); !strings.Contains(page, sentHTML(t, previewBody)) {
		t.Fatalf("the first page must carry the sent HTML verbatim:\n%s", page)
	}
	dir, _ := emailExportDir()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("server mode must not write the draft to disk, found %d file(s) in %s", len(entries), filepath.Base(dir))
	}
}

func TestServerModeSecondF4ReopensWithoutAnotherTickChain(t *testing.T) {
	log := captureBrowser(t)
	serverMode(t)
	c, _ := previewCompose("hello\n").previewDraft()
	t.Cleanup(c.preview.srv.Close)
	url := c.preview.srv.URL()
	c, cmd, _ := c.Update(tea.KeyMsg{Type: tea.KeyF4}, DefaultKeys)
	if _, isBatch := cmd().(tea.BatchMsg); isBatch {
		t.Fatal("a second F4 must not start a second tick chain")
	}
	if c.preview.srv.URL() != url || len(log.targets) != 1 || log.targets[0] != url {
		t.Fatalf("the same server must be reused and reopened: url %q -> %q, targets %v", url, c.preview.srv.URL(), log.targets)
	}
}

func TestLiveTickPushesChangesAndKeepsTheLastGoodPage(t *testing.T) {
	captureBrowser(t)
	serverMode(t)
	c, _ := previewCompose("first draft\n").previewDraft()
	srv := c.preview.srv
	t.Cleanup(srv.Close)

	c.bodyInput.SetValue("second draft **bold**\n")
	c.subjectInput.SetValue("New subject")
	c, cmd, _ := c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if cmd == nil {
		t.Fatal("the tick chain must continue while the server is up")
	}
	title, html := serverDoc(t, srv)
	if !strings.Contains(html, "second draft <strong>bold</strong>") || title != "Preview (not sent): New subject" {
		t.Fatalf("the edit was not pushed: title=%q html=%q", title, html)
	}

	c.bodyInput.SetValue("a\n\n⁂\n\nb\n\n⁂\n") // two markers: does not convert
	c, cmd, _ = c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if cmd == nil || c.isErr {
		t.Fatalf("an invalid body mid-typing must not stop or flag the preview: cmd=%v status=%q", cmd, c.statusMsg)
	}
	if _, html := serverDoc(t, srv); !strings.Contains(html, "second draft") {
		t.Fatalf("the last good page must stay: %q", html)
	}
	c.bodyInput.SetValue("fixed draft\n")
	c, _, _ = c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if _, html := serverDoc(t, srv); !strings.Contains(html, "fixed draft") {
		t.Fatalf("the page must follow the fix: %q", html)
	}
}

func TestLiveTickTouchesTheServerSoItDoesNotIdleOut(t *testing.T) {
	captureBrowser(t)
	serverMode(t)
	old := startPreviewServer
	startPreviewServer = func() (*previewserver.Server, error) { return previewserver.Start(400 * time.Millisecond) }
	t.Cleanup(func() { startPreviewServer = old })
	c, _ := previewCompose("x\n").previewDraft()
	srv := c.preview.srv
	t.Cleanup(srv.Close)
	for i := 0; i < 6; i++ { // 900 ms of ticks: well past the 400 ms idle limit
		time.Sleep(150 * time.Millisecond)
		c, _, _ = c.Update(draftPreviewTickMsg{}, DefaultKeys)
		if !srv.Alive() {
			t.Fatal("ticks must keep the server alive")
		}
	}
	// Once the composer stops ticking (it closed), the server ends by itself.
	deadline := time.Now().Add(3 * time.Second)
	for srv.Alive() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if srv.Alive() {
		t.Fatal("the server must shut itself down after the ticks stop")
	}
}

func TestLiveTickEndsWhenTheServerIsGoneAndF4StartsANewOne(t *testing.T) {
	captureBrowser(t)
	serverMode(t)
	c, _ := previewCompose("x\n").previewDraft()
	first := c.preview.srv
	first.Close()
	c, cmd, _ := c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if cmd != nil || c.preview.live || !c.isErr || !strings.Contains(c.statusMsg, "live preview stopped") {
		t.Fatalf("a dead server must end the chain and say so: cmd=%v live=%v status=%q", cmd, c.preview.live, c.statusMsg)
	}
	c, cmd, _ = c.Update(tea.KeyMsg{Type: tea.KeyF4}, DefaultKeys)
	t.Cleanup(c.preview.srv.Close)
	if cmd == nil || c.preview.srv == first || !c.preview.srv.Alive() || !c.preview.live {
		t.Fatalf("F4 must start a fresh server: %+v", c.preview)
	}
}

func TestStaleTickIsIgnoredWhenNotLive(t *testing.T) {
	captureBrowser(t)
	c := previewCompose("x\n")
	c, cmd, _ := c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if cmd != nil || c.preview.srv != nil || c.preview.path != "" {
		t.Fatalf("a tick from an earlier composer must end its chain and do nothing: cmd=%v state=%+v", cmd, c.preview)
	}
}

func TestServerThatCannotStartFallsBackToTheStaticFileAndSaysSo(t *testing.T) {
	log := captureBrowser(t)
	serverMode(t)
	old := startPreviewServer
	startPreviewServer = func() (*previewserver.Server, error) { return nil, errors.New("listen tcp4: address in use") }
	t.Cleanup(func() { startPreviewServer = old })

	c, cmd := previewCompose(previewBody).previewDraft()
	if cmd == nil || c.isErr || c.preview.live || c.preview.srv != nil || c.preview.path == "" {
		t.Fatalf("want a static preview: cmd=%v state=%+v status=%q", cmd, c.preview, c.statusMsg)
	}
	c, _, _ = c.Update(cmd(), DefaultKeys)
	if !strings.Contains(c.statusMsg, "static file") || !strings.Contains(c.statusMsg, "address in use") {
		t.Fatalf("the fallback must be explained: %q", c.statusMsg)
	}
	if len(log.targets) != 1 || log.targets[0] != c.preview.path || !strings.Contains(readFile(t, c.preview.path), sentHTML(t, previewBody)) {
		t.Fatalf("the static file must be written and opened: %v", log.targets)
	}
}

func TestServerModeOffByDefault(t *testing.T) {
	if composePreviewServer {
		t.Fatal("the live preview server must be off unless configured")
	}
	if config.DefaultConfig().Display.ComposePreviewServer {
		t.Fatal("DefaultConfig must leave the live preview server off")
	}
}

// ---- inline images in the preview ----------------------------------------

func smallPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), 99, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func attach(c ComposeModel, name string, data []byte) ComposeModel {
	c.attachments = append(c.attachments, attachmentFile{Name: name, Data: data})
	return c
}

func TestPreviewPolicyAllowsRemoteImagesButNothingElse(t *testing.T) {
	if !strings.Contains(draftPreviewCSP, "img-src data: https: http:") || !strings.Contains(draftPreviewCSP, "default-src 'none'") {
		t.Fatalf("policy = %s", draftPreviewCSP)
	}
	for _, bad := range []string{"script-src", "connect-src", "frame-src", "unsafe-eval"} {
		if strings.Contains(draftPreviewCSP, bad) {
			t.Errorf("the static preview policy must not allow %q: %s", bad, draftPreviewCSP)
		}
	}
}

func TestStaticPreviewInlinesAttachedImagesAndNeverShowsThePath(t *testing.T) {
	captureBrowser(t)
	c := attach(previewCompose("Look: ![The logo](logo.png)\n"), "logo.png", smallPNG(t, 120, 40))
	c, cmd := c.previewDraft()
	if cmd == nil || c.isErr {
		t.Fatalf("status = %q", c.statusMsg)
	}
	html := readFile(t, c.preview.path)
	for _, want := range []string{`src="data:image/png;base64,`, `alt="The logo"`, `width="120" height="40"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("preview lacks %q:\n%.400s", want, html)
		}
	}
	if strings.Contains(html, "logo.png") {
		t.Fatal("the attachment name must not appear in the page")
	}
}

func TestPreviewNamesAnUnattachedImageButStillOpens(t *testing.T) {
	log := captureBrowser(t)
	c, cmd := previewCompose("![gone](gone.png) and ![p](/home/me/x.png)\n").previewDraft()
	if cmd == nil || c.isErr {
		t.Fatalf("a problem image must not stop the preview: status=%q", c.statusMsg)
	}
	c, _, _ = c.Update(cmd(), DefaultKeys)
	if !strings.Contains(c.statusMsg, "gone.png") || !strings.Contains(c.statusMsg, "file paths are not supported") {
		t.Fatalf("the status must name the problems: %q", c.statusMsg)
	}
	if len(log.targets) != 1 {
		t.Fatal("the page must still open")
	}
}

func TestLivePreviewFollowsAnImageBeingAttachedAndFixed(t *testing.T) {
	captureBrowser(t)
	serverMode(t)
	c, _ := previewCompose("Here: ![logo](logo.png)\n").previewDraft()
	srv := c.preview.srv
	t.Cleanup(srv.Close)
	if _, html := serverDoc(t, srv); strings.Contains(html, "data:image") {
		t.Fatal("nothing is attached yet")
	}
	if c.preview.problem == "" {
		t.Fatal("the missing image must be recorded as a problem")
	}

	// Attaching the file is not a text edit: the tick must still notice and refresh.
	c = attach(c, "logo.png", smallPNG(t, 80, 80))
	c, _, _ = c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if _, html := serverDoc(t, srv); !strings.Contains(html, `src="data:image/png;base64,`) {
		t.Fatalf("the attached image must appear in the live page:\n%.300s", html)
	}
	if c.preview.problem != "" || c.isErr || !strings.Contains(c.statusMsg, "images fixed") {
		t.Fatalf("the status must say the problem went away: %q (err=%v)", c.statusMsg, c.isErr)
	}

	// Writing a bad reference while live reports it once, in the status line.
	c.bodyInput.SetValue("Here: ![logo](logo.png) ![x](nope.png)\n")
	c, _, _ = c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if !c.isErr || !strings.Contains(c.statusMsg, "nope.png") {
		t.Fatalf("status = %q (err=%v)", c.statusMsg, c.isErr)
	}
	c.statusMsg = "cleared by the user"
	c, _, _ = c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if c.statusMsg != "cleared by the user" {
		t.Fatal("an unchanged problem must not be re-announced on every tick")
	}
}
