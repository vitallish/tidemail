// Package searchquery parses what the user types in the search box into a
// structure both backends compile: the local database (internal/db) and, for
// server search, IMAP SEARCH criteria.
//
// A query is a list of clauses that must all match (implicit AND). A clause is
// one or more terms joined by OR, so "from:ann OR from:bob invoice" means
// (from ann or from bob) and mentions invoice. There are no parentheses.
//
//	word  "a phrase"        anywhere: subject, sender, recipients, body
//	subject:x from:x to:x cc:x body:x
//	after:2026-01-31 before:2026-02-15 on:2026-02-01   (also 7d, 2w, 3m, 1y)
//	has:attachment   is:unread  is:read  is:starred
//	#tag                    a tag shown on message rows (local only)
//	-term                   exclude
//
// Typing is not an error: a half-typed value ("after:2026-0") is dropped from
// the query and listed in Problems, never fatal, because search runs on every
// keystroke.
package searchquery

import (
	"fmt"
	"strings"
	"time"
)

// Field says what a term is matched against.
type Field string

const (
	Any     Field = "any" // subject, from, to, cc and body
	Subject Field = "subject"
	From    Field = "from"
	To      Field = "to"
	CC      Field = "cc"
	Body    Field = "body"
	Date    Field = "date" // Start inclusive, End exclusive
	HasAtt  Field = "attachment"
	Unread  Field = "unread"
	Read    Field = "read"
	Starred Field = "starred"
	Tag     Field = "tag"
)

// IsText reports whether the field is matched against message text.
func (f Field) IsText() bool {
	switch f {
	case Any, Subject, From, To, CC, Body:
		return true
	}
	return false
}

// Term is one condition. Value is the text or tag; Start and End bound a Date
// term. Quoted marks a phrase, which is not matched as a prefix while typing.
type Term struct {
	Field  Field
	Value  string
	Quoted bool
	Negate bool
	Start  time.Time
	End    time.Time
}

// Query is clauses ANDed together; each clause is terms ORed together.
type Query struct {
	Clauses  [][]Term
	Problems []string // terms that were dropped, and why
}

// Empty reports whether the query matches nothing in particular (no usable
// terms), which callers treat as "no search".
func (q Query) Empty() bool { return len(q.Clauses) == 0 }

// Terms returns every term in order, for callers that need to look at them all.
func (q Query) Terms() []Term {
	var out []Term
	for _, c := range q.Clauses {
		out = append(out, c...)
	}
	return out
}

// Parse reads a search. now anchors relative dates ("7d").
func Parse(input string, now time.Time) Query {
	var q Query
	var clause []Term
	orPending := false // the previous token was OR, so the next term joins clause
	flush := func() {
		if len(clause) > 0 {
			q.Clauses = append(q.Clauses, clause)
			clause = nil
		}
	}
	for _, tok := range tokenize(input) {
		if !tok.quoted && tok.text == "OR" {
			// A leading or dangling OR (mid-typing) is ignored.
			orPending = len(clause) > 0
			continue
		}
		term, ok, problem := interpret(tok, now)
		if problem != "" {
			q.Problems = append(q.Problems, problem)
		}
		if !ok {
			continue
		}
		if orPending {
			clause = append(clause, term)
		} else {
			flush()
			clause = []Term{term}
		}
		orPending = false
	}
	flush()
	return q
}

// token is one whitespace-separated piece; a quoted span may contain spaces.
type token struct {
	text   string // as typed, quotes removed from a purely quoted token
	raw    string // as typed, quotes intact
	quoted bool   // a double-quoted span was present
}

// tokenize splits on spaces, keeping "quoted spans" (also after a prefix such
// as subject:"two words" or #"To Do") whole. An unterminated quote runs to the
// end, so a phrase works while it is still being typed.
func tokenize(s string) []token {
	var toks []token
	var raw, text strings.Builder
	quoted, inQuote, has := false, false, false
	emit := func() {
		if has {
			toks = append(toks, token{text: text.String(), raw: raw.String(), quoted: quoted})
		}
		raw.Reset()
		text.Reset()
		quoted, inQuote, has = false, false, false
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case inQuote && r == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\'):
			raw.WriteRune(r)
			i++
			raw.WriteRune(rs[i])
			text.WriteRune(rs[i])
		case r == '"':
			quoted, has = true, true
			inQuote = !inQuote
			raw.WriteRune(r)
		case !inQuote && (r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			emit()
		default:
			has = true
			raw.WriteRune(r)
			text.WriteRune(r)
		}
	}
	emit()
	return toks
}

