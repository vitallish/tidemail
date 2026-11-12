package db

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

// searchFixture seeds a small mailbox whose messages differ in exactly the
// ways the search operators distinguish.
func searchFixture(t *testing.T) *DB {
	t.Helper()
	d := newTestDB(t)
	acc, err := d.AddAccount("", "Work", "")
	if err != nil {
		t.Fatal(err)
	}
	box, err := d.UpsertMailbox(Mailbox{AccountID: acc, Name: "INBOX", Delimiter: "/"})
	if err != nil {
		t.Fatal(err)
	}
	day := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	msgs := []Message{
		{Subject: "Invoice March", From: "Ann Lee <ann@example.com>", To: "me@work.test", BodyText: "please pay the attached invoice", Date: day("2026-03-10 09:00"), Read: true, HasAttachment: true},
		{Subject: "Lunch", From: "Bob Ray <bob@example.com>", To: "me@work.test", BodyText: "noon at the usual place", Date: day("2026-03-11 12:00"), Starred: true},
		{Subject: "Re: Invoice March", From: "me@work.test", To: "ann@example.com", BodyText: "paid, thanks", Date: day("2026-03-12 08:00"), Read: true},
		{Subject: "Newsletter", From: "News <news@example.org>", To: "me@work.test", CC: "ann@example.com", BodyText: "invoice season is here", Date: day("2026-04-01 07:30")},
		{Subject: "Photos", From: "Cy <cy@example.net>", To: "me@work.test", BodyText: "holiday pictures", Date: day("2026-04-02 23:59"), Read: true, HasAttachment: true},
	}
	for i := range msgs {
		msgs[i].MailboxID, msgs[i].UID = box, uint32(i+1)
		if err := d.UpsertMessage(msgs[i]); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func subjects(t *testing.T, d *DB, query string) []string {
	t.Helper()
	found, err := d.SearchAllMessages(query, false)
	if err != nil {
		t.Fatalf("SearchAllMessages(%q): %v", query, err)
	}
	var out []string
	for _, m := range found {
		out = append(out, m.Subject)
	}
	sort.Strings(out)
	return out
}

func TestSearchOperators(t *testing.T) {
	d := searchFixture(t)
	cases := []struct {
		query string
		want  []string
	}{
		// Fields. "invoice" alone matches anywhere, including the body.
		{"invoice", []string{"Invoice March", "Newsletter", "Re: Invoice March"}},
		{"subject:invoice", []string{"Invoice March", "Re: Invoice March"}},
		{"body:invoice", []string{"Invoice March", "Newsletter"}},
		{"from:ann", []string{"Invoice March"}},
		{"from:example.com", []string{"Invoice March", "Lunch"}},
		{"to:ann", []string{"Re: Invoice March"}},
		{"cc:ann", []string{"Newsletter"}},
		{"from:ann@example.com", []string{"Invoice March"}},
		{`from:"ann lee"`, []string{"Invoice March"}},
		{"subject:invoice from:ann", []string{"Invoice March"}},
		// Anything-field matching is wider than a field filter.
		{"ann", []string{"Invoice March", "Newsletter", "Re: Invoice March"}},

		// OR joins neighbours; AND is the default between clauses.
		{"from:ann OR from:bob", []string{"Invoice March", "Lunch"}},
		{"from:ann OR from:bob subject:lunch", []string{"Lunch"}},
		{"lunch OR photos", []string{"Lunch", "Photos"}},
		{"subject:newsletter OR is:starred", []string{"Lunch", "Newsletter"}},

		// Exclusion.
		{"invoice -subject:re", []string{"Invoice March", "Newsletter"}},
		{"-invoice subject:photos", []string{"Photos"}},
		{"invoice -from:ann -from:news", []string{"Re: Invoice March"}},

		// Flags.
		{"has:attachment", []string{"Invoice March", "Photos"}},
		{"-has:attachment invoice", []string{"Newsletter", "Re: Invoice March"}},
		{"is:starred", []string{"Lunch"}},
		{"is:unread", []string{"Lunch", "Newsletter"}},
		{"is:read has:attachment", []string{"Invoice March", "Photos"}},
		{"is:unread is:read", nil},

		// Dates: after is inclusive, before exclusive, on is the day.
		{"after:2026-03-11", []string{"Lunch", "Newsletter", "Photos", "Re: Invoice March"}},
		{"before:2026-03-11", []string{"Invoice March"}},
		{"after:2026-03-11 before:2026-04-01", []string{"Lunch", "Re: Invoice March"}},
		{"on:2026-04-02", []string{"Photos"}}, // 23:59 still belongs to the day
		{"on:2026-04-01", []string{"Newsletter"}},
		{"on:2026-03-13", nil},
		{"-on:2026-03-11 subject:lunch", nil},
		{"on:2026-03-10 OR on:2026-03-12", []string{"Invoice March", "Re: Invoice March"}},
		{"invoice before:2026-03-31", []string{"Invoice March", "Re: Invoice March"}},

		// Mid-typing and unusable values never error; they drop out.
		{"from:", nil},
		{"invoice after:2026-0", []string{"Invoice March", "Newsletter", "Re: Invoice March"}},
		{"has:picture", nil},
		{"", nil},
	}
	for _, c := range cases {
		if got := subjects(t, d, c.query); !reflect.DeepEqual(got, c.want) {
			t.Errorf("search %q = %v, want %v", c.query, got, c.want)
		}
	}
}

// A query without operators must behave exactly as it did before operators
// existed: ANDed words, the last one as a prefix.
func TestSearchWithoutOperatorsIsUnchanged(t *testing.T) {
	d := searchFixture(t)
	cases := map[string][]string{
		"inv":                {"Invoice March", "Newsletter", "Re: Invoice March"}, // prefix while typing
		"invoice pay":        {"Invoice March"},                                    // words are ANDed, last is a prefix
		"pay invoice":        {"Invoice March"},
		"holiday pictures":   {"Photos"},
		"ann@example.com":    {"Invoice March", "Newsletter", "Re: Invoice March"},
		"nothingmatchesthis": nil,
	}
	for query, want := range cases {
		if got := subjects(t, d, query); !reflect.DeepEqual(got, want) {
			t.Errorf("search %q = %v, want %v", query, got, want)
		}
	}
}

// Quotes are new: before operators a quoted phrase was split into loose words,
// so "thanks paid" matched a message saying "paid, thanks". A phrase now keeps
// its word order.
func TestSearchPhrasesKeepWordOrder(t *testing.T) {
	d := searchFixture(t)
	cases := map[string][]string{
		`"paid, thanks"`:          {"Re: Invoice March"},
		`"thanks paid"`:           nil,
		`body:"attached invoice"`: {"Invoice March"},
		`body:"invoice attached"`: nil,
	}
	for query, want := range cases {
		if got := subjects(t, d, query); !reflect.DeepEqual(got, want) {
			t.Errorf("search %q = %v, want %v", query, got, want)
		}
	}
}

func TestSearchPrefixOnlyAppliesToTheWordBeingTyped(t *testing.T) {
	d := searchFixture(t)
	// "inv" is a prefix only while it is the last word; with another word after
	// it, it is a finished word and must match whole tokens.
	if got := subjects(t, d, "inv lunch"); got != nil {
		t.Errorf("inv lunch = %v, want none (inv is not a whole word)", got)
	}
	if got := subjects(t, d, "invoice lun"); got != nil {
		t.Errorf("invoice lun = %v, want none", got)
	}
	if got := subjects(t, d, "subject:inv"); !reflect.DeepEqual(got, []string{"Invoice March", "Re: Invoice March"}) {
		t.Errorf("subject:inv = %v, want both invoice subjects", got)
	}
}

func TestSearchPagesCountAndOrderWithOperators(t *testing.T) {
	d := searchFixture(t)
	page, total, err := d.SearchAllMessagesPage("is:read", false, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(page) != 2 {
		t.Fatalf("total=%d page=%d, want 3 and 2", total, len(page))
	}
	// Newest first when nothing ranks the results.
	if page[0].Subject != "Photos" || page[1].Subject != "Re: Invoice March" {
		t.Errorf("order = %q, %q", page[0].Subject, page[1].Subject)
	}
	rest, _, err := d.SearchAllMessagesPage("is:read", false, 2, 2)
	if err != nil || len(rest) != 1 || rest[0].Subject != "Invoice March" {
		t.Errorf("second page = %v, %v", rest, err)
	}
	unread, total, err := d.SearchAllMessagesPage("invoice", true, 10, 0)
	if err != nil || total != 1 || unread[0].Subject != "Newsletter" {
		t.Errorf("unread-only invoice = %v (%d), %v", unread, total, err)
	}
}

func TestSearchTagsStillCombineWithOperators(t *testing.T) {
	a := newAttentionDB(t)
	a.add(a.inbox, "ci failed", 1, "smart category github")
	a.add(a.inbox, "invoice due", 2, "smart category billing")
	a.add(a.inbox, "pr review", 3, "smart category github")
	got, err := a.SearchAllMessages("#github -subject:ci", false)
	if err != nil || len(got) != 1 || got[0].Subject != "pr review" {
		t.Fatalf("#github -subject:ci = %v, %v", got, err)
	}
	got, err = a.SearchAllMessages("-#github", false)
	if err != nil || len(got) != 1 || got[0].Subject != "invoice due" {
		t.Fatalf("-#github = %v, %v", got, err)
	}
}
