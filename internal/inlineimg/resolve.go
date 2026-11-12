// Package inlineimg turns Markdown images in a draft into inline images for the
// message. An image is attached to the draft first and referenced by its file
// name, as in ![logo](logo.png). When the message is sent, each referenced
// attachment becomes an inline MIME part (Content-ID) instead of an ordinary
// attachment; in the preview the same processed bytes are shown as data: URIs.
// File paths are deliberately not supported: the draft carries the bytes, so it
// stays self-contained and no local path reaches the recipient.
package inlineimg

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Source is a file attached to the draft that Markdown may reference by name.
type Source struct {
	Name string
	Data []byte
}

// Image is one inline image ready to be written as a MIME part.
type Image struct {
	ContentID   string // without angle brackets
	Name        string // file name for the part
	ContentType string
	Data        []byte
}

// Mode selects what an image reference becomes.
type Mode int

const (
	// ModeSend rewrites references to cid: and collects the parts to attach.
	ModeSend Mode = iota
	// ModePreview rewrites references to data: URIs of the processed bytes.
	ModePreview
)

// Kind classifies an image source.
type Kind int

const (
	KindName   Kind = iota // a bare file name: look it up among the attachments
	KindData               // a data: URI
	KindRemote             // http(s): left alone
	KindBad                // a path, file:, cid:, empty, or another scheme
)

