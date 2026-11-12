package mailapp

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/smtp"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

var imgAccount = config.AccountConfig{User: "sender@example.com", Signature: "Alice"}

func imgDraft(body string, atts ...db.DraftAttachment) db.Draft {
	return db.Draft{To: "reader@example.com", BodyText: body, Attachments: atts}
}

func att(name string, data []byte) db.DraftAttachment {
	return db.DraftAttachment{Filename: name, Data: data}
}

func TestPrepareComposeSendsAttachedImagesInline(t *testing.T) {
	logo, data := testPNG(t, 240, 80), []byte("a,b\n1,2\n")
	body := "Hello\n\n![The logo](logo.png)\n\nSee the table.\n\n⁂\n"
	msg, err := PrepareCompose(imgAccount, imgDraft(body, att("logo.png", logo), att("data.csv", data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.InlineImages) != 1 || msg.InlineImages[0].Name != "logo.png" || !bytes.Equal(msg.InlineImages[0].Data, logo) {
		t.Fatalf("inline images: %+v", msg.InlineImages)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].Name != "data.csv" {
		t.Fatalf("the referenced image must not also be an attachment, the other file must stay: %+v", msg.Attachments)
	}
	cid := msg.InlineImages[0].ContentID
	if !strings.Contains(msg.HTMLBody, `<img src="cid:`+cid+`" alt="The logo" width="240" height="80" style="max-width:100%;height:auto">`) {
		t.Fatalf("HTML:\n%s", msg.HTMLBody)
	}
	if strings.Contains(msg.Body, "logo.png") || !strings.Contains(msg.Body, "[image: The logo]") || !strings.Contains(msg.Body, "-- \nAlice") {
		t.Fatalf("the plain part must say [image: alt] and still carry the signature:\n%s", msg.Body)
	}
	if !strings.Contains(msg.HTMLBody, "-- <br>\nAlice") {
		t.Fatal("the HTML signature must survive the image handling")
	}
}

func TestPrepareComposeRefusesBadImagesNamingThem(t *testing.T) {
	cases := map[string]string{
		"![x](nothere.png)\n\n⁂\n":    "nothere.png",
		"![x](/home/me/a.png)\n\n⁂\n": "file paths are not supported",
		"![x](~/a.png)\n":             "file paths are not supported",
		"![x](notes.txt)\n":           "not a PNG, JPEG or GIF",
	}
	for body, want := range cases {
		_, err := PrepareCompose(imgAccount, imgDraft(body, att("notes.txt", []byte("hello"))))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want it to contain %q", body, err, want)
		}
	}
}

func TestPreviewShowsTheVeryBytesThatAreSent(t *testing.T) {
	big := testPNG(t, 1500, 500)
	body := "![chart](big.png){width=300}\n\n⁂\n"
	atts := []db.DraftAttachment{att("big.png", big)}
	sent, err := PrepareCompose(imgAccount, imgDraft(body, atts...))
	if err != nil {
		t.Fatal(err)
	}
	html, problems, err := ComposeHTML(imgAccount, body, atts)
	if err != nil || len(problems) != 0 {
		t.Fatalf("%v %v", problems, err)
	}
	const prefix = `src="data:image/png;base64,`
	i := strings.Index(html, prefix)
	if i < 0 {
		t.Fatalf("the preview must inline the image as a data: URI:\n%.300s", html)
	}
	payload := html[i+len(prefix):]
	raw, err := base64.StdEncoding.DecodeString(payload[:strings.Index(payload, `"`)])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, sent.InlineImages[0].Data) {
		t.Fatal("the preview and the sent message must carry byte-identical image data")
	}
	// Everything but the image reference is the same HTML.
	norm := func(h string) string {
		a := strings.Index(h, "<img ")
		b := strings.Index(h[a:], ">") + a + 1
		return h[:a] + "<IMG>" + h[b:]
	}
	if norm(html) != norm(sent.HTMLBody) {
		t.Fatalf("preview and sent HTML differ beyond the image src:\n%s\n---\n%s", html, sent.HTMLBody)
	}
	if !strings.Contains(html, `width="300" height="100"`) || !strings.Contains(sent.HTMLBody, `width="300" height="100"`) {
		t.Fatalf("both must show the 300 px request:\n%s", html)
	}
}

