package mailapp

import (
	"bytes"
	"fmt"
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
func PrepareCompose(account config.AccountConfig, draft db.Draft) (smtp.OutgoingMessage, error) {
	images := inlineimg.NewResolver(inlineimg.ModeSend, imageSources(draft.Attachments))
	body, html, err := composeBody(draft.BodyText, account.Signature, images)
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
func ComposeHTML(account config.AccountConfig, body string, attachments []db.DraftAttachment) (html string, problems []string, err error) {
	images := inlineimg.NewResolver(inlineimg.ModePreview, imageSources(attachments))
	_, html, err = composeBody(body, account.Signature, images)
	return html, images.Problems(), err
}

func composeBody(body, signature string, images *inlineimg.Resolver) (string, string, error) {
	var html string
	switch strings.Count(body, SignatureMarker) {
	case 0:
		html = smtp.MarkdownToHTMLOpts(body, imageOption(images))
	case 1:
		body = strings.ReplaceAll(body, "\r\n", "\n")
		pos := strings.Index(body, SignatureMarker)
		start := strings.LastIndex(body[:pos], "\n") + 1
		end := len(body)
		if next := strings.IndexByte(body[pos:], '\n'); next >= 0 {
			end = pos + next
		}
		if strings.Trim(body[start:end], " \t") != SignatureMarker {
			return "", "", fmt.Errorf("place ⁂ alone on a line, or delete it to omit the signature")
		}
		source := []byte(body)
		// Same converter as MarkdownToHTML plus the signature renderer: every new
		// message carries the marker, so it must not get a different dialect.
		md := smtp.NewMarkdown(goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(signatureRenderer{}, 100))), imageOption(images))
		doc := md.Parser().Parse(text.NewReader(source))
		var marker ast.Node
		for node := doc.FirstChild(); node != nil; node = node.NextSibling() {
			if node.Kind() != ast.KindParagraph || node.Lines().Len() != 1 {
				continue
			}
			line := node.Lines().At(0)
			if line.Start <= pos && pos < line.Stop && strings.TrimSpace(string(line.Value(source))) == SignatureMarker {
				marker = node
				break
			}
		}
		if marker == nil {
			return "", "", fmt.Errorf("put ⁂ in its own paragraph outside quotes, lists, and code blocks (add blank lines), or delete it")
		}
		sig := strings.TrimSpace(signature)
		doc.ReplaceChild(doc, marker, &signatureBlock{signature: sig})
		var rendered bytes.Buffer
		if err := md.Renderer().Render(&rendered, source, doc); err != nil {
			return "", "", err
		}
		html = rendered.String()
		if end < len(body) {
			end++
		} // consume the marker line's newline, not its neighbors
		replacement := ""
		if sig != "" {
			replacement = "-- \n" + sig + "\n"
		}
		body = body[:start] + replacement + body[end:]
	default:
		return "", "", fmt.Errorf("keep only one ⁂ signature marker, or delete all markers to send unsigned")
	}
	// This also catches a reserved rune introduced by an HTML entity or the
	// configured signature, rather than only checking literal Markdown input.
	if strings.Contains(body, SignatureMarker) || strings.Contains(html, SignatureMarker) {
		return "", "", fmt.Errorf("remove the reserved ⁂ symbol from message text or the configured signature")
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
