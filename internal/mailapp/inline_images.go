package mailapp

import (
	"fmt"
	"strings"

	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/inlineimg"
	"github.com/allisonhere/tidemail/internal/smtp"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/util"
)

// imageSources lists the draft's attachments as images that Markdown may refer
// to by file name.
func imageSources(attachments []db.DraftAttachment) []inlineimg.Source {
	out := make([]inlineimg.Source, 0, len(attachments))
	for _, a := range attachments {
		out = append(out, inlineimg.Source{Name: a.Filename, Data: a.Data})
	}
	return out
}

// imageOption makes a Markdown converter resolve images through r.
func imageOption(r *inlineimg.Resolver) goldmark.Option {
	return goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(r.Transformer(), 300)))
}

// imageError turns the resolver's problems into one error, or nil.
func imageError(r *inlineimg.Resolver) error {
	if p := r.Problems(); len(p) > 0 {
		return fmt.Errorf("%s", strings.Join(p, "; "))
	}
	return nil
}

// withoutInline drops the attachments that became inline images: they travel
// in the HTML part, not as separate attachments.
func withoutInline(attachments []db.DraftAttachment, r *inlineimg.Resolver) []db.DraftAttachment {
	out := make([]db.DraftAttachment, 0, len(attachments))
	for _, a := range attachments {
		if !r.Used(a.Filename) {
			out = append(out, a)
		}
	}
	return out
}

// inlineParts converts the resolver's images to message parts.
func inlineParts(r *inlineimg.Resolver) []smtp.InlineImage {
	var out []smtp.InlineImage
	for _, img := range r.Images() {
		out = append(out, smtp.InlineImage{ContentID: img.ContentID, Name: img.Name, ContentType: img.ContentType, Data: img.Data})
	}
	return out
}
