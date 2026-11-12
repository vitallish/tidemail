package inlineimg

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/util"
)

// quadrants builds an image whose four quadrants are red (top-left), green
// (top-right), blue (bottom-left) and white (bottom-right).
func quadrants(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.NRGBA
			switch {
			case x < w/2 && y < h/2:
				c = color.NRGBA{255, 0, 0, 255}
			case x >= w/2 && y < h/2:
				c = color.NRGBA{0, 255, 0, 255}
			case x < w/2:
				c = color.NRGBA{0, 0, 255, 255}
			default:
				c = color.NRGBA{255, 255, 255, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, quadrants(w, h)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// jpegWithEXIF returns a JPEG carrying an EXIF block with the given orientation
// and a recognisable "location" string standing in for GPS data.
func jpegWithEXIF(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, quadrants(w, h), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, // big-endian header, IFD0 at 8
		0, 1, // one entry
		0x01, 0x12, 0, 3, 0, 0, 0, 1, byte(orientation >> 8), byte(orientation), 0, 0, // Orientation (SHORT)
		0, 0, 0, 0} // no next IFD
	tiff = append(tiff, []byte("GPS-LATITUDE-52.5200N")...)
	seg := append([]byte("Exif\x00\x00"), tiff...)
	n := len(seg) + 2
	app1 := append([]byte{0xFF, 0xE1, byte(n >> 8), byte(n)}, seg...)
	src := plain.Bytes()
	return append(append(append([]byte{}, src[:2]...), app1...), src[2:]...)
}

func gifBytes(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{color.Black, color.White}
	g := &gif.GIF{}
	for i := 0; i < 2; i++ {
		p := image.NewPaletted(image.Rect(0, 0, 40, 20), pal)
		g.Image, g.Delay = append(g.Image, p), append(g.Delay, 10)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func near(a, b color.Color) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	d := func(x, y uint32) bool {
		if x > y {
			x, y = y, x
		}
		return y-x < 0x2800
	}
	return d(ar, br) && d(ag, bg) && d(ab, bb)
}

func TestClassify(t *testing.T) {
	for src, want := range map[string]Kind{
		"logo.png":                   KindName,
		"My Photo (1).JPG":           KindName,
		"https://example.com/a.png":  KindRemote,
		"HTTP://example.com/a.png":   KindRemote,
		"data:image/png;base64,AAAA": KindData,
		"/home/me/a.png":             KindBad,
		"~/a.png":                    KindBad,
		"./a.png":                    KindBad,
		"../a.png":                   KindBad,
		"dir/a.png":                  KindBad,
		`C:\pics\a.png`:              KindBad,
		"file:///home/me/a.png":      KindBad,
		"cid:logo@x":                 KindBad,
		"ftp://example.com/a.png":    KindBad,
		"":                           KindBad,
	} {
		if got := Classify(src); got != want {
			t.Errorf("Classify(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestWidthAttr(t *testing.T) {
	for in, want := range map[string]int{"": 0, "width=300": 300, "width=300px": 300, "WIDTH=50%": 300, "width=100%": 600, "width=1%": 6} {
		if got, err := widthAttr(in); err != nil || got != want {
			t.Errorf("widthAttr(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"width=0", "width=-5", "width=abc", "width=150%", "height=100", "width", ".center", "width=300 height=100"} {
		if _, err := widthAttr(bad); err == nil {
			t.Errorf("widthAttr(%q) must be an error", bad)
		}
	}
}

func TestProcessSizesAndPassThrough(t *testing.T) {
	small := pngBytes(t, 240, 80)
	p, err := process(small, 0)
	if err != nil || !bytes.Equal(p.data, small) || p.width != 240 || p.height != 80 {
		t.Fatalf("a small PNG passes through untouched at its own size: %+v (%v)", p, err)
	}

	big := pngBytes(t, 4000, 3000)
	p, err = process(big, 0)
	if err != nil || p.width != 600 || p.height != 450 {
		t.Fatalf("a large image is shown at 600 px wide: %+v (%v)", p, err)
	}
	if got := decode(t, p.data).Bounds(); got.Dx() != 1200 || got.Dy() != 900 {
		t.Fatalf("and embedded at 2x: %v", got)
	}

	p, _ = process(big, 300)
	if got := decode(t, p.data).Bounds(); p.width != 300 || p.height != 225 || got.Dx() != 600 {
		t.Fatalf("a requested width of 300 embeds 600 px: %+v %v", p, got)
	}
	p, _ = process(big, 1500)
	if got := decode(t, p.data).Bounds(); p.width != 1500 || got.Dx() != 2000 {
		t.Fatalf("a request above 600 is honoured, with the 2000 px embed ceiling: %+v %v", p, got)
	}
	p, _ = process(small, 900)
	if p.width != 240 || !bytes.Equal(p.data, small) {
		t.Fatalf("an image is never enlarged: %+v", p)
	}
}

func TestProcessJPEGAppliesOrientationAndDropsMetadata(t *testing.T) {
	src := jpegWithEXIF(t, 80, 40, 6) // stored sideways: must be rotated 90 degrees clockwise
	if !bytes.Contains(src, []byte("GPS-LATITUDE")) || jpegOrientation(src) != 6 {
		t.Fatal("test image lacks its EXIF block")
	}
	p, err := process(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.contentType != "image/jpeg" || p.width != 40 || p.height != 80 {
		t.Fatalf("orientation 6 swaps the dimensions: %+v", p)
	}
	if bytes.Contains(p.data, []byte("GPS-LATITUDE")) || bytes.Contains(p.data, []byte("Exif")) {
		t.Fatal("EXIF (including location) must not survive")
	}
	img := decode(t, p.data)
	if b := img.Bounds(); b.Dx() != 40 || b.Dy() != 80 {
		t.Fatalf("decoded size %v", b)
	}
	// Rotating clockwise moves top-left (red) to top-right, top-right (green) to bottom-right.
	if !near(img.At(35, 5), color.NRGBA{255, 0, 0, 255}) || !near(img.At(35, 75), color.NRGBA{0, 255, 0, 255}) || !near(img.At(5, 5), color.NRGBA{0, 0, 255, 255}) {
		t.Fatalf("the picture was not rotated upright: tr=%v br=%v tl=%v", img.At(35, 5), img.At(35, 75), img.At(5, 5))
	}
}

func TestOrientEveryCase(t *testing.T) {
	src := quadrants(20, 10) // red TL, green TR, blue BL, white BR
	R, G, B, W := color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 255, 0, 255}, color.NRGBA{0, 0, 255, 255}, color.NRGBA{255, 255, 255, 255}
	// For each orientation: the colours expected at the four corners of the result, TL TR BL BR.
	want := map[int][4]color.NRGBA{
		1: {R, G, B, W}, 2: {G, R, W, B}, 3: {W, B, G, R}, 4: {B, W, R, G},
		5: {R, B, G, W}, 6: {B, R, W, G}, 7: {W, G, B, R}, 8: {G, W, R, B},
	}
	for o, corners := range want {
		got := orient(src, o)
		b := got.Bounds()
		pts := [4]image.Point{{1, 1}, {b.Dx() - 2, 1}, {1, b.Dy() - 2}, {b.Dx() - 2, b.Dy() - 2}}
		for i, pt := range pts {
			if !near(got.At(pt.X, pt.Y), corners[i]) {
				t.Errorf("orientation %d corner %d = %v, want %v", o, i, got.At(pt.X, pt.Y), corners[i])
			}
		}
		if (o >= 5) != (b.Dx() == 10) {
			t.Errorf("orientation %d: size %v", o, b)
		}
	}
}

func TestProcessGIFUntouchedAndBadInputsRefused(t *testing.T) {
	g := gifBytes(t)
	if p, err := process(g, 0); err != nil || !bytes.Equal(p.data, g) || p.contentType != "image/gif" || p.width != 40 {
		t.Fatalf("a GIF is sent untouched so animation survives: %+v (%v)", p, err)
	}
	for name, data := range map[string][]byte{
		"text":      []byte("hello, I am not an image"),
		"svg":       []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`),
		"truncated": pngBytes(t, 50, 50)[:30],
		"empty":     {},
	} {
		if _, err := process(data, 0); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if _, err := process(make([]byte, MaxFileBytes+1), 0); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("an oversize file must be refused with the limit: %v", err)
	}
}

func TestContentIDIsStableAndContentBased(t *testing.T) {
	a, b := contentID([]byte("one")), contentID([]byte("two"))
	if a == b || a != contentID([]byte("one")) || !strings.HasSuffix(a, "@tidemail.local") {
		t.Fatalf("ids: %q %q", a, b)
	}
}

// ---- Markdown integration ------------------------------------------------

func convert(t *testing.T, r *Resolver, md string) string {
	t.Helper()
	g := goldmark.New(goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(r.Transformer(), 300))))
	var b bytes.Buffer
	if err := g.Convert([]byte(md), &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestSendModeTurnsAttachedImagesIntoCIDParts(t *testing.T) {
	logo := pngBytes(t, 240, 80)
	r := NewResolver(ModeSend, []Source{{"logo.png", logo}, {"notes.txt", []byte("x")}, {"report.pdf", []byte("%PDF")}})
	html := convert(t, r, "![The logo](LOGO.PNG)\n\nagain ![The logo](logo.png)\n")
	if len(r.Problems()) != 0 {
		t.Fatalf("problems: %v", r.Problems())
	}
	imgs := r.Images()
	if len(imgs) != 1 || imgs[0].Name != "logo.png" || imgs[0].ContentType != "image/png" || !bytes.Equal(imgs[0].Data, logo) {
		t.Fatalf("the same image used twice is one part, named after the attachment: %+v", imgs)
	}
	want := `<img src="cid:` + imgs[0].ContentID + `" alt="The logo" width="240" height="80" style="max-width:100%;height:auto">`
	if strings.Count(html, want) != 2 {
		t.Fatalf("want two references to the one part:\n%s\nwant %s", html, want)
	}
	if !r.Used("logo.PNG") || r.Used("notes.txt") || r.Used("report.pdf") {
		t.Fatal("only the referenced attachment becomes inline; others stay attachments")
	}
}

func TestPreviewModeShowsTheProcessedBytesAsDataURIs(t *testing.T) {
	big := pngBytes(t, 1800, 900)
	r := NewResolver(ModePreview, []Source{{"big.png", big}})
	html := convert(t, r, "![chart](big.png)")
	if len(r.Problems()) != 0 || len(r.Images()) != 0 || r.Used("big.png") {
		t.Fatalf("the preview collects no parts: %v %v", r.Problems(), r.Images())
	}
	i := strings.Index(html, `src="data:image/png;base64,`)
	if i < 0 {
		t.Fatalf("no data URI:\n%.200s", html)
	}
	payload := html[i+len(`src="data:image/png;base64,`):]
	payload = payload[:strings.Index(payload, `"`)]
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(t, raw).Bounds(); got.Dx() != 1200 || !strings.Contains(html, `width="600" height="300"`) {
		t.Fatalf("the preview must show what is sent (1200 px embedded, shown at 600x300): %v\n%.300s", got, html)
	}
}

func TestWidthOverrideIsConsumedAndApplied(t *testing.T) {
	r := NewResolver(ModeSend, []Source{{"big.png", pngBytes(t, 1800, 900)}})
	html := convert(t, r, "![a](big.png){width=300} and then more\n\n![b](big.png){width=50%}\n")
	if len(r.Problems()) != 0 {
		t.Fatal(r.Problems())
	}
	if strings.Contains(html, "{width") {
		t.Fatalf("the attribute must not be printed:\n%s", html)
	}
	if !strings.Contains(html, `width="300" height="150"`) || !strings.Contains(html, "/> and then more") && !strings.Contains(html, "> and then more") {
		t.Fatalf("width=300 and the trailing text must both survive:\n%s", html)
	}
	if len(r.Images()) != 1 {
		t.Fatalf("{width=50%%} is 300 px as well, so it is the same part: %d parts", len(r.Images()))
	}
}

func TestProblemsAreReportedAndImagesLeftAlone(t *testing.T) {
	r := NewResolver(ModeSend, []Source{{"notes.txt", []byte("hello")}, {"a.png", pngBytes(t, 10, 10)}})
	cases := map[string]string{
		"![x](missing.png)":              "not attached",
		"![x](/home/me/a.png)":           "file paths are not supported",
		"![x](~/a.png)":                  "file paths are not supported",
		"![x](file:///a.png)":            "file paths are not supported",
		"![x](cid:logo@tidemail)":        "file paths are not supported",
		"![x](notes.txt)":                "not a PNG, JPEG or GIF",
		"![x](data:image/png;base64,@@)": "base64",
		"![x](a.png){height=20}":         "unsupported image attribute",
	}
	for md, want := range cases {
		rr := NewResolver(ModeSend, []Source{{"notes.txt", []byte("hello")}, {"a.png", pngBytes(t, 10, 10)}})
		convert(t, rr, md)
		if p := strings.Join(rr.Problems(), " | "); !strings.Contains(p, want) {
			t.Errorf("%s: problems %q lack %q", md, p, want)
		}
		if len(rr.Images()) > 0 && !strings.Contains(md, "height=20") {
			t.Errorf("%s: no part may be made for a failing image", md)
		}
	}
	_ = r
}

func TestRemoteImagesAreNotFetchedOrEmbedded(t *testing.T) {
	r := NewResolver(ModeSend, nil)
	html := convert(t, r, "![r](https://example.com/a.png){width=200}\n\n![s](https://example.com/b.png)\n")
	if len(r.Problems()) != 0 || len(r.Images()) != 0 {
		t.Fatalf("%v %v", r.Problems(), r.Images())
	}
	for _, want := range []string{`src="https://example.com/a.png"`, `width="200"`, `style="max-width:100%;height:auto"`, `src="https://example.com/b.png"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q:\n%s", want, html)
		}
	}
}

func TestDataURIsBecomeParts(t *testing.T) {
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes(t, 30, 30))
	r := NewResolver(ModeSend, nil)
	html := convert(t, r, "![d]("+uri+")")
	if len(r.Problems()) != 0 || len(r.Images()) != 1 || r.Images()[0].Name != "image.png" || !strings.Contains(html, `src="cid:`) || strings.Contains(html, "data:image") {
		t.Fatalf("a data: image must become a CID part: %v %+v\n%s", r.Problems(), r.Images(), html)
	}
}

func TestPlainTextHidesEverythingButRemoteImages(t *testing.T) {
	uri := "data:image/png;base64," + strings.Repeat("QUJD", 50)
	in := "Intro ![The logo](logo.png) and ![](pic.png \"hover\"){width=300}.\n![path](/home/me/secret.png) ![d](" + uri + ")\n![remote](https://example.com/a.png)\nNot an image: [link](logo.png)\n"
	got := PlainText(in)
	want := "Intro [image: The logo] and [image].\n[image: path] [image: d]\n![remote](https://example.com/a.png)\nNot an image: [link](logo.png)\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	for _, leak := range []string{"secret.png", "/home/me", "QUJD", "pic.png", "{width"} {
		if strings.Contains(got, leak) {
			t.Errorf("the plain text still carries %q", leak)
		}
	}
}