// Classify says what an image source in Markdown refers to.
func Classify(src string) Kind {
	src = strings.TrimSpace(src)
	lower := strings.ToLower(src)
	switch {
	case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://"):
		return KindRemote
	case strings.HasPrefix(lower, "data:"):
		return KindData
	case src == "" || strings.ContainsAny(src, `/\`) || strings.HasPrefix(src, "~") || strings.Contains(src, ":"):
		return KindBad // a path, a Windows drive, or another scheme (file:, cid:, ...)
	}
	return KindName
}

// Resolver resolves the images of one conversion. Create one per draft
// conversion; it is not safe for concurrent use.
type Resolver struct {
	mode     Mode
	sources  map[string]Source // lower-cased name -> attachment
	cache    map[string]processed
	images   []Image
	seen     map[string]bool
	used     map[string]bool
	total    int
	problems []string
}

// NewResolver returns a Resolver for a draft with the given attachments.
func NewResolver(mode Mode, attachments []Source) *Resolver {
	r := &Resolver{mode: mode, sources: map[string]Source{}, cache: map[string]processed{}, seen: map[string]bool{}, used: map[string]bool{}}
	for _, a := range attachments {
		key := strings.ToLower(a.Name)
		if _, dup := r.sources[key]; !dup { // the first attachment of a name wins
			r.sources[key] = a
		}
	}
	return r
}

// Images returns the inline parts collected in ModeSend, in first-use order.
func (r *Resolver) Images() []Image { return r.images }

// Used reports whether the attachment called name became an inline image, so the
// caller can leave it out of the ordinary attachments.
func (r *Resolver) Used(name string) bool { return r.used[strings.ToLower(name)] }

// Problems lists everything wrong with the images, one sentence each.
func (r *Resolver) Problems() []string { return r.problems }

func (r *Resolver) problem(format string, args ...any) {
	r.problems = append(r.problems, fmt.Sprintf(format, args...))
}

var attrRe = regexp.MustCompile(`^\{([^{}]*)\}`)

// widthAttr parses the inside of {width=300} or {width=50%}. Percentages are of
// the MaxDisplayWidth column. It returns 0 for no request.
func widthAttr(inner string) (int, error) {
	want := 0
	for _, field := range strings.Fields(inner) {
		key, val, ok := strings.Cut(field, "=")
		if !ok || strings.ToLower(key) != "width" {
			return 0, fmt.Errorf("unsupported image attribute %q (only width=300 or width=50%% is supported)", field)
		}
		val = strings.ToLower(val)
		pct := strings.HasSuffix(val, "%")
		num, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(val, "%"), "px"))
		if err != nil || num <= 0 || (pct && num > 100) {
			return 0, fmt.Errorf("image width %q must be a pixel count like 300 or a percentage like 50%%", val)
		}
		if pct {
			num = max(1, MaxDisplayWidth*num/100)
		}
		want = num
	}
	return want, nil
}

// Transformer returns the goldmark AST transformer that rewrites image nodes.
// Add it with parser.WithASTTransformers(util.Prioritized(t, 300)).
func (r *Resolver) Transformer() parser.ASTTransformer { return transformer{r} }

type transformer struct{ r *Resolver }

func (t transformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if img, ok := n.(*ast.Image); ok && entering {
			t.r.rewrite(img, source)
		}
		return ast.WalkContinue, nil
	})
}

// takeWidth removes a {width=...} written right after the image from the text
// that follows it and returns the requested width.
func (r *Resolver) takeWidth(img *ast.Image, source []byte) int {
	next, ok := img.NextSibling().(*ast.Text)
	if !ok {
		return 0
	}
	rest := next.Segment.Value(source)
	m := attrRe.FindSubmatch(rest)
	if m == nil {
		return 0
	}
	width, err := widthAttr(string(m[1]))
	if err != nil {
		r.problem("%v", err)
		return 0
	}
	next.Segment = next.Segment.WithStart(next.Segment.Start + len(m[0]))
	if next.Segment.Len() == 0 {
		img.Parent().RemoveChild(img.Parent(), next)
	}
	return width
}

func (r *Resolver) rewrite(img *ast.Image, source []byte) {
	wantWidth := r.takeWidth(img, source)
	src := string(img.Destination)
	alt := string(img.Text(source))
	switch Classify(src) {
	case KindRemote:
		// Never fetched. The size is unknown, so only keep it from overflowing a
		// narrow client, and honour a requested width.
		img.SetAttributeString("style", []byte("max-width:100%;height:auto"))
		if wantWidth > 0 {
			img.SetAttributeString("width", []byte(strconv.Itoa(wantWidth)))
		}
	case KindData:
		data, err := decodeDataURI(src)
		if err != nil {
			r.problem("image %q: %v", shortAlt(alt), err)
			return
		}
		r.embed(img, "image", true, data, wantWidth)
	case KindName:
		att, ok := r.sources[strings.ToLower(src)]
		if !ok {
			r.problem("image %q is not attached: attach the file, then write ![%s](%s)", src, alt, src)
			return
		}
		if r.embed(img, att.Name, false, att.Data, wantWidth) && r.mode == ModeSend {
			r.used[strings.ToLower(att.Name)] = true
		}
	default:
		r.problem("image source %q: file paths are not supported; attach the file and use its name, like ![%s](logo.png)", src, alt)
	}
}

func shortAlt(alt string) string {
	if alt == "" {
		return "(no alt text)"
	}
	return alt
}

// embed processes data and points the image at it. It reports whether it did;
// on failure the image is left as written and a problem is recorded. fromData is
// true for a data: URI, which has no file name of its own.
func (r *Resolver) embed(img *ast.Image, name string, fromData bool, data []byte, wantWidth int) bool {
	key := fmt.Sprintf("%x/%d", sha256.Sum256(data), wantWidth)
	p, ok := r.cache[key]
	if !ok {
		var err error
		if p, err = process(data, wantWidth); err != nil {
			r.problem("image %q: %v", name, err)
			return false
		}
		r.cache[key] = p
	}
	switch r.mode {
	case ModePreview:
		img.Destination = []byte("data:" + p.contentType + ";base64," + base64.StdEncoding.EncodeToString(p.data))
	default:
		cid := contentID(p.data)
		if !r.seen[cid] {
			r.seen[cid] = true
			r.total += len(p.data)
			if r.total > MaxTotalBytes {
				r.problem("the inline images total %d MB; the limit is %d MB", r.total>>20, MaxTotalBytes>>20)
			}
			if fromData {
				name = "image." + p.ext
			}
			r.images = append(r.images, Image{ContentID: cid, Name: name, ContentType: p.contentType, Data: p.data})
		}
		img.Destination = []byte("cid:" + cid)
	}
	img.SetAttributeString("width", []byte(strconv.Itoa(p.width)))
	img.SetAttributeString("height", []byte(strconv.Itoa(p.height)))
	img.SetAttributeString("style", []byte("max-width:100%;height:auto"))
	return true
}

func decodeDataURI(uri string) ([]byte, error) {
	head, payload, ok := strings.Cut(uri, ",")
	if !ok || !strings.HasSuffix(strings.ToLower(head), ";base64") {
		return nil, fmt.Errorf("only base64 data: images are supported")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return nil, fmt.Errorf("the data: image is not valid base64")
	}
	return data, nil
}

var imageRe = regexp.MustCompile(`!\[([^\]]*)\]\(\s*([^)\s]*)(?:\s+"[^"]*")?\s*\)(?:\{[^{}]*\})?`)

// PlainText returns body for the plain-text part of the message: every image that
// is not a remote URL is replaced by [image: alt], so no file name, path or base64
// data reaches a reader of the text part. Remote images keep their Markdown.
func PlainText(body string) string {
	return imageRe.ReplaceAllStringFunc(body, func(m string) string {
		sub := imageRe.FindStringSubmatch(m)
		if Classify(sub[2]) == KindRemote {
			return m
		}
		if alt := strings.TrimSpace(sub[1]); alt != "" {
			return "[image: " + alt + "]"
		}
		return "[image]"
	})
}
