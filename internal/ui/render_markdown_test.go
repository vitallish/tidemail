package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The styled theme starts from an empty glamour style, so a blank block_prefix
// on item/enumeration drops the bullet and the "N. " marker.
func TestRenderMarkdownKeepsListMarkers(t *testing.T) {
	src := "- First item\n- Second item\n\n1. One\n2. Two"
	for _, plain := range []bool{false, true} {
		got := ansi.Strip(renderMarkdown(src, 60, CatppuccinMocha, plain))
		for _, want := range []string{"• First item", "• Second item", "1. One", "2. Two"} {
			if !strings.Contains(got, want) {
				t.Errorf("plain=%v: missing %q in %q", plain, want, got)
			}
		}
	}
}
