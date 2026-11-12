package mailapp

import (
	"strings"
	"testing"

	"github.com/allisonhere/tidemail/internal/smtp"
)

// Table styling (feat/table-style) and inline images (feat/inline-images) both
// add AST transformers to the shared converter; they were built on separate
// branches, so check they work together, with and without the signature marker.
func TestTableStyleAndInlineImagesWorkTogether(t *testing.T) {
	smtp.SetTableStyle("basic", "")
	t.Cleanup(func() { smtp.SetTableStyle("", "") })
	logo := testPNG(t, 120, 40)
	body := "| Name | Picture |\n|:--|:-:|\n| Logo | ![logo](logo.png) |\n\nBelow: ![logo again](logo.png){width=60}\n"
	for _, tc := range []struct{ name, body string }{{"no marker", body}, {"marker", body + "\n⁂\n"}} {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := PrepareCompose(imgAccount, imgDraft(tc.body, att("logo.png", logo)))
			if err != nil {
				t.Fatal(err)
			}
			h := msg.HTMLBody
			for _, want := range []string{
				`<table style="border-collapse:collapse">`,
				`<th style="border:1px solid #999;padding:4px 8px;text-align:left">Name</th>`,
				`<td style="border:1px solid #999;padding:4px 8px;text-align:center"><img src="cid:`,
				`width="120" height="40"`, `width="60" height="20"`,
			} {
				if !strings.Contains(h, want) {
					t.Fatalf("missing %q:\n%s", want, h)
				}
			}
			// Shown at 120 px and at 60 px, but both embed the same 120 px pixels
			// (2x the smaller size equals the natural width): one part.
			if len(msg.InlineImages) != 1 {
				t.Fatalf("inline images: %d, want 1", len(msg.InlineImages))
			}
		})
	}
}
