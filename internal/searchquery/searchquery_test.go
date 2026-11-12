package searchquery

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 12, 15, 30, 0, 0, time.UTC)

// shape renders a query compactly: clauses joined by " & ", terms by " | ".
func shape(q Query) string {
	var clauses []string
	for _, c := range q.Clauses {
		var terms []string
		for _, t := range c {
			s := string(t.Field) + "=" + t.Value
			switch t.Field {
			case Date:
				s = "date[" + stamp(t.Start) + "," + stamp(t.End) + ")"
			case HasAtt, Unread, Read, Starred:
				s = string(t.Field)
			}
			if t.Quoted {
				s += "(phrase)"
			}
			if t.Negate {
				s = "-" + s
			}
			terms = append(terms, s)
		}
		clauses = append(clauses, strings.Join(terms, " | "))
	}
	return strings.Join(clauses, " & ")
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func TestParse(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"invoice", "any=invoice"},
		{"invoice due", "any=invoice & any=due"},
		{`"quarterly review"`, "any=quarterly review(phrase)"},
		{"subject:invoice", "subject=invoice"},
		{"SUBJECT:Invoice", "subject=Invoice"},
		{`subject:"two words"`, "subject=two words(phrase)"},
		{"from:ann@example.com to:bob", "from=ann@example.com & to=bob"},
		{"cc:x body:y", "cc=x & body=y"},
		{"from:ann OR from:bob invoice", "from=ann | from=bob & any=invoice"},
		{"a OR b OR c", "any=a | any=b | any=c"},
		{"OR a", "any=a"},
		{"a OR", "any=a"},
		{"-spam", "-any=spam"},
		{"-from:ann", "-from=ann"},
		{`-"big sale"`, "-any=big sale(phrase)"},
		{"-", "any=-"},
		{"or", "any=or"}, // OR is an operator only in capitals
		{"#github", "tag=github"},
		{`#"To Do"`, "tag=to do"},
		{"-#spam", "-tag=spam"},
		{"# #!", ""},
		{"related:123", "related=123"},
		{"related:cache:42", "related=42"},
		{"RELATED:Cache:7 from:ann", "related=7 & from=ann"},
		{"-related:5", "-related=5"},
		{"related:", ""},
		{"has:attachment", "attachment"},
		{"has:attachments", "attachment"},
		{"-has:attachment", "-attachment"},
		{"is:unread is:read is:starred is:flagged", "unread & read & starred & starred"},
		{"after:2026-01-31", "date[2026-01-31,)"},
		{"before:2026-02-15", "date[,2026-02-15)"},
		{"on:2026-02-01", "date[2026-02-01,2026-02-02)"},
		{"after:7d", "date[2026-11-05,)"},
		{"after:2w", "date[2026-10-29,)"},
		{"after:3m", "date[2026-08-12,)"},
		{"before:1y", "date[,2025-11-12)"},
		{"after:0d", "date[2026-11-12,)"},
		{"re: hello", "any=re: & any=hello"},
		{"http://example.com", "any=http://example.com"},
		{"unknown:thing", "any=unknown:thing"},
		// Mid-typing: nothing is dropped loudly.
		{"from:", ""},
		{"from: ann", "any=ann"},
		{"after:", ""},
		{"is:", ""},
	}
	for _, c := range cases {
		if got := shape(Parse(c.in, now)); got != c.want {
			t.Errorf("Parse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseReportsUnusableValues(t *testing.T) {
	cases := map[string]string{
		"after:2026-0":   "after:2026-0",
		"before:soon":    "before:soon",
		"on:2026-13-01":  "on:2026-13-01",
		"after:-3d":      "after:-3d",
		"after:3x":       "after:3x",
		"has:picture":    "has:picture",
		"is:important":   "is:important",
		"related:abc":    "related:abc",
		"related:0":      "related:0",
		"related:-3":     "related:-3",
		"related:cache:": "related:cache:",
		"is:unread junk": "",
	}
	for in, want := range cases {
		q := Parse(in, now)
		if want == "" {
			if len(q.Problems) != 0 {
				t.Errorf("Parse(%q) problems = %v, want none", in, q.Problems)
			}
			continue
		}
		if len(q.Problems) != 1 || !strings.HasPrefix(q.Problems[0], want) {
			t.Errorf("Parse(%q) problems = %v, want one starting %q", in, q.Problems, want)
		}
		// A bad value is dropped; the rest of the query still applies.
		if got := Parse(in+" word", now); shape(got) != "any=word" {
			t.Errorf("Parse(%q) = %q, want the bad term dropped", in+" word", shape(got))
		}
	}
}

func TestParseQuotes(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"unterminated phrase`, "any=unterminated phrase(phrase)"}, // while still typing
		{`say "a \"quoted\" word"`, `any=say & any=a "quoted" word(phrase)`},
		{`subject:"unterminated`, "subject=unterminated(phrase)"},
		{`""`, ""},
	}
	for _, c := range cases {
		if got := shape(Parse(c.in, now)); got != c.want {
			t.Errorf("Parse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseUsesTheLocationOfNow(t *testing.T) {
	loc := time.FixedZone("test", -8*3600)
	q := Parse("on:2026-02-01", time.Date(2026, 11, 12, 3, 0, 0, 0, loc))
	got := q.Clauses[0][0]
	want := time.Date(2026, 2, 1, 0, 0, 0, 0, loc)
	if !got.Start.Equal(want) || !got.End.Equal(want.AddDate(0, 0, 1)) {
		t.Errorf("on: = [%v, %v), want a day starting %v", got.Start, got.End, want)
	}
}

func TestEmptyAndTerms(t *testing.T) {
	if !Parse("", now).Empty() || !Parse("from:", now).Empty() {
		t.Error("a query with no usable terms must be empty")
	}
	q := Parse("a OR b c", now)
	if q.Empty() || len(q.Terms()) != 3 {
		t.Errorf("Terms() = %v", q.Terms())
	}
	if !reflect.DeepEqual(q.Clauses[1], q.Terms()[2:]) {
		t.Errorf("clauses and terms disagree")
	}
}
