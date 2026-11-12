package quotehtml

import (
	"strings"
	"testing"
)

func TestSanitizeKeepsStructureThatMattersInAQuote(t *testing.T) {
	src := `<html><head><title>x</title><style>p{color:red}</style></head><body>
<p>Hello <b>there</b> and <a href="https://example.com/a?b=1">a link</a></p>
<table border="1" cellpadding="4"><thead><tr><th>Item</th><th>Qty</th></tr></thead>
<tbody><tr><td style="text-align:right;color:#333">Pens</td><td>3</td></tr></tbody></table>
<ul><li>one</li><li>two</li></ul></body></html>`
	got := Sanitize(src)
	for _, want := range []string{"<b>there</b>", `<a href="https://example.com/a?b=1">`, "<table", `border="1"`, "<th>Item</th>", "<td", "text-align: right", "color: #333", "<li>two</li>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	for _, bad := range []string{"<html", "<head", "<style", "<title", "color:red"} {
		if strings.Contains(got, bad) {
			t.Errorf("kept %q in %s", bad, got)
		}
	}
}

func TestSanitizeRemovesAnythingActive(t *testing.T) {
	cases := map[string]string{
		`<p onclick="evil()">x</p>`:                                               `onclick`,
		`<script>alert(1)</script><p>x</p>`:                                       `alert`,
		`<iframe src="https://e.example"></iframe>x`:                              `iframe`,
		`<a href="javascript:alert(1)">x</a>`:                                     `javascript`,
		`<a href=" JaVa&#x09;Script:alert(1)">x</a>`:                              `script:`,
		`<a href="data:text/html,<b>">x</a>`:                                      `data:`,
		`<p style="background:url(https://t.example/x)">x</p>`:                    `url(`,
		`<p style="color:red;expression(alert(1))">x</p>`:                         `expression`,
		`<form action="https://e.example"><input name=a>x</form>`:                 `input`,
		`<svg onload="x()"><circle/></svg>x`:                                      `svg`,
		`<p class="c" id="i" data-x="1" name="n">x</p>`:                           `class=`,
		`<a href="https://e.example" onmouseover="x()" target="_top">x</a>`:       `onmouseover`,
		`<meta http-equiv="refresh" content="0;url=https://e.example">x`:          `refresh`,
		`<object data="x"></object><embed src="y">x`:                              `embed`,
		`<p>x</p><script>stealCookies()</script>`:                                 `stealCookies`,
		`<p>x<style>.a{background:red}</style></p>`:                               `background`,
		`<p>x</p><textarea>secretDraft</textarea>`:                                `secretDraft`,
		`<p>x</p><noscript>trackingFallback</noscript>`:                           `trackingFallback`,
		`<p>x</p><svg><text>svgPayload</text></svg>`:                              `svgPayload`,
		`<p>x</p><button>clickBait</button><select><option>opt</option></select>`: `clickBait`,
	}
	for src, bad := range cases {
		if got := Sanitize(src); strings.Contains(strings.ToLower(got), strings.ToLower(bad)) {
			t.Errorf("Sanitize(%q) = %q still contains %q", src, got, bad)
		}
	}
	// What is left of a hostile link is still readable text.
	if got := Sanitize(`<a href="javascript:alert(1)">click me</a>`); !strings.Contains(got, "click me") || strings.Contains(got, "href") {
		t.Errorf("hostile link = %q", got)
	}
}

func TestSanitizeImagePolicy(t *testing.T) {
	cases := []struct{ src, want, bad string }{
		{`<img src="https://cdn.example/logo.png" alt="Logo" width="120">`, `src="https://cdn.example/logo.png"`, ""},
		{`<img src="http://cdn.example/logo.png" alt="Logo">`, "Logo", "<img"},   // not https: alt text only
		{`<img src="cid:abc@x" alt="Inline photo">`, "Inline photo", "<img"},     // inline attachment: alt text only
		{`<img src="data:image/png;base64,AAAA" alt="D">`, "D", "<img"},          // embedded data
		{`<img src="https://t.example/p.gif" width="1" height="1">`, "", "<img"}, // tracking pixel
		{`<img src="https://t.example/p.gif" style="width:1px;height:1px">`, "", "<img"},
		{`<img src="https://t.example/p.gif" height="0">`, "", "<img"},
		{`<img src="javascript:x" alt="J">`, "J", "<img"},
	}
	for _, c := range cases {
		got := Sanitize("<p>" + c.src + "</p>")
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s -> %q, want %q", c.src, got, c.want)
		}
		if c.bad != "" && strings.Contains(got, c.bad) {
			t.Errorf("%s -> %q, must not contain %q", c.src, got, c.bad)
		}
	}
}

func TestSanitizeEmptyAndGarbage(t *testing.T) {
	for _, src := range []string{"", "   ", "<<<>>>", "<p", "plain text only"} {
		_ = Sanitize(src) // must not panic
	}
	if got := Sanitize("plain text only"); got != "plain text only" {
		t.Errorf("plain text = %q", got)
	}
}

func TestFromTextEscapesAndKeepsParagraphs(t *testing.T) {
	got := FromText("Hi <b>Ann</b> & co\nsecond line\n\nNext paragraph")
	want := "<p>Hi &lt;b&gt;Ann&lt;/b&gt; &amp; co<br>second line</p><p>Next paragraph</p>"
	if got != want {
		t.Errorf("FromText = %q, want %q", got, want)
	}
	if FromText("  \n ") != "" {
		t.Error("blank text must give no fragment")
	}
}

func TestPlainTextOfATable(t *testing.T) {
	frag := Sanitize(`<p>Totals</p><table><tr><th>Item</th><th>Qty</th></tr><tr><td>Pens</td><td>3</td></tr></table><ul><li>a</li><li>b</li></ul><p>See <a href="https://e.example/x">the site</a>.</p>`)
	got := PlainText(frag)
	for _, want := range []string{"Totals", "Item | Qty", "Pens | 3", "- a", "- b", "the site <https://e.example/x>."} {
		if !strings.Contains(got, want) {
			t.Errorf("PlainText missing %q in:\n%s", want, got)
		}
	}
}

func TestMarkdownOfATableAndInlineFormatting(t *testing.T) {
	frag := Sanitize(`<p>Hello <b>big</b> <i>world</i>, <a href="https://e.example">link</a></p><table><tr><th>Item</th><th>Qty</th></tr><tr><td>Pens | pencils</td><td>3</td></tr></table><ol><li>x</li><li>y</li></ol><blockquote><p>older</p></blockquote>`)
	got, err := Markdown(frag)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Hello **big** *world*, [link](https://e.example)", "| Item | Qty |", "| --- | --- |", `| Pens \| pencils | 3 |`, "1. x", "2. y", "> older"} {
		if !strings.Contains(got, want) {
			t.Errorf("Markdown missing %q in:\n%s", want, got)
		}
	}
}
