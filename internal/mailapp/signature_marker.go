package mailapp

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/inlineimg"
	"github.com/allisonhere/tidemail/internal/smtp"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// SignatureMarker is reserved in the interactive composer's body. CLI callers
// continue to use Prepare's explicit signature option instead.
const SignatureMarker = "⁂"

// PrepareCompose resolves the editable marker once, before queueing or PGP
// protection. A body without a marker is intentionally unsigned, including old
// drafts. The draft itself is never modified, so undo/edit restores the marker.
func PrepareCompose(account config.AccountConfig, draft db.Draft, opts ...ComposeOption) (smtp.OutgoingMessage, error) {
	images := inlineimg.NewResolver(inlineimg.ModeSend, imageSources(draft.Attachments))
	o := applyOptions(opts)
	if o.replyTo == "" {
		o.replyTo = draft.InReplyTo
	}
	body, html, err := composeBody(draft.BodyText, account.Signature, o, images)
	if err != nil {
		return smtp.OutgoingMessage{}, err
	}
	if err := imageError(images); err != nil {
		return smtp.OutgoingMessage{}, err
	}
	// Reuse all ordinary identity, recipient, and attachment validation without
	// rendering the original Markdown twice or appending another signature.
	draft.BodyText = ""
	draft.Attachments = withoutInline(draft.Attachments, images)
	msg, err := Prepare(account, draft, false)
	if err != nil {
		return msg, err
	}
	msg.Body, msg.HTMLBody, msg.InlineImages = body, html, inlineParts(images)
	return msg, nil
}

// ComposeHTML returns the HTML part that sending would produce for a draft body
// and the account's signature, without validating recipients or sender. Sending
// and the browser preview both come through composeBody, so they cannot differ.
// Images are shown as data: URIs of the very bytes that would be sent (resized
// and re-encoded). Problems with images are returned alongside the HTML, which
// then shows them as broken; sending refuses them instead. err is for a body
// that cannot be converted at all, such as two signature markers.
func ComposeHTML(account config.AccountConfig, body string, attachments []db.DraftAttachment, opts ...ComposeOption) (html string, problems []string, err error) {
	images := inlineimg.NewResolver(inlineimg.ModePreview, imageSources(attachments))
	_, html, err = composeBody(body, account.Signature, applyOptions(opts), images)
	return html, images.Problems(), err
}

// markerSpot is where a reserved marker sits in a body.
type markerSpot struct {
	pos        int // byte offset of the marker
	start, end int // its whole line, without the newline
}

// locateMarker finds the single marker in body, or reports ok=false when there
// is none. It fails when there are several or the marker is not alone on its
// line, with the messages given.
func locateMarker(body, marker, tooMany, notAlone string) (spot markerSpot, ok bool, err error) {
	switch strings.Count(body, marker) {
	case 0:
		return spot, false, nil
	case 1:
	default:
		return spot, false, fmt.Errorf("%s", tooMany)
	}
	spot.pos = strings.Index(body, marker)
	spot.start = strings.LastIndex(body[:spot.pos], "\n") + 1
	spot.end = len(body)
	if next := strings.IndexByte(body[spot.pos:], '\n'); next >= 0 {
		spot.end = spot.pos + next
	}
	if strings.Trim(body[spot.start:spot.end], " \t") != marker {
		return spot, false, fmt.Errorf("%s", notAlone)
	}
	return spot, true, nil
}

// markerParagraph finds the top-level single-line paragraph holding the marker.
func markerParagraph(doc ast.Node, source []byte, spot markerSpot, marker string) ast.Node {
	for node := doc.FirstChild(); node != nil; node = node.NextSibling() {
		if node.Kind() != ast.KindParagraph || node.Lines().Len() != 1 {
			continue
		}
		line := node.Lines().At(0)
		if line.Start <= spot.pos && spot.pos < line.Stop && strings.TrimSpace(string(line.Value(source))) == marker {
			return node
		}
	}
	return nil
}

// replaceLine swaps the marker's line (and its newline, so neighbours stay
// where they are) for replacement.
func replaceLine(body string, spot markerSpot, replacement string) string {
	end := spot.end
	if end < len(body) {
		end++
	}
	return body[:spot.start] + replacement + body[end:]
}

