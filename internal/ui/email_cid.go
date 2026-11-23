package ui

import (
	"encoding/base64"
	"strings"

	imapClient "github.com/allisonhere/tidemail/internal/imap"
)

// Limits on the images embedded in an export, so a message full of pictures
// cannot make a huge file.
const (
	exportImageMaxBytes   = 10 << 20
	exportImageTotalBytes = 25 << 20
)

// exportImageTypes are the image types embedded as data: URIs. SVG is left out:
// it can carry script and external references.
var exportImageTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true,
}

// cidEmbedding resolves a message's cid: references for an export and counts
// the ones it could not.
type cidEmbedding struct {
	images     map[string]imapClient.InlineImage
	total      int
	uris       map[string]string // already embedded, so a repeated logo is counted once
	unresolved map[string]bool
}

func newCIDEmbedding(images []imapClient.InlineImage) *cidEmbedding {
	e := &cidEmbedding{images: map[string]imapClient.InlineImage{}, unresolved: map[string]bool{}, uris: map[string]string{}}
	for _, img := range images {
		key := cidLookupKey(img.ContentID)
		if _, dup := e.images[key]; !dup {
			e.images[key] = img
		}
	}
	return e
}

func cidLookupKey(id string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(id), "<>"))
}

// embed is the cidEmbedder: a data: URI for id, or false (counted) when the
// message has no such image, it is not a raster type, or the budget is spent.
func (e *cidEmbedding) embed(id string) (string, bool) {
	key := cidLookupKey(id)
	if uri, done := e.uris[key]; done {
		return uri, true
	}
	img, ok := e.images[key]
	if !ok || !exportImageTypes[img.ContentType] || len(img.Data) == 0 || len(img.Data) > exportImageMaxBytes || e.total+len(img.Data) > exportImageTotalBytes {
		e.unresolved[key] = true
		return "", false
	}
	e.total += len(img.Data)
	uri := "data:" + img.ContentType + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
	e.uris[key] = uri
	return uri, true
}

// missing is how many distinct cid: images could not be shown.
func (e *cidEmbedding) missing() int { return len(e.unresolved) }
