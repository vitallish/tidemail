// Package quotehtml prepares a received message for quoting inside a reply.
//
// The quoted original is put into mail we send, so unlike the viewer's
// sanitizer (which makes a safe file to open locally) this is an allowlist: only
// known-harmless tags and attributes are rebuilt, everything else is dropped or
// unwrapped. Structure that matters in a quote (tables, lists, links, emphasis)
// survives; scripts, styles blocks, forms, frames, tracking-size images, images
// that cannot load for the recipient, and event handlers do not.
package quotehtml

import (
	"bytes"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// dropped are removed together with everything inside them.
var dropped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Head: true, atom.Title: true, atom.Iframe: true,
	atom.Frame: true, atom.Frameset: true, atom.Object: true, atom.Embed: true, atom.Applet: true,
	atom.Link: true, atom.Base: true, atom.Meta: true, atom.Noscript: true, atom.Svg: true,
	atom.Math: true, atom.Video: true, atom.Audio: true, atom.Source: true, atom.Track: true,
	atom.Canvas: true, atom.Input: true, atom.Button: true, atom.Select: true, atom.Textarea: true,
	atom.Template: true,
}

// allowed tags are kept; any other element is unwrapped (its children stay).
var allowed = map[atom.Atom]bool{
	atom.A: true, atom.Abbr: true, atom.B: true, atom.Blockquote: true, atom.Br: true,
	atom.Caption: true, atom.Center: true, atom.Cite: true, atom.Code: true, atom.Col: true,
	atom.Colgroup: true, atom.Dd: true, atom.Del: true, atom.Div: true, atom.Dl: true,
	atom.Dt: true, atom.Em: true, atom.Font: true, atom.H1: true, atom.H2: true, atom.H3: true,
	atom.H4: true, atom.H5: true, atom.H6: true, atom.Hr: true, atom.I: true, atom.Img: true,
	atom.Ins: true, atom.Li: true, atom.Ol: true, atom.P: true, atom.Pre: true, atom.Q: true,
	atom.S: true, atom.Small: true, atom.Span: true, atom.Strike: true, atom.Strong: true,
	atom.Sub: true, atom.Sup: true, atom.Table: true, atom.Tbody: true, atom.Td: true,
	atom.Tfoot: true, atom.Th: true, atom.Thead: true, atom.Tr: true, atom.U: true, atom.Ul: true,
}

// attrs allowed on any allowed element, and extras per element.
var commonAttrs = map[string]bool{"title": true, "dir": true, "lang": true, "align": true}
var tagAttrs = map[atom.Atom]map[string]bool{
	atom.Td:    {"colspan": true, "rowspan": true, "valign": true, "width": true, "height": true, "bgcolor": true},
	atom.Th:    {"colspan": true, "rowspan": true, "valign": true, "width": true, "height": true, "bgcolor": true, "scope": true},
	atom.Tr:    {"valign": true, "bgcolor": true},
	atom.Table: {"border": true, "cellpadding": true, "cellspacing": true, "width": true, "bgcolor": true},
	atom.Col:   {"span": true, "width": true},
	atom.Font:  {"color": true, "size": true, "face": true},
	atom.Ol:    {"start": true, "type": true},
}

// safeStyle lists the CSS properties kept from style="" attributes.
var safeStyle = map[string]bool{
	"color": true, "background-color": true, "text-align": true, "vertical-align": true,
	"font-weight": true, "font-style": true, "font-size": true, "font-family": true,
	"text-decoration": true, "border": true, "border-top": true, "border-right": true,
	"border-bottom": true, "border-left": true, "border-collapse": true, "border-color": true,
	"border-style": true, "border-width": true, "padding": true, "padding-top": true,
	"padding-right": true, "padding-bottom": true, "padding-left": true, "margin": true,
	"margin-top": true, "margin-right": true, "margin-bottom": true, "margin-left": true,
	"width": true, "height": true, "white-space": true, "line-height": true,
}

var unsafeCSSValue = regexp.MustCompile(`(?i)url\s*\(|expression|@import|javascript:|behaviou?r|-moz-binding|[\\<>]`)

// Sanitize returns an HTML fragment (no html/head/body wrapper) rebuilt from
// src's body using only the allowlist above. Links keep http, https and mailto
// targets; images keep only https sources, never 1x1 pixels, and an image that
// cannot be kept leaves its alt text behind.
func Sanitize(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return ""
	}
	body := findBody(doc)
	if body == nil {
		return ""
	}
	out := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		clean(c, out)
	}
	var buf bytes.Buffer
	for c := out.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&buf, c)
	}
	return strings.TrimSpace(buf.String())
}

func findBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == atom.Body {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBody(c); b != nil {
			return b
		}
	}
	return nil
}

// clean copies n into parent according to the allowlist.
func clean(n, parent *html.Node) {
	switch n.Type {
	case html.TextNode:
		parent.AppendChild(&html.Node{Type: html.TextNode, Data: n.Data})
		return
	case html.ElementNode:
	default:
		return // comments, doctype, processing instructions
	}
	if dropped[n.DataAtom] {
		return
	}
	if n.DataAtom == atom.Img {
		if img := cleanImage(n); img != nil {
			parent.AppendChild(img)
		} else if alt := strings.TrimSpace(attr(n, "alt")); alt != "" {
			parent.AppendChild(&html.Node{Type: html.TextNode, Data: alt})
		}
		return
	}
	target := parent
	if allowed[n.DataAtom] {
		el := &html.Node{Type: html.ElementNode, DataAtom: n.DataAtom, Data: n.Data}
		for _, a := range n.Attr {
			if v, ok := keepAttr(n.DataAtom, a); ok {
				el.Attr = append(el.Attr, html.Attribute{Key: a.Key, Val: v})
			}
		}
		parent.AppendChild(el)
		target = el
		if n.DataAtom == atom.Br || n.DataAtom == atom.Hr || n.DataAtom == atom.Col {
			return
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		clean(c, target)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func keepAttr(tag atom.Atom, a html.Attribute) (string, bool) {
	key := strings.ToLower(a.Key)
	switch {
	case key == "href" && tag == atom.A:
		if u := safeURL(a.Val, "http", "https", "mailto"); u != "" {
			return u, true
		}
		return "", false
	case key == "style":
		if s := cleanStyle(a.Val); s != "" {
			return s, true
		}
		return "", false
	case commonAttrs[key] || tagAttrs[tag][key]:
		if strings.ContainsAny(a.Val, "<>\"") && key != "title" {
			return "", false
		}
		return a.Val, true
	}
	return "", false
}

// safeURL returns raw when its scheme is one of schemes, else "". Whitespace and
// control characters, which browsers ignore inside a scheme, are removed first.
func safeURL(raw string, schemes ...string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f {
			return -1
		}
		return r
	}, raw)
	lower := strings.ToLower(cleaned)
	for _, s := range schemes {
		if strings.HasPrefix(lower, s+":") {
			return cleaned
		}
	}
	return ""
}

func cleanStyle(style string) string {
	var kept []string
	for _, decl := range strings.Split(style, ";") {
		name, value, ok := strings.Cut(decl, ":")
		name, value = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(value)
		if !ok || value == "" || !safeStyle[name] || unsafeCSSValue.MatchString(value) {
			continue
		}
		kept = append(kept, name+": "+value)
	}
	return strings.Join(kept, "; ")
}

// cleanImage keeps an https image that is not a tracking pixel, or returns nil.
func cleanImage(n *html.Node) *html.Node {
	src := safeURL(attr(n, "src"), "https")
	if src == "" || isPixel(n) {
		return nil
	}
	img := &html.Node{Type: html.ElementNode, DataAtom: atom.Img, Data: "img", Attr: []html.Attribute{{Key: "src", Val: src}}}
	for _, key := range []string{"alt", "width", "height", "title"} {
		if v := attr(n, key); v != "" && !strings.ContainsAny(v, "<>") {
			img.Attr = append(img.Attr, html.Attribute{Key: key, Val: v})
		}
	}
	if s := cleanStyle(attr(n, "style")); s != "" {
		img.Attr = append(img.Attr, html.Attribute{Key: "style", Val: s})
	}
	return img
}

var onePx = regexp.MustCompile(`^\s*[01](px)?\s*$`)

// isPixel reports an image declared as at most 1 pixel in either direction, the
// shape of a tracking beacon.
func isPixel(n *html.Node) bool {
	if onePx.MatchString(attr(n, "width")) || onePx.MatchString(attr(n, "height")) {
		return true
	}
	style := strings.ToLower(attr(n, "style"))
	return regexp.MustCompile(`(width|height)\s*:\s*[01](px)?\s*(;|$)`).MatchString(style)
}
