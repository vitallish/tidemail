package db

import (
	"strings"

	"github.com/allisonhere/tidemail/internal/searchquery"
)

// compiledSearch is a parsed search as SQL over the messages table.
//
// Plain AND-ed text terms are folded into one full-text MATCH, as before, so
// results stay ranked by relevance and a query without operators behaves as it
// always has. Everything else (OR groups, exclusions, dates, flags, tags)
// becomes a WHERE condition.
type compiledSearch struct {
	match string   // FTS5 expression for the joined messages_fts, or ""
	where []string // conditions ANDed together
	args  []any    // arguments for where
}

func (c compiledSearch) empty() bool { return c.match == "" && len(c.where) == 0 }

// ftsColumns maps a text field to the messages_fts columns it searches.
var ftsColumns = map[searchquery.Field]string{
	searchquery.Subject: "subject",
	searchquery.From:    "from_addr",
	searchquery.To:      "to_addr",
	searchquery.CC:      "cc_addr",
	searchquery.Body:    "body_text",
}

func compileSearch(q searchquery.Query) compiledSearch {
	var c compiledSearch
	// Only the word being typed gets the prefix operator (see ftsExpr).
	last := lastPrefixable(q)
	var match []string
	for ci, clause := range q.Clauses {
		if len(clause) == 1 && clause[0].Field.IsText() && !clause[0].Negate {
			match = append(match, ftsExpr(clause[0], last == [2]int{ci, 0}))
			continue
		}
		var parts []string
		for ti, term := range clause {
			expr, args := termSQL(term, last == [2]int{ci, ti})
			if expr == "" {
				continue
			}
			parts = append(parts, expr)
			c.args = append(c.args, args...)
		}
		if len(parts) > 0 {
			c.where = append(c.where, "("+strings.Join(parts, " OR ")+")")
		}
	}
	c.match = strings.Join(match, " AND ")
	return c
}

// lastPrefixable finds the last positive, unquoted text term: the one the user
// is still typing. It returns {-1,-1} when there is none.
func lastPrefixable(q searchquery.Query) [2]int {
	at := [2]int{-1, -1}
	for ci, clause := range q.Clauses {
		for ti, term := range clause {
			if term.Field.IsText() && !term.Negate && !term.Quoted {
				at = [2]int{ci, ti}
			}
		}
	}
	return at
}

// ftsExpr quotes a text term for FTS5 (so punctuation in an address cannot be
// read as an operator) and limits it to its column. prefix makes it match as a
// prefix: search runs on every keystroke, and without it a half-typed word
// matches nothing and results vanish mid-word.
func ftsExpr(t searchquery.Term, prefix bool) string {
	expr := `"` + strings.ReplaceAll(t.Value, `"`, `""`) + `"`
	if prefix {
		expr += "*"
	}
	if col, ok := ftsColumns[t.Field]; ok {
		return col + " : (" + expr + ")"
	}
	return expr
}

// termSQL is the condition for one term, or "" if it has none.
func termSQL(t searchquery.Term, prefix bool) (string, []any) {
	var expr string
	var args []any
	switch {
	case t.Field.IsText():
		expr = "messages.id IN (SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?)"
		args = []any{ftsExpr(t, prefix)}
	case t.Field == searchquery.Date:
		var conds []string
		if !t.Start.IsZero() {
			conds = append(conds, "messages.date >= ?")
			args = append(args, t.Start.Unix())
		}
		if !t.End.IsZero() {
			conds = append(conds, "messages.date < ?")
			args = append(args, t.End.Unix())
		}
		if len(conds) == 0 {
			return "", nil
		}
		// A message with no date is stored as 0 and never matches a date test.
		conds = append(conds, "messages.date > 0")
		expr = "(" + strings.Join(conds, " AND ") + ")"
	case t.Field == searchquery.HasAtt:
		expr = "messages.has_attachment = 1"
	case t.Field == searchquery.Unread:
		expr = "messages.read = 0"
	case t.Field == searchquery.Read:
		expr = "messages.read = 1"
	case t.Field == searchquery.Starred:
		expr = "messages.starred = 1"
	case t.Field == searchquery.Tag:
		expr, args = tagPredicate(t.Value)
	default:
		return "", nil
	}
	if t.Negate {
		return "NOT (" + expr + ")", args
	}
	return expr, args
}
