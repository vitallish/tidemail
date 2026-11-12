package quotehtml

import (
	"fmt"
	"html"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// FromText turns a plain-text message into an HTML fragment: paragraphs for
// blank-line separated blocks, <br> for single line breaks, everything escaped.
func FromText(text string) string {
	text = strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n")
	if text == "" {
		return ""
	}
	var paras []string
	for _, block := range strings.Split(text, "\n\n") {
		block = strings.Trim(block, "\n")
		if strings.TrimSpace(block) == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		for i := range lines {
			lines[i] = html.EscapeString(strings.TrimRight(lines[i], " \t"))
		}
		paras = append(paras, "<p>"+strings.Join(lines, "<br>")+"</p>")
	}
	return strings.Join(paras, "")
}

// PlainText renders a sanitized fragment as readable text: blocks on their own
// lines, list items bulleted, table cells separated by " | ", links followed by
// their address when it differs from the text.
func PlainText(fragment string) string {
	root, err := xhtml.ParseFragment(strings.NewReader(fragment), &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Div, Data: "div"})
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, n := range root {
		writeText(&b, n)
	}
	lines := strings.Split(b.String(), "\n")
	var out []string
	blank := true
	for _, ln := range lines {
		ln = strings.TrimRight(strings.Join(strings.Fields(ln), " "), " ")
		if ln == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, ln)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func writeText(b *strings.Builder, n *xhtml.Node) {
	switch n.Type {
	case xhtml.TextNode:
		b.WriteString(strings.ReplaceAll(n.Data, "\n", " "))
		return
	case xhtml.ElementNode:
	default:
		return
	}
	switch n.DataAtom {
	case atom.Br:
		b.WriteString("\n")
		return
	case atom.Hr:
		b.WriteString("\n----\n")
		return
	case atom.Img:
		if alt := attr(n, "alt"); alt != "" {
			b.WriteString(alt)
		}
		return
	case atom.Li:
		b.WriteString("\n- ")
	case atom.Td, atom.Th:
		if prev := n.PrevSibling; prev != nil {
			b.WriteString(" | ")
		}
	case atom.P, atom.Div, atom.Tr, atom.Table, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Blockquote, atom.Pre, atom.Ul, atom.Ol:
		b.WriteString("\n")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writeText(b, c)
	}
	switch n.DataAtom {
	case atom.A:
		if href := attr(n, "href"); href != "" && !strings.HasPrefix(href, "mailto:") {
			if text := strings.TrimSpace(textOf(n)); text != href {
				b.WriteString(" <" + href + ">")
			}
		}
	case atom.P, atom.Div, atom.Tr, atom.Table, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Blockquote, atom.Pre, atom.Ul, atom.Ol:
		b.WriteString("\n")
	}
}

func textOf(n *xhtml.Node) string {
	if n.Type == xhtml.TextNode {
		return n.Data
	}
	var s string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		s += textOf(c)
	}
	return s
}

// Markdown converts a sanitized fragment to Markdown for the composer's "expand
// the quote" command: emphasis, links, lists, headings, code, block quotes and
// tables (as GitHub pipe tables). Only the tags Sanitize allows can occur.
func Markdown(fragment string) (string, error) {
	nodes, err := xhtml.ParseFragment(strings.NewReader(fragment), &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Div, Data: "div"})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, n := range nodes {
		mdNode(&b, n, 0)
	}
	var out []string
	blank := true
	for _, ln := range strings.Split(b.String(), "\n") {
		ln = strings.TrimRight(ln, " \t")
		if ln == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n")), nil
}

func mdChildren(b *strings.Builder, n *xhtml.Node, depth int) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		mdNode(b, c, depth)
	}
}

func mdInline(n *xhtml.Node) string {
	var b strings.Builder
	mdChildren(&b, n, 0)
	return strings.Join(strings.Fields(b.String()), " ")
}

