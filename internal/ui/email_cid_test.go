package ui

import (
	"encoding/base64"
	imapClient "github.com/allisonhere/tidemail/internal/imap"
	"os"
	"strings"
	"testing"
)

// png1 is a 1x1 PNG.
var png1, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

// relatedMessage builds a multipart/related message whose HTML is html and which
// carries the given image parts (content id -> content type).
func relatedMessage(html string, parts map[string]string, order ...string) []byte {
	var b strings.Builder
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: multipart/related; boundary=r\r\n\r\n--r\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + html + "\r\n")
	for _, id := range order {
		b.WriteString("--r\r\nContent-Type: " + parts[id] + "\r\nContent-Transfer-Encoding: base64\r\nContent-ID: <" + id + ">\r\nContent-Disposition: inline\r\n\r\n" + base64.StdEncoding.EncodeToString(png1) + "\r\n")
	}
	b.WriteString("--r--\r\n")
	return []byte(b.String())
}

func exportOf(t *testing.T, raw []byte, allowImages bool) (html string, blocked, missing int) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	original := openEmailBrowser
	t.Cleanup(func() { openEmailBrowser = original })
	openEmailBrowser = func(_, path string) error {
		data, err := os.ReadFile(path)
		html = string(data)
		return err
	}
	blocked, missing, err := openEmailHTMLCounting(raw, "", allowImages)
	if err != nil {
		t.Fatal(err)
	}
	return html, blocked, missing
}

func TestExportEmbedsInlineImagesAsDataURIs(t *testing.T) {
	raw := relatedMessage(`<p><img src="cid:Logo@x"> <img src="CID:logo@x" alt="again"></p>`,
		map[string]string{"logo@x": "image/png"}, "logo@x")
	html, _, missing := exportOf(t, raw, false)
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png1)
	if strings.Count(html, want) != 2 || strings.Contains(html, "cid:") || missing != 0 {
		t.Fatalf("missing=%d, html:\n%s", missing, html)
	}
	if !strings.Contains(html, "img-src data:;") {
		t.Errorf("remote images must stay blocked by the CSP:\n%s", html)
	}
}

func TestExportEmbedsCSSBackgroundCIDs(t *testing.T) {
	raw := relatedMessage(`<style>.h{background:url(cid:bg@x)}</style><div class="h" style="background-image:url('cid:bg@x')">x</div><table background="cid:bg@x"></table>`,
		map[string]string{"bg@x": "image/png"}, "bg@x")
	html, _, missing := exportOf(t, raw, false)
	if strings.Contains(html, "cid:") || strings.Count(html, "data:image/png;base64,") != 3 || missing != 0 {
		t.Fatalf("missing=%d, html:\n%s", missing, html)
	}
}

func TestExportCountsInlineImagesItCannotShow(t *testing.T) {
	raw := relatedMessage(`<img src="cid:gone@x"><img src="cid:logo.svg@x"><img src="cid:ok@x"><img src="cid:gone@x"><img src="data:image/gif;base64,R0lGOD">`,
		map[string]string{"ok@x": "image/png", "logo.svg@x": "image/svg+xml"}, "ok@x", "logo.svg@x")
	html, _, missing := exportOf(t, raw, false)
	if missing != 2 { // gone@x (twice, counted once) and the svg
		t.Fatalf("missing = %d, want 2", missing)
	}
	if !strings.Contains(html, "data:image/gif;base64,R0lGOD") || strings.Count(html, "data:image/png;base64,") != 1 {
		t.Errorf("html:\n%s", html)
	}
	if got := missingInlineNote(1) + missingInlineNote(2) + missingInlineNote(0); !strings.Contains(got, "1 inline image could not be shown") || !strings.Contains(got, "2 inline images") {
		t.Errorf("status note: %q", got)
	}
}

func TestExportKeepsBlockingRemoteImagesAlongsideInlineOnes(t *testing.T) {
	raw := relatedMessage(`<img src="cid:ok@x"><img src="https://t.example/p.gif">`, map[string]string{"ok@x": "image/png"}, "ok@x")
	html, blocked, _ := exportOf(t, raw, false)
	if blocked != 1 || strings.Contains(html, "t.example") || !strings.Contains(html, "data:image/png") {
		t.Fatalf("blocked=%d html:\n%s", blocked, html)
	}
	html, blocked, _ = exportOf(t, raw, true)
	if blocked != 0 || !strings.Contains(html, "t.example") {
		t.Fatalf("with images allowed: blocked=%d html:\n%s", blocked, html)
	}
}

func TestExportImageBudgetIsEnforcedAndRepeatsAreFree(t *testing.T) {
	e := newCIDEmbedding(nil)
	e.images["big"] = imapImage("big", "image/png", exportImageMaxBytes+1)
	e.images["a"] = imapImage("a", "image/png", 9<<20)
	e.images["b"] = imapImage("b", "image/png", 9<<20)
	e.images["c"] = imapImage("c", "image/png", 9<<20)
	for _, id := range []string{"a", "a", "b"} { // a twice costs once; a+b = 18 MB fits
		if _, ok := e.embed(id); !ok {
			t.Fatalf("%s should embed", id)
		}
	}
	if _, ok := e.embed("c"); ok {
		t.Error("c would exceed the 25 MB total")
	}
	if _, ok := e.embed("big"); ok {
		t.Error("an image over 10 MB must be refused")
	}
	if e.missing() != 2 {
		t.Errorf("missing = %d", e.missing())
	}
}

func imapImage(id, typ string, n int) imapClient.InlineImage {
	return imapClient.InlineImage{ContentID: id, ContentType: typ, Data: make([]byte, n)}
}