func TestPreviewReportsImageProblemsWithoutFailing(t *testing.T) {
	html, problems, err := ComposeHTML(imgAccount, "![gone](gone.png) and ![p](/a/b.png)\n", nil)
	if err != nil {
		t.Fatalf("a missing image must not stop the preview: %v", err)
	}
	if len(problems) != 2 || !strings.Contains(problems[0], "gone.png") || !strings.Contains(problems[1], "file paths") || !strings.Contains(html, "<img") {
		t.Fatalf("problems=%v html=%s", problems, html)
	}
}

func TestPrepareHandlesImagesForTheCLIPathToo(t *testing.T) {
	logo := testPNG(t, 50, 50)
	msg, err := Prepare(imgAccount, imgDraft("![l](logo.png)\n", att("logo.png", logo), att("b.txt", []byte("x"))), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.InlineImages) != 1 || len(msg.Attachments) != 1 || msg.Attachments[0].Name != "b.txt" || strings.Contains(msg.Body, "logo.png") {
		t.Fatalf("inline=%d attachments=%+v body=%q", len(msg.InlineImages), msg.Attachments, msg.Body)
	}
	if _, err := Prepare(imgAccount, imgDraft("![l](missing.png)\n"), false); err == nil {
		t.Fatal("the CLI path must refuse a reference to an image that is not attached")
	}
}

func TestSameImageTwiceIsOnePart(t *testing.T) {
	msg, err := PrepareCompose(imgAccount, imgDraft("![a](Logo.PNG) ![b](logo.png)\n", att("logo.png", testPNG(t, 40, 40))))
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.InlineImages) != 1 || strings.Count(msg.HTMLBody, "cid:"+msg.InlineImages[0].ContentID) != 2 {
		t.Fatalf("parts=%d\n%s", len(msg.InlineImages), msg.HTMLBody)
	}
}

func TestRemoteImagesAreKeptAsLinksAndInThePlainText(t *testing.T) {
	msg, err := PrepareCompose(imgAccount, imgDraft("![r](https://example.com/a.png)\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.InlineImages) != 0 || !strings.Contains(msg.HTMLBody, `src="https://example.com/a.png"`) || !strings.Contains(msg.Body, "![r](https://example.com/a.png)") {
		t.Fatalf("%+v\n%s\n%s", msg.InlineImages, msg.HTMLBody, msg.Body)
	}
}

func TestInlineImagesWorkWithoutTheSignatureMarkerToo(t *testing.T) {
	msg, err := PrepareCompose(imgAccount, imgDraft("no marker here ![l](logo.png)\n", att("logo.png", testPNG(t, 20, 20))))
	if err != nil || len(msg.InlineImages) != 1 {
		t.Fatalf("%v %+v", err, msg.InlineImages)
	}
}

func TestBuiltMessageIsRelatedAndPlainPartHasNoImageReferences(t *testing.T) {
	msg, err := PrepareCompose(imgAccount, imgDraft("x ![l](logo.png)\n\n⁂\n", att("logo.png", testPNG(t, 20, 20))))
	if err != nil {
		t.Fatal(err)
	}
	msg.EnsureIdentity("sender@example.com")
	raw := string(smtp.BuildEntity(msg, false))
	for _, want := range []string{"multipart/related", "Content-Id: <" + msg.InlineImages[0].ContentID + ">", "Content-Disposition: inline"} {
		if !strings.Contains(raw, want) {
			t.Errorf("built message lacks %q", want)
		}
	}
	if strings.Contains(raw, "![l](logo.png)") {
		t.Error("the Markdown reference must not reach the recipient in any part")
	}
}
