package mailapp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/tidemail/internal/db"
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
		_, err := ServerSearch(context.Background(), nil, nil, nil, query, time.Now())
		var refused *NothingToSearchError
		if !errors.As(err, &refused) {
			t.Errorf("%q: err = %v, want NothingToSearchError", query, err)
		}
	}
	_, err := ServerSearch(context.Background(), nil, nil, nil, "has:attachment #x", time.Now())
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