func composeBody(body, signature string, opts composeOptions, images *inlineimg.Resolver) (string, string, error) {
	var html string
	const (
		sigMany  = "keep only one ⁂ signature marker, or delete all markers to send unsigned"
		sigAlone = "place ⁂ alone on a line, or delete it to omit the signature"
	)
	quoteMany := "keep only one " + QuoteMarker + " quote marker, or delete it to send without the quoted message"
	quoteAlone := "place " + QuoteMarker + " alone on a line, or delete it to omit the quoted message"
	norm := strings.ReplaceAll(body, "\r\n", "\n")
	if _, _, err := locateMarker(norm, SignatureMarker, sigMany, sigAlone); err != nil {
		return "", "", err
	}
	if _, _, err := locateMarker(norm, QuoteMarker, quoteMany, quoteAlone); err != nil {
		return "", "", err
	}
	if !strings.Contains(norm, SignatureMarker) && !strings.Contains(norm, QuoteMarker) {
		html = smtp.MarkdownToHTMLOpts(body, imageOption(images))
	} else {
		body = norm
		sig, hasSig, err := locateMarker(body, SignatureMarker, sigMany, sigAlone)
		if err != nil {
			return "", "", err
		}
		quoteSpot, hasQuote, err := locateMarker(body, QuoteMarker, quoteMany, quoteAlone)
		if err != nil {
			return "", "", err
		}
		var quote *Quote
		if hasQuote {
			if quote, err = opts.resolveQuote(); err != nil {
				return "", "", err
			}
		}
		source := []byte(body)
		// Same converter as MarkdownToHTML plus the marker renderers: every new
		// message carries a marker, so it must not get a different dialect.
		md := smtp.NewMarkdown(goldmark.WithRendererOptions(renderer.WithNodeRenderers(
			util.Prioritized(signatureRenderer{}, 100), util.Prioritized(quoteRenderer{}, 101))), imageOption(images))
		doc := md.Parser().Parse(text.NewReader(source))
		sigText := strings.TrimSpace(signature)
		if hasSig {
			marker := markerParagraph(doc, source, sig, SignatureMarker)
			if marker == nil {
				return "", "", fmt.Errorf("put ⁂ in its own paragraph outside quotes, lists, and code blocks (add blank lines), or delete it")
			}
			doc.ReplaceChild(doc, marker, &signatureBlock{signature: sigText})
		}
		if hasQuote {
			marker := markerParagraph(doc, source, quoteSpot, QuoteMarker)
			if marker == nil {
				return "", "", fmt.Errorf("put %s in its own paragraph outside quotes, lists, and code blocks (add blank lines), or delete it", QuoteMarker)
			}
			doc.ReplaceChild(doc, marker, &quoteBlock{html: quoteHTML(*quote)})
		}
		var rendered bytes.Buffer
		if err := md.Renderer().Render(&rendered, source, doc); err != nil {
			return "", "", err
		}
		html = rendered.String()
		// Text part: replace later lines first so earlier offsets stay valid.
		type edit struct {
			spot        markerSpot
			replacement string
		}
		var edits []edit
		if hasSig {
			r := ""
			if sigText != "" {
				r = "-- \n" + sigText + "\n"
			}
			edits = append(edits, edit{sig, r})
		}
		if hasQuote {
			edits = append(edits, edit{quoteSpot, quoteText(*quote) + "\n"})
		}
		sort.Slice(edits, func(i, j int) bool { return edits[i].spot.start > edits[j].spot.start })
		for _, e := range edits {
			body = replaceLine(body, e.spot, e.replacement)
		}
	}
	// This also catches a reserved rune introduced by an HTML entity or the
	// configured signature, rather than only checking literal Markdown input.
	if strings.Contains(body, SignatureMarker) || strings.Contains(html, SignatureMarker) {
		return "", "", fmt.Errorf("remove the reserved ⁂ symbol from message text or the configured signature")
	}
	if strings.Contains(body, QuoteMarker) || strings.Contains(html, QuoteMarker) {
		return "", "", fmt.Errorf("remove the reserved %s symbol from the message text", QuoteMarker)
	}
	// The plain-text part must not carry the image references: they would show a
	// recipient a file name, or a wall of base64.
	return inlineimg.PlainText(body), html, nil
}

var signatureBlockKind = ast.NewNodeKind("ComposeSignature")

type signatureBlock struct {
	ast.BaseBlock
	signature string
}

func (n *signatureBlock) Kind() ast.NodeKind            { return signatureBlockKind }
func (n *signatureBlock) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

type signatureRenderer struct{}

func (signatureRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(signatureBlockKind, func(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, err := w.WriteString(smtp.SignatureHTML(node.(*signatureBlock).signature))
			return ast.WalkSkipChildren, err
		}
		return ast.WalkContinue, nil
	})
}

var quoteBlockKind = ast.NewNodeKind("ComposeQuote")

type quoteBlock struct {
	ast.BaseBlock
	html string
}

func (n *quoteBlock) Kind() ast.NodeKind            { return quoteBlockKind }
func (n *quoteBlock) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

type quoteRenderer struct{}

func (quoteRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(quoteBlockKind, func(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, err := w.WriteString(node.(*quoteBlock).html)
			return ast.WalkSkipChildren, err
		}
		return ast.WalkContinue, nil
	})
}
