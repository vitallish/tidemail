package ui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/tidemail/internal/clipimage"
	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/mailapp"
)

// useFakeClipboard installs a clipboard holding the given types and data (keyed by
// MIME type) behind a fake wl-paste, for one test.
func useFakeClipboard(t *testing.T, types []string, data map[string][]byte) {
	t.Helper()
	old := clipEnv
	clipEnv = func() clipimage.Env {
		return clipimage.Env{
			GOOS:     "linux",
			Getenv:   func(k string) string { return map[string]string{"WAYLAND_DISPLAY": "wayland-1"}[k] },
			LookPath: func(string) (string, error) { return "/usr/bin/wl-paste", nil },
			Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if len(args) == 1 && args[0] == "--list-types" {
					return []byte(strings.Join(types, "\n") + "\n"), nil
				}
				return data[args[2]], nil // --no-newline --type <mime>
			},
		}
	}
	t.Cleanup(func() { clipEnv = old })
}

func screenshot(t *testing.T, w, h int, seed uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x) + seed, uint8(y), seed, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func composeModel(t *testing.T) Model {
	t.Helper()
	m := NewModel(nil, config.DefaultConfig(), "dev", false)
	m.overlay = overlayCompose
	m.compose = NewCompose(config.AccountConfig{User: "me@example.com"}, nil, nil)
	m.compose.bodyInput.SetValue("Look at this: ")
	m.compose.focusedField = composeFieldBody
	m.compose.bodyInput.Focus()
	return m
}

// press runs Ctrl+V through the model and feeds the clipboard's answer back.
func pressCtrlV(t *testing.T, m Model) (Model, tea.Msg) {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	if cmd == nil {
		t.Fatal("Ctrl+V must start a clipboard read")
	}
	msg := cmd()
	next, _ = next.(Model).Update(msg)
	return next.(Model), msg
}

func TestCtrlVPastesAnImageThatIsTheOnlyThingOnTheClipboard(t *testing.T) {
	shot := screenshot(t, 200, 100, 1)
	useFakeClipboard(t, []string{"image/png"}, map[string][]byte{"image/png": shot})
	m := composeModel(t)
	m.compose.focusedField = composeFieldSubject // paste from a header field

	m, msg := pressCtrlV(t, m)
	if _, ok := msg.(ClipboardImageMsg); !ok {
		t.Fatalf("want an image message, got %T", msg)
	}
	if len(m.compose.attachments) != 1 || m.compose.attachments[0].Name != "image-1.png" || !bytes.Equal(m.compose.attachments[0].Data, shot) {
		t.Fatalf("attachments: %+v", m.compose.attachments)
	}
	if body := m.compose.bodyInput.Value(); !strings.Contains(body, "![](image-1.png)") || !strings.HasPrefix(body, "Look at this: ") {
		t.Fatalf("the reference must go into the body at the cursor, body = %q", body)
	}
	if m.compose.focusedField != composeFieldBody {
		t.Fatal("focus must move to the body")
	}
	if !m.compose.dirty || !strings.Contains(m.statusMsg, "pasted image-1.png") || m.statusErr {
		t.Fatalf("dirty=%v status=%q err=%v", m.compose.dirty, m.statusMsg, m.statusErr)
	}
}

func TestCtrlVStillPastesTextWhenTextIsOnTheClipboardToo(t *testing.T) {
	useFakeClipboard(t, []string{"text/html", "text/plain", "image/png"}, map[string][]byte{"image/png": screenshot(t, 20, 20, 2)})
	old := clipboardReadCmd
	clipboardReadCmd = func() tea.Cmd { return func() tea.Msg { return ClipboardReadMsg{Text: "from a web page"} } }
	t.Cleanup(func() { clipboardReadCmd = old })

	m, msg := pressCtrlV(t, composeModel(t))
	if _, ok := msg.(ClipboardReadMsg); !ok {
		t.Fatalf("text plus an image must paste the text, got %T", msg)
	}
	if len(m.compose.attachments) != 0 || !strings.Contains(m.compose.bodyInput.Value(), "from a web page") {
		t.Fatalf("attachments=%d body=%q", len(m.compose.attachments), m.compose.bodyInput.Value())
	}
}

func TestCtrlVFallsBackToTextWhenTheClipboardCannotBeInspected(t *testing.T) {
	old := clipboardReadCmd // the default hermetic clipboard has no tool: probing fails
	clipboardReadCmd = func() tea.Cmd { return func() tea.Msg { return ClipboardReadMsg{Text: "plain"} } }
	t.Cleanup(func() { clipboardReadCmd = old })
	m, msg := pressCtrlV(t, composeModel(t))
	if _, ok := msg.(ClipboardReadMsg); !ok || !strings.Contains(m.compose.bodyInput.Value(), "plain") {
		t.Fatalf("msg=%T body=%q", msg, m.compose.bodyInput.Value())
	}
}

func TestCtrlVShowsTheReasonWhenTheImageCannotBeRead(t *testing.T) {
	useFakeClipboard(t, []string{"image/png"}, map[string][]byte{"image/png": []byte("not an image")})
	m, _ := pressCtrlV(t, composeModel(t))
	if !m.statusErr || !strings.Contains(m.statusMsg, "paste image:") || !strings.Contains(m.statusMsg, "not a valid image") || len(m.compose.attachments) != 0 {
		t.Fatalf("status=%q err=%v attachments=%d", m.statusMsg, m.statusErr, len(m.compose.attachments))
	}
}

