package searchquery

import (
	"testing"
	"time"
)

// The field-operator cases upstream tested against its own db.ParseSearch
// (a24f5b1, #113), run against this parser. want is "field=value" per term, in
// order, "|" between clauses.
func TestFieldOperatorsAgreeWithUpstream(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	for query, want := range map[string]string{
		"to:work@example.com":         "to=work@example.com",
		"to:wor":                      "to=wor",
		"FROM:jo invoice":             "from=jo|any=invoice",
		`from:"Jo Smith" due`:         "from=Jo Smith|any=due",
		`subject:"hello wor`:          "subject=hello wor",
		"invoice to:":                 "any=invoice",
		`to:"`:                        "",
		"#billing cc:ann@example.com": "tag=billing|cc=ann@example.com",
		"re:hello":                    "any=re:hello",
		"from:a to:b x":               "from=a|to=b|any=x",
	} {
		var got string
		for i, clause := range Parse(query, now).Clauses {
			if i > 0 {
				got += "|"
			}
			for j, term := range clause {
				if j > 0 {
					got += " OR "
				}
				got += string(term.Field) + "=" + term.Value
			}
		}
		if got != want {
			t.Errorf("Parse(%q) = %q, want %q", query, got, want)
		}
	}
}
