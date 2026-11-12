package smtp

import (
	"strings"
	"sync/atomic"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Inline styles for tables in sent mail. Mail clients drop <style> blocks, and
// Gmail shows a bare <table> with no borders and almost no padding, so the
// styling has to sit on each element. #999 reads on light and dark backgrounds.
const (
	tableCSS     = "border-collapse:collapse"
	basicCellCSS = "border:1px solid #999;padding:4px 8px"
)

// tableStyle holds the CSS for the <table> and for each cell; both empty means
// tables are left as goldmark renders them.
type tableStyle struct{ table, cell string }

// currentTableStyle is set once from config (see SetTableStyle). It is global
// because outgoing mail is prepared from the TUI, the CLI and the CLI form,
// which would otherwise all need the setting threaded through Prepare.
var currentTableStyle atomic.Pointer[tableStyle]

// SetTableStyle selects the inline styling for Markdown tables from the
// [display] compose_table_style and compose_table_css settings. An unknown
// style, like "none", leaves tables unstyled. "custom" with no CSS behaves as
// "basic".
func SetTableStyle(style, css string) {
	norm, _ := config.NormalizeTableStyle(style)
	ts := &tableStyle{}
	switch norm {
	case config.TableStyleBasic:
		ts = &tableStyle{table: tableCSS, cell: basicCellCSS}
	case config.TableStyleCustom:
		ts = &tableStyle{table: tableCSS, cell: cleanCSS(css)}
		if ts.cell == "" {
			ts.cell = basicCellCSS
		}
	}
	currentTableStyle.Store(ts)
}

// cleanCSS collapses the declaration list onto one line; the renderer escapes
// quotes and angle brackets when it writes the style attribute.
func cleanCSS(css string) string {
	css = strings.Join(strings.Fields(css), " ")
	return strings.Trim(css, "; ")
}

// tableStyler writes the style attribute on table and cell nodes before
// rendering. goldmark's table renderer already appends text-align to a cell's
// existing style, so alignment and our styling merge without a custom renderer.
type tableStyler struct{ style tableStyle }

func (t tableStyler) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case east.KindTable:
			n.SetAttributeString("style", []byte(t.style.table))
		case east.KindTableCell:
			n.SetAttributeString("style", []byte(t.style.cell))
		}
		return ast.WalkContinue, nil
	})
}

// tableTransformer returns the styling transformer for the current setting, or
// nothing when tables are unstyled.
func tableTransformer() []util.PrioritizedValue {
	ts := currentTableStyle.Load()
	if ts == nil || (ts.table == "" && ts.cell == "") {
		return nil
	}
	return []util.PrioritizedValue{util.Prioritized(tableStyler{style: *ts}, 200)}
}