var fieldNames = map[string]Field{
	"subject": Subject, "from": From, "to": To, "cc": CC, "body": Body,
}

// interpret turns a token into a term. ok is false when the token yields
// nothing (an empty value); problem is set when something was dropped that the
// user probably meant, such as an unreadable date.
func interpret(tok token, now time.Time) (term Term, ok bool, problem string) {
	text, raw := tok.text, tok.raw
	negate := false
	if len(raw) > 1 && raw[0] == '-' {
		negate = true
		text, raw = strings.TrimPrefix(text, "-"), raw[1:]
	}
	term.Negate = negate

	if strings.HasPrefix(raw, "#") {
		tag := tagName(strings.TrimLeft(raw, "#"))
		if tag == "" {
			return term, false, ""
		}
		term.Field, term.Value = Tag, tag
		return term, true, ""
	}

	if name, value, found := strings.Cut(text, ":"); found && value != "" {
		lower := strings.ToLower(name)
		if f, known := fieldNames[lower]; known {
			// The value is what follows the colon in the typed text, quotes
			// already removed by the tokenizer.
			term.Field, term.Value, term.Quoted = f, value, tok.quoted
			return term, true, ""
		}
		switch lower {
		case "after", "before", "on":
			start, end, err := dateRange(lower, value, now)
			if err != nil {
				return term, false, fmt.Sprintf("%s:%s: %v", lower, value, err)
			}
			term.Field, term.Start, term.End = Date, start, end
			return term, true, ""
		case "has":
			if v := strings.ToLower(value); v == "attachment" || v == "attachments" {
				term.Field = HasAtt
				return term, true, ""
			}
			return term, false, fmt.Sprintf("has:%s: only has:attachment is supported", value)
		case "is":
			switch strings.ToLower(value) {
			case "unread":
				term.Field = Unread
			case "read":
				term.Field = Read
			case "starred", "flagged":
				term.Field = Starred
			default:
				return term, false, fmt.Sprintf("is:%s: use is:unread, is:read or is:starred", value)
			}
			return term, true, ""
		}
	} else if found && value == "" {
		// "from:" with nothing after it yet.
		if _, known := fieldNames[strings.ToLower(name)]; known {
			return term, false, ""
		}
		switch strings.ToLower(name) {
		case "after", "before", "on", "has", "is":
			return term, false, ""
		}
	}

	if text == "" {
		return term, false, ""
	}
	term.Field, term.Value, term.Quoted = Any, text, tok.quoted
	return term, true, ""
}

// tagName normalises what follows '#': a quoted name keeps its characters
// (lowercased); an unquoted one keeps only letters, digits and _-.$+ .
func tagName(raw string) string {
	if strings.HasPrefix(raw, `"`) {
		name := strings.TrimSuffix(strings.TrimPrefix(raw, `"`), `"`)
		return strings.ToLower(strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(name))
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-', r == '.', r == '$', r == '+':
			return r
		}
		return -1
	}, strings.ToLower(raw))
}

// dateRange returns [start, end) for after:/before:/on: in now's location.
// after: includes the day given, before: excludes it, on: is that one day. A
// relative value (7d, 2w, 3m, 1y) counts back from now's date.
func dateRange(op, value string, now time.Time) (start, end time.Time, err error) {
	day, err := parseDay(value, now)
	if err != nil {
		return start, end, err
	}
	next := day.AddDate(0, 0, 1)
	switch op {
	case "after":
		return day, time.Time{}, nil
	case "before":
		return time.Time{}, day, nil
	default:
		return day, next, nil
	}
}

func parseDay(value string, now time.Time) (time.Time, error) {
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if t, err := time.ParseInLocation("2006-01-02", value, loc); err == nil {
		return t, nil
	}
	if len(value) >= 2 {
		unit := value[len(value)-1]
		var n int
		if _, err := fmt.Sscanf(value[:len(value)-1], "%d", &n); err == nil && n >= 0 && fmt.Sprint(n) == value[:len(value)-1] {
			switch unit {
			case 'd':
				return today.AddDate(0, 0, -n), nil
			case 'w':
				return today.AddDate(0, 0, -7*n), nil
			case 'm':
				return today.AddDate(0, -n, 0), nil
			case 'y':
				return today.AddDate(-n, 0, 0), nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("use YYYY-MM-DD or a count like 7d, 2w, 3m, 1y")
}