func TestPaletteCommandForcesAnImagePasteAndExplainsAnEmptyClipboard(t *testing.T) {
	useFakeClipboard(t, []string{"text/plain"}, nil)
	msg := clipboardImageCmd()()
	got, ok := msg.(ClipboardImageMsg)
	if !ok || !got.Forced || got.Err == nil {
		t.Fatalf("%#v", msg)
	}
	next, _ := composeModel(t).Update(msg)
	m := next.(Model)
	if !m.statusErr || !strings.Contains(m.statusMsg, "the clipboard has no image") {
		t.Fatalf("status = %q", m.statusMsg)
	}

	// With text AND an image the palette still pastes the image: that is what it was asked for.
	useFakeClipboard(t, []string{"text/plain", "image/png"}, map[string][]byte{"image/png": screenshot(t, 10, 10, 3)})
	next, _ = composeModel(t).Update(clipboardImageCmd()())
	if m := next.(Model); len(m.compose.attachments) != 1 {
		t.Fatalf("attachments: %d, status %q", len(m.compose.attachments), m.statusMsg)
	}
}

func TestPaletteListsThePasteImageCommand(t *testing.T) {
	m := composeModel(t)
	found := false
	for _, it := range m.composeCommandItems() {
		if it.id == "compose-paste-image" && it.enabled && it.label == "Paste image from clipboard" {
			found = true
		}
	}
	if !found {
		t.Fatal("the compose command palette must offer Paste image from clipboard")
	}
}

func TestPastedImagesAreNamedWithoutCollisions(t *testing.T) {
	c := NewCompose(config.AccountConfig{}, nil, nil)
	c.attachments = []attachmentFile{{Name: "IMAGE-1.PNG", Data: []byte("a")}, {Name: "image-3.png", Data: []byte("b")}}
	c, _ = c.pasteImage(clipimage.Image{Data: screenshot(t, 5, 5, 1), Ext: "png"})
	c, _ = c.pasteImage(clipimage.Image{Data: screenshot(t, 5, 5, 2), Ext: "png"})
	names := []string{}
	for _, a := range c.attachments {
		names = append(names, a.Name)
	}
	if got := strings.Join(names, ","); got != "IMAGE-1.PNG,image-3.png,image-2.png,image-4.png" {
		t.Fatalf("names = %s", got)
	}
}

func TestPastingTheSameImageTwiceAddsAReferenceNotAnAttachment(t *testing.T) {
	c := NewCompose(config.AccountConfig{}, nil, nil)
	img := clipimage.Image{Data: screenshot(t, 8, 8, 4), Ext: "png"}
	c, _ = c.pasteImage(img)
	c, status := c.pasteImage(img)
	if len(c.attachments) != 1 || strings.Count(c.bodyInput.Value(), "![](image-1.png)") != 2 || !strings.Contains(status, "already attached") {
		t.Fatalf("attachments=%d body=%q status=%q", len(c.attachments), c.bodyInput.Value(), status)
	}
}

func TestPastedJPEGAndGIFKeepTheirExtension(t *testing.T) {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 10, 10)), nil); err != nil {
		t.Fatal(err)
	}
	c, _ := NewCompose(config.AccountConfig{}, nil, nil).pasteImage(clipimage.Image{Data: b.Bytes(), Ext: "jpg"})
	if c.attachments[0].Name != "image-1.jpg" || !strings.Contains(c.bodyInput.Value(), "![](image-1.jpg)") {
		t.Fatalf("%+v %q", c.attachments, c.bodyInput.Value())
	}
}

func TestPasteInVimNormalModeInsertsTheReferenceAsText(t *testing.T) {
	setEditorVimMode(true)
	t.Cleanup(func() { setEditorVimMode(false) })
	c := NewCompose(config.AccountConfig{}, nil, nil)
	c.bodyInput.SetValue("text")
	c.bodyInput.Focus()
	c.bodyInput.ed.StartInsert()
	c.bodyInput, _ = c.bodyInput.Update(tea.KeyMsg{Type: tea.KeyEsc}) // back to NORMAL
	if c.bodyInput.Mode() != "NORMAL" {
		t.Fatalf("setup: mode %q", c.bodyInput.Mode())
	}
	c, _ = c.pasteImage(clipimage.Image{Data: screenshot(t, 6, 6, 5), Ext: "png"})
	if body := c.bodyInput.Value(); !strings.Contains(body, "![](image-1.png)") {
		t.Fatalf("in Vim normal mode the reference must be inserted, not run as commands: %q", body)
	}
}

func TestPastedImageShowsUpInTheLivePreviewWithNoProblems(t *testing.T) {
	captureBrowser(t)
	serverMode(t)
	c := previewCompose("A screenshot:\n")
	c, _ = c.previewDraft()
	srv := c.preview.srv
	t.Cleanup(srv.Close)

	c, _ = c.pasteImage(clipimage.Image{Data: screenshot(t, 300, 200, 6), Ext: "png"})
	c, _, _ = c.Update(draftPreviewTickMsg{}, DefaultKeys)
	if _, html := serverDoc(t, srv); !strings.Contains(html, `src="data:image/png;base64,`) || !strings.Contains(html, `width="300" height="200"`) {
		t.Fatalf("the pasted image must appear in the live page:\n%.300s", html)
	}
	html, problems, err := mailapp.ComposeHTML(config.AccountConfig{User: "me@example.com"}, c.bodyInput.Value(), c.draftAttachments())
	if err != nil || len(problems) != 0 || !strings.Contains(html, "data:image/png") {
		t.Fatalf("problems=%v err=%v", problems, err)
	}
}