func mdNode(b *strings.Builder, n *xhtml.Node, depth int) {
	if n.Type == xhtml.TextNode {
		b.WriteString(strings.ReplaceAll(n.Data, "\n", " "))
		return
	}
	if n.Type != xhtml.ElementNode {
		return
	}
	switch n.DataAtom {
	case atom.Br:
		b.WriteString("  \n")
	case atom.Hr:
		b.WriteString("\n\n---\n\n")
	case atom.Img:
		if alt := attr(n, "alt"); alt != "" {
			b.WriteString(alt)
		}
	case atom.B, atom.Strong:
		wrapInline(b, n, "**")
	case atom.I, atom.Em, atom.Cite:
		wrapInline(b, n, "*")
	case atom.Del, atom.S, atom.Strike:
		wrapInline(b, n, "~~")
	case atom.Code:
		wrapInline(b, n, "`")
	case atom.A:
		text := mdInline(n)
		href := attr(n, "href")
		switch {
		case text == "":
		case href == "" || href == text:
			b.WriteString(text)
		default:
			b.WriteString("[" + text + "](" + href + ")")
		}
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(n.Data[1] - '0')
		b.WriteString("\n\n" + strings.Repeat("#", level) + " " + mdInline(n) + "\n\n")
	case atom.Pre:
		b.WriteString("\n\n```\n" + strings.Trim(textOf(n), "\n") + "\n```\n\n")
	case atom.Blockquote:
		var inner strings.Builder
		mdChildren(&inner, n, depth)
		text := strings.TrimSpace(inner.String())
		var lines []string
		for _, ln := range strings.Split(text, "\n") {
			lines = append(lines, strings.TrimRight("> "+ln, " "))
		}
		b.WriteString("\n\n" + strings.Join(lines, "\n") + "\n\n")
	case atom.Ul, atom.Ol:
		b.WriteString("\n")
		i := 1
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != xhtml.ElementNode || c.DataAtom != atom.Li {
				continue
			}
			marker := "- "
			if n.DataAtom == atom.Ol {
				marker = fmt.Sprintf("%d. ", i)
				i++
			}
			var item strings.Builder
			mdChildren(&item, c, depth+1)
			text := strings.TrimSpace(item.String())
			text = strings.ReplaceAll(text, "\n", "\n"+strings.Repeat(" ", len(marker)))
			b.WriteString(strings.Repeat("  ", depth) + marker + text + "\n")
		}
		b.WriteString("\n")
	case atom.Table:
		mdTable(b, n)
	case atom.P, atom.Div, atom.Center, atom.Dl, atom.Dt, atom.Dd:
		b.WriteString("\n\n")
		mdChildren(b, n, depth)
		b.WriteString("\n\n")
	default:
		mdChildren(b, n, depth)
	}
}

func wrapInline(b *strings.Builder, n *xhtml.Node, mark string) {
	if text := mdInline(n); text != "" {
		b.WriteString(mark + text + mark)
	}
}

// mdTable writes a pipe table. The first row is the header (a table without
// <th> still needs one in Markdown); cells are flattened to a line each.
func mdTable(b *strings.Builder, table *xhtml.Node) {
	var rows [][]string
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != xhtml.ElementNode {
				continue
			}
			switch c.DataAtom {
			case atom.Tr:
				var cells []string
				for d := c.FirstChild; d != nil; d = d.NextSibling {
					if d.Type == xhtml.ElementNode && (d.DataAtom == atom.Td || d.DataAtom == atom.Th) {
						cell := strings.ReplaceAll(mdInline(d), "|", "\\|")
						cells = append(cells, cell)
					}
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
			case atom.Thead, atom.Tbody, atom.Tfoot:
				walk(c)
			}
		}
	}
	walk(table)
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}
	line := func(cells []string) string {
		for len(cells) < width {
			cells = append(cells, "")
		}
		return "| " + strings.Join(cells, " | ") + " |\n"
	}
	b.WriteString("\n\n" + line(rows[0]))
	b.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
	for _, r := range rows[1:] {
		b.WriteString(line(r))
	}
	b.WriteString("\n")
}
