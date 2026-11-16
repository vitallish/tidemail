package mailapp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/db"
	imapClient "github.com/allisonhere/tidemail/internal/imap"
	"github.com/allisonhere/tidemail/internal/searchquery"
)

func TestSearchableMailboxesSkipsFoldersThatHoldNoServerMail(t *testing.T) {
	boxes := []db.Mailbox{
		{Name: "INBOX"}, {Name: "Drafts", Flags: []string{`\Drafts`}},
		{Name: "[Gmail]", Flags: []string{`\Noselect`, `\HasChildren`}}, {Name: "Sent", Flags: []string{`\Sent`}},
	}
	var got []string
	for _, mb := range SearchableMailboxes(boxes) {
		got = append(got, mb.Name)
	}
	if strings.Join(got, ",") != "INBOX,Sent" {
		t.Errorf("searchable = %v, want INBOX and Sent", got)
	}
}

func TestServerSearchRefusesWhatOnlyTheCacheCanAnswer(t *testing.T) {
	for _, query := range []string{"has:attachment", "#github", "-has:attachment", ""} {
		_, err := ServerSearch(context.Background(), nil, nil, nil, query, time.Now(), 0)
		var refused *NothingToSearchError
		if !errors.As(err, &refused) {
			t.Errorf("%q: err = %v, want NothingToSearchError", query, err)
		}
	}
	_, err := ServerSearch(context.Background(), nil, nil, nil, "has:attachment #x", time.Now(), 0)
	var refused *NothingToSearchError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "has:attachment, #x") {
		t.Errorf("message %v does not name what is checked locally", err)
	}
}

func TestAccountConfigForMatchesByIDThenUniqueName(t *testing.T) {
	// Resolution rules are exercised through the UI tests; this guards the move.
	if _, err := AccountConfigFor(nil, db.Account{Name: "x"}); !errors.Is(err, ErrNoAccountConfig) {
		t.Errorf("no config block: %v", err)
	}
}

func TestServerSearchSummaryNamesFailuresCapsAndLocalTerms(t *testing.T) {
	text, failed := ServerSearchResult{Found: 1, Folders: 3, Errs: []string{"Work/Archive: boom"}}.Summary()
	if !failed || !strings.Contains(text, "1 match in 3 folders") || !strings.Contains(text, "1 folder failed: Work/Archive: boom") {
		t.Errorf("summary %q failed=%v", text, failed)
	}
	text, failed = ServerSearchResult{Found: 60, Folders: 1, Fetched: 50, Capped: 10}.Summary()
	if failed || !strings.Contains(text, "50 new downloaded") || !strings.Contains(text, "10 more not downloaded") {
		t.Errorf("summary %q failed=%v", text, failed)
	}
	q := searchquery.Parse("has:attachment #x", time.Now())
	text, _ = ServerSearchResult{Skipped: q.Terms()}.Summary()
	if !strings.Contains(text, "has:attachment, #x checked locally") {
		t.Errorf("summary %q", text)
	}
}

func TestServerQueryAddsTheDefaultWindowUnlessTheQueryHasADate(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	hasDate := func(q searchquery.Query) (n int, start time.Time) {
		for _, term := range q.Terms() {
			if term.Field == searchquery.Date {
				n++
				start = term.Start
			}
		}
		return n, start
	}
	n, start := hasDate(ServerQuery("from:ann", now, 365))
	if n != 1 || !start.Equal(time.Date(2025, 11, 12, 0, 0, 0, 0, start.Location())) {
		t.Errorf("window: %d date terms, start %v; want one starting a year back", n, start)
	}
	for _, query := range []string{"from:ann after:2026-01-01", "from:ann before:2020-01-01", "from:ann on:2026-03-03", "from:ann OR after:7d"} {
		if n, _ := hasDate(ServerQuery(query, now, 365)); n != 1 {
			t.Errorf("%q: %d date terms, want only the typed one", query, n)
		}
	}
	if n, _ := hasDate(ServerQuery("from:ann", now, 0)); n != 0 {
		t.Errorf("window 0 must add nothing, got %d date terms", n)
	}
}

func TestTheWindowMakesBareAttachmentSearchesRunnable(t *testing.T) {
	now := time.Now()
	if _, _, ok := imapClient.SearchCriteria(ServerQuery("has:attachment", now, 0)); ok {
		t.Error("without a window a bare has:attachment has nothing for the server")
	}
	if _, _, ok := imapClient.SearchCriteria(ServerQuery("has:attachment", now, 365)); !ok {
		t.Error("with a window a bare has:attachment should reach the server")
	}
}

func TestAppliedWindowIsReportedOnlyWhenAdded(t *testing.T) {
	now := time.Now()
	if got := appliedWindow("from:ann", now, 365); got != 365 {
		t.Errorf("added window = %d, want 365", got)
	}
	if got := appliedWindow("from:ann after:30d", now, 365); got != 0 {
		t.Errorf("query had its own date, window = %d, want 0", got)
	}
	text, _ := ServerSearchResult{Found: 2, Folders: 1, WindowDays: 365}.Summary()
	if !strings.Contains(text, "last 365 days") {
		t.Errorf("summary %q should say the window", text)
	}
}

func TestWithWindowTextAppendsOnlyWhenTheWindowApplies(t *testing.T) {
	now := time.Now()
	for query, want := range map[string]string{
		"from:ann":               "from:ann after:365d",
		"  has:attachment ":      "has:attachment after:365d",
		"from:ann after:7d":      "from:ann after:7d",
		"from:ann on:2026-03-03": "from:ann on:2026-03-03",
	} {
		got, added := WithWindowText(query, now, 365)
		if got != want || added != (got != query) {
			t.Errorf("%q -> %q (added=%v), want %q", query, got, added, want)
		}
	}
	if got, added := WithWindowText("from:ann", now, 0); added || got != "from:ann" {
		t.Errorf("window 0 changed the query: %q", got)
	}
}
