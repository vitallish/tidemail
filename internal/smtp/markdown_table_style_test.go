package smtp

import (
	"strings"
	"testing"
)

const styleTestTable = "| Arm | N |\n|:---|---:|\n| A | 12 |\n"

func withTableStyle(t *testing.T, style, css string) {
	t.Helper()
	SetTableStyle(style, css)
	t.Cleanup(func() { SetTableStyle("", "") })
}

func TestTableStyleNoneLeavesOutputUnchanged(t *testing.T) {
	for _, style := range []string{"", "none", "NONE", "bogus"} {
		withTableStyle(t, style, "border:9px solid red")
		got := MarkdownToHTML(styleTestTable)
		want := "<table>\n<thead>\n<tr>\n<th style=\"text-align:left\">Arm</th>\n<th style=\"text-align:right\">N</th>\n</tr>\n</thead>\n<tbody>\n<tr>\n<td style=\"text-align:left\">A</td>\n<td style=\"text-align:right\">12</td>\n</tr>\n</tbody>\n</table>\n"
		if got != want {
			t.Fatalf("style %q changed output:\n%s", style, got)
		}
	}
}

func TestTableStyleBasicStylesTableAndEveryCell(t *testing.T) {
	withTableStyle(t, "basic", "")
	got := MarkdownToHTML(styleTestTable)
	for _, want := range []string{
		`<table style="border-collapse:collapse">`,
		`<th style="border:1px solid #999;padding:4px 8px;text-align:left">Arm</th>`,
		`<th style="border:1px solid #999;padding:4px 8px;text-align:right">N</th>`,
		`<td style="border:1px solid #999;padding:4px 8px;text-align:right">12</td>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestTableStyleCellWithoutAlignmentIsStillStyled(t *testing.T) {
	withTableStyle(t, "basic", "")
	got := MarkdownToHTML("| A | B |\n|---|---|\n| 1 | 2 |\n")
	if !strings.Contains(got, `<td style="border:1px solid #999;padding:4px 8px">1</td>`) {
		t.Fatalf("unaligned cell not styled:\n%s", got)
	}
}

func TestTableStyleCustomUsesGivenCSS(t *testing.T) {
	withTableStyle(t, "custom", " border: 1px solid #444;\n padding:6px 10px; ")
	got := MarkdownToHTML(styleTestTable)
	if !strings.Contains(got, `<td style="border: 1px solid #444; padding:6px 10px;text-align:right">12</td>`) {
		t.Fatalf("custom CSS not applied:\n%s", got)
	}
	if !strings.Contains(got, `<table style="border-collapse:collapse">`) {
		t.Fatalf("custom must still collapse borders:\n%s", got)
	}
}

func TestTableStyleCustomWithoutCSSFallsBackToBasic(t *testing.T) {
	withTableStyle(t, "custom", "  ")
	if got := MarkdownToHTML(styleTestTable); !strings.Contains(got, "border:1px solid #999") {
		t.Fatalf("empty custom CSS should behave as basic:\n%s", got)
	}
}

func TestTableStyleCSSCannotBreakOutOfTheAttribute(t *testing.T) {
	withTableStyle(t, "custom", `border:1px solid red" onclick="x"><script>alert(1)</script>`)
	got := MarkdownToHTML(styleTestTable)
	if strings.Contains(got, `onclick="x"`) || strings.Contains(got, "<script>") {
		t.Fatalf("CSS escaped the style attribute:\n%s", got)
	}
}

func TestTableStyleLeavesOtherMarkdownAlone(t *testing.T) {
	withTableStyle(t, "basic", "")
	if got, want := MarkdownToHTML("**bold** text\n"), "<p><strong>bold</strong> text</p>\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
