package mailapp

import (
	"errors"
	"strings"
	"testing"

	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/inlineimg"
	"github.com/allisonhere/tidemail/internal/smtp"
)

// originalWithInlineImage is a received mail whose HTML shows an inline image by
// cid, with the image part cached (as the reader does after fetching the body).
func originalWithInlineImage(t *testing.T) (db.Message, []db.Attachment) {
	t.Helper()
	m := originalWithTable()
	m.ID = 7
	m.BodyHTML = `<p>See the chart:</p><p><img src="cid:Chart1@sender" alt="Q1 chart" width="40"></p><p>Logo <img src="cid:missing@sender" alt="Our logo"></p>`
	return m, []db.Attachment{{Filename: "chart.png", ContentType: "image/png", ContentID: "chart1@sender", Inline: true, Data: testPNG(t, 40, 20)}}
}

func replyDraft() db.Draft {
	return db.Draft{To: "ann@example.com", InReplyTo: "<orig@x>", BodyText: "Thanks.\n\n⁑\n"}
}

func TestQuoteCarriesTheOriginalsInlineImageWhenAsked(t *testing.T) {
	m, parts := originalWithInlineImage(t)
	load := func(db.Message) ([]db.Attachment, error) { return parts, nil }
	msg, err := PrepareCompose(quoteAccount, replyDraft(), WithQuote("", lookupOf(m)), WithQuoteImages(load))
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.InlineImages) != 1 {
		t.Fatalf("want one related part, got %d", len(msg.InlineImages))
	}
	cid := msg.InlineImages[0].ContentID
	if !strings.Contains(msg.HTMLBody, `src="cid:`+cid+`"`) || strings.Contains(msg.HTMLBody, "Chart1@sender") {
		t.Errorf("the quote's cid must be rewritten to the attached part %q:\n%s", cid, msg.HTMLBody)
	}
	// A cid with no cached part still degrades to its alt text.
	if !strings.Contains(msg.HTMLBody, "Our logo") || strings.Contains(msg.HTMLBody, "missing@sender") {
		t.Errorf("missing part must leave alt text:\n%s", msg.HTMLBody)
	}
	// Parse the real message: related part present, no reference left in the text part.
	msg.EnsureIdentity("me@example.com")
	raw := string(smtp.BuildEntity(msg, false))
	for _, want := range []string{"multipart/related", "Content-Id: <" + cid + ">", "Content-Type: image/png"} {
		if !strings.Contains(raw, want) {
			t.Errorf("built message lacks %q", want)
		}
	}
	if strings.Contains(msg.Body, "cid:") {
		t.Errorf("text part must not carry cid references:\n%s", msg.Body)
	}
}

func TestQuoteKeepsAltTextWhenImagesAreNotAsked(t *testing.T) {
	m, _ := originalWithInlineImage(t)
	msg, err := PrepareCompose(quoteAccount, replyDraft(), WithQuote("", lookupOf(m)))
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.InlineImages) != 0 || strings.Contains(msg.HTMLBody, "cid:") || !strings.Contains(msg.HTMLBody, "Q1 chart") {
		t.Errorf("default must keep today's behaviour (alt text only): parts=%d\n%s", len(msg.InlineImages), msg.HTMLBody)
	}
}

func TestQuoteImagesSkipWhatCannotOrShouldNotBeCarried(t *testing.T) {
	m := originalWithTable()
	m.BodyHTML = `<p><img src="cid:a@x" alt="vector"> <img src="cid:b@x" alt="big one"> <img src="cid:c@x" alt="small one"></p>`
	big := testPNG(t, 1500, 1500) // noisy enough to be over the quote budget after processing is not guaranteed; pad below
	parts := []db.Attachment{
		{Filename: "a.svg", ContentType: "image/svg+xml", ContentID: "a@x", Inline: true, Data: []byte("<svg/>")},
		{Filename: "b.png", ContentType: "image/png", ContentID: "b@x", Inline: true, Data: big},
		{Filename: "c.png", ContentType: "image/png", ContentID: "c@x", Inline: true, Data: testPNG(t, 30, 30)},
	}
	load := func(db.Message) ([]db.Attachment, error) { return parts, nil }
	msg, err := PrepareCompose(quoteAccount, replyDraft(), WithQuote("", lookupOf(m)), WithQuoteImages(load))
	if err != nil {
		t.Fatalf("an unsupported or oversized quote image must never fail the send: %v", err)
	}
	if !strings.Contains(msg.HTMLBody, "vector") {
		t.Errorf("unsupported format must keep alt text:\n%s", msg.HTMLBody)
	}
	total := 0
	for _, im := range msg.InlineImages {
		total += len(im.Data)
	}
	if total > inlineimg.MaxQuoteBytes {
		t.Errorf("quote images total %d bytes, budget %d", total, inlineimg.MaxQuoteBytes)
	}
}

func TestQuoteImageBudgetIsEnforcedPerQuote(t *testing.T) {
	r := inlineimg.NewResolver(inlineimg.ModeSend, nil)
	var carried, refused int
	for i := 0; i < 6; i++ {
		// A GIF is passed through unprocessed, so its size is exact: ~1.2 MB each.
		gif := append([]byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"), make([]byte, 0)...)
		gif = append(gif[:len(gif)-1:len(gif)-1], append(make([]byte, 1_200_000+i), ';')...) // trailing bytes after the trailer keep it decodable
		if _, ok := r.EmbedQuoteImage("g.gif", gif); ok {
			carried++
		} else {
			refused++
		}
	}
	if carried == 0 || refused == 0 {
		t.Errorf("carried %d refused %d: a 5 MB budget must admit some and refuse the rest of six 1.2 MB images", carried, refused)
	}
	if len(r.Problems()) != 0 {
		t.Errorf("quote images must not record problems: %v", r.Problems())
	}
}

func TestQuoteImagesPreviewUsesDataURIs(t *testing.T) {
	m, parts := originalWithInlineImage(t)
	load := func(db.Message) ([]db.Attachment, error) { return parts, nil }
	html, problems, err := ComposeHTML(quoteAccount, replyDraft().BodyText, nil, WithQuote("<orig@x>", lookupOf(m)), WithQuoteImages(load))
	if err != nil || len(problems) != 0 {
		t.Fatalf("%v %v", err, problems)
	}
	if !strings.Contains(html, `src="data:image/png;base64,`) || strings.Contains(html, "cid:") {
		t.Errorf("preview must show the image bytes as a data URI:\n%s", html)
	}
}

func TestQuoteImageLoaderErrorIsReported(t *testing.T) {
	m, _ := originalWithInlineImage(t)
	boom := func(db.Message) ([]db.Attachment, error) { return nil, errors.New("disk gone") }
	_, err := PrepareCompose(quoteAccount, replyDraft(), WithQuote("", lookupOf(m)), WithQuoteImages(boom))
	if err == nil || !strings.Contains(err.Error(), "disk gone") {
		t.Fatalf("err = %v", err)
	}
}
