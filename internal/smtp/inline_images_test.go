package smtp

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

// part is one parsed MIME entity: its headers, and either a body or children.
type part struct {
	mediaType string
	params    map[string]string
	header    map[string][]string
	body      []byte
	children  []part
}

func parseEntity(t *testing.T, raw []byte) part {
	t.Helper()
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(m.Body)
	return parsePart(t, m.Header, body)
}

func parsePart(t *testing.T, header map[string][]string, body []byte) part {
	t.Helper()
	get := func(k string) string {
		for name, v := range header {
			if strings.EqualFold(name, k) && len(v) > 0 {
				return v[0]
			}
		}
		return ""
	}
	mt, params, err := mime.ParseMediaType(get("Content-Type"))
	if err != nil {
		t.Fatalf("Content-Type %q: %v", get("Content-Type"), err)
	}
	p := part{mediaType: mt, params: params, header: header, body: body}
	if strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			sub, err := mr.NextRawPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(sub)
			p.children = append(p.children, parsePart(t, sub.Header, b))
		}
	}
	return p
}

func (p part) h(k string) string {
	for name, v := range p.header {
		if strings.EqualFold(name, k) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func shape(p part) string {
	if len(p.children) == 0 {
		return p.mediaType
	}
	var kids []string
	for _, c := range p.children {
		kids = append(kids, shape(c))
	}
	return p.mediaType + "[" + strings.Join(kids, ", ") + "]"
}

var testImage = InlineImage{ContentID: "abc123@tidemail.local", Name: "logo.png", ContentType: "image/png", Data: bytes.Repeat([]byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4}, 40)}

func inlineMessage() OutgoingMessage {
	return OutgoingMessage{
		MessageID:    "<id@example>",
		Body:         "plain [image: logo]",
		HTMLBody:     `<p>Hi</p><img src="cid:abc123@tidemail.local">`,
		InlineImages: []InlineImage{testImage},
	}
}

func TestInlineImagesGoInRelatedInsideAlternative(t *testing.T) {
	for _, mailSafe := range []bool{false, true} {
		root := parseEntity(t, BuildEntity(inlineMessage(), mailSafe))
		want := "multipart/alternative[text/plain, multipart/related[text/html, image/png]]"
		if got := shape(root); got != want {
			t.Fatalf("mailSafe=%v: structure = %s, want %s", mailSafe, got, want)
		}
		related := root.children[1]
		if related.params["type"] != "text/html" {
			t.Errorf("multipart/related must name its root type: %v", related.params)
		}
		img := related.children[1]
		if img.h("Content-Id") != "<abc123@tidemail.local>" || !strings.HasPrefix(img.h("Content-Disposition"), "inline") || img.h("Content-Transfer-Encoding") != "base64" {
			t.Errorf("image headers: %v", img.header)
		}
		if _, p, _ := mime.ParseMediaType(img.h("Content-Disposition")); p["filename"] != "logo.png" {
			t.Errorf("filename: %v", p)
		}
		if !strings.Contains(string(related.children[0].body), "cid:abc123@tidemail.local") {
			t.Error("the HTML part must reference the image by cid")
		}
	}
}

func TestInlineImageBytesSurviveTheEncoding(t *testing.T) {
	root := parseEntity(t, BuildEntity(inlineMessage(), false))
	img := root.children[1].children[1]
	var decoded bytes.Buffer
	dec := newBase64Reader(img.body)
	if _, err := io.Copy(&decoded, dec); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Bytes(), testImage.Data) {
		t.Fatal("the image bytes changed in transit")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(img.body)), "\r\n") {
		if len(line) > 76 {
			t.Fatalf("base64 line of %d columns", len(line))
		}
	}
}

func TestInlineImagesWithAttachmentsStayInsideMixed(t *testing.T) {
	msg := inlineMessage()
	msg.Attachments = []Attachment{{Name: "data.csv", Data: []byte("a,b\n1,2\n")}}
	root := parseEntity(t, BuildEntity(msg, false))
	want := "multipart/mixed[multipart/alternative[text/plain, multipart/related[text/html, image/png]], application/octet-stream]"
	if got := shape(root); got != want {
		t.Fatalf("structure = %s, want %s", got, want)
	}
	if _, p, _ := mime.ParseMediaType(root.children[1].h("Content-Disposition")); p["filename"] != "data.csv" {
		t.Fatal("the ordinary attachment must stay an attachment")
	}
}

func TestMessagesWithoutInlineImagesAreUnchanged(t *testing.T) {
	msg := OutgoingMessage{MessageID: "<id@example>", Body: "plain", HTMLBody: "<p>html</p>"}
	if got, want := shape(parseEntity(t, BuildEntity(msg, false))), "multipart/alternative[text/plain, text/html]"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	msg.Attachments = []Attachment{{Name: "a.txt", Data: []byte("x")}}
	if got, want := shape(parseEntity(t, BuildEntity(msg, false))), "multipart/mixed[multipart/alternative[text/plain, text/html], application/octet-stream]"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestTwoInlineImagesKeepTheirOrderAndIDs(t *testing.T) {
	msg := inlineMessage()
	msg.InlineImages = append(msg.InlineImages, InlineImage{ContentID: "def456@tidemail.local", Name: "chart ünï.png", ContentType: "image/png", Data: []byte("second")})
	related := parseEntity(t, BuildEntity(msg, false)).children[1]
	if len(related.children) != 3 || related.children[1].h("Content-Id") != "<abc123@tidemail.local>" || related.children[2].h("Content-Id") != "<def456@tidemail.local>" {
		t.Fatalf("order/ids: %s", shape(related))
	}
	if _, p, err := mime.ParseMediaType(related.children[2].h("Content-Disposition")); err != nil || p["filename"] != "chart ünï.png" {
		t.Fatalf("a non-ASCII file name must round-trip: %v %v", p, err)
	}
}

// newBase64Reader decodes the base64 body of a part (CRLF-separated lines).
func newBase64Reader(b []byte) io.Reader {
	clean := strings.NewReplacer("\r", "", "\n", "").Replace(string(b))
	return base64.NewDecoder(base64.StdEncoding, strings.NewReader(clean))
}
