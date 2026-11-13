package mailapp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	imapClient "github.com/allisonhere/tidemail/internal/imap"
	"github.com/allisonhere/tidemail/internal/searchquery"
	"github.com/emersion/go-imap/v2"
)

// Server search asks the IMAP server which mail matches a search and downloads
// what the cache does not hold yet. It does not return results itself: once the
// matches are cached, the ordinary local search over the cache answers, so
// results, ordering, tags and has:attachment behave as for any search. The
// terminal interface and the command line both run this.
const (
	// ServerSearchPerFolder caps how many new messages one search downloads from
	// a folder. A fetch pulls whole bodies; the result says when it stopped.
	ServerSearchPerFolder = 50
	// ServerSearchTimeout bounds one whole search over all folders.
	ServerSearchTimeout = 3 * time.Minute
	// serverSearchBatch is how many messages one FETCH asks for.
	serverSearchBatch = 25
)

// SearchTarget is an account to search, with the settings to reach it.
type SearchTarget struct {
	Account db.Account
	Config  config.AccountConfig
}

// ServerSearchResult is what a server search did.
type ServerSearchResult struct {
	Folders int      // folders searched
	Found   int      // messages the server matched
	Fetched int      // of those, newly downloaded
	Capped  int      // matches left undownloaded by the per-folder cap
	Errs    []string // per-folder failures; a search carries on past them
	Skipped []searchquery.Term
	// WindowDays is the default date window that applied (0 when none: the query
	// had its own date or the window is off).
	WindowDays int
}

// NothingToSearchError is returned when no part of a search can go to the
// server (a bare has:attachment or #tag), rather than fetching a whole folder.
type NothingToSearchError struct{ Skipped []searchquery.Term }

func (e *NothingToSearchError) Error() string {
	return "the server cannot search that on its own — add a word, sender, subject or date (" + DescribeTerms(e.Skipped) + " is checked locally)"
}

// DescribeTerms names terms for a message to the user.
func DescribeTerms(terms []searchquery.Term) string {
	var names []string
	for _, t := range terms {
		name := string(t.Field)
		switch t.Field {
		case searchquery.HasAtt:
			name = "has:attachment"
		case searchquery.Tag:
			name = "#" + t.Value
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

// ServerSearch searches the targets' folders on the server and stores the
// matches missing from the cache. Failure of one folder or account is recorded
// in the result and does not stop the others.
//
// windowDays limits the search to mail sent in the last that many days when the
// query has no date of its own (see WithServerWindow); 0 means no limit.
func ServerSearch(ctx context.Context, database *db.DB, sessions *imapClient.SessionPool, targets []SearchTarget, query string, now time.Time, windowDays int) (ServerSearchResult, error) {
	var res ServerSearchResult
	criteria, skipped, ok := imapClient.SearchCriteria(ServerQuery(query, now, windowDays))
	if !ok {
		return res, &NothingToSearchError{Skipped: skipped}
	}
	res.Skipped = skipped
	res.WindowDays = appliedWindow(query, now, windowDays)
	for _, t := range targets {
		boxes, err := database.ListMailboxes(t.Account.ID)
		if err != nil {
			res.Errs = append(res.Errs, fmt.Sprintf("%s: %v", t.Account.Name, err))
			continue
		}
		boxes = SearchableMailboxes(boxes)
		err = sessions.Do(ctx, t.Config, func(client *imapClient.Client) error {
			for _, mb := range boxes {
				found, fetched, capped, err := searchFolder(ctx, client, database, mb, criteria)
				res.Folders++
				res.Found += found
				res.Fetched += fetched
				res.Capped += capped
				if err != nil {
					res.Errs = append(res.Errs, fmt.Sprintf("%s/%s: %v", t.Account.Name, mb.Name, err))
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
			return nil
		})
		if err != nil {
			res.Errs = append(res.Errs, fmt.Sprintf("%s: %v", t.Account.Name, err))
		}
	}
	if res.Folders == 0 && len(res.Errs) > 0 {
		return res, errors.New(strings.Join(res.Errs, "; "))
	}
	return res, nil
}

// ServerQuery parses a search the way the server search runs it: with the
// default date window added.
func ServerQuery(query string, now time.Time, windowDays int) searchquery.Query {
	return WithServerWindow(searchquery.Parse(query, now), now, windowDays)
}

// WithServerWindow adds "after:<windowDays>d" to q so a server search stays quick
// on a big mailbox and has something the server can run for has:attachment. It
// leaves q alone when windowDays is 0 or the query already has a date term
// (after:, before: or on:), so an explicit date always wins. The window only
// limits what the server is asked for: the local search that follows still shows
// older mail that is already cached.
func WithServerWindow(q searchquery.Query, now time.Time, windowDays int) searchquery.Query {
	if windowDays <= 0 {
		return q
	}
	for _, t := range q.Terms() {
		if t.Field == searchquery.Date {
			return q
		}
	}
	window := searchquery.Parse(fmt.Sprintf("after:%dd", windowDays), now)
	out := searchquery.Query{Problems: q.Problems}
	out.Clauses = append(append(out.Clauses, q.Clauses...), window.Clauses...)
	return out
}

// WithWindowText returns query with " after:<windowDays>d" appended when the
// server search would add the window (see WithServerWindow), and whether it did.
// The terminal interface writes this into the search field, so the limit is
// visible and can be edited or replaced by a date of the user's own.
func WithWindowText(query string, now time.Time, windowDays int) (string, bool) {
	if appliedWindow(query, now, windowDays) == 0 {
		return query, false
	}
	return strings.TrimSpace(query) + fmt.Sprintf(" after:%dd", windowDays), true
}

// appliedWindow is windowDays when ServerQuery added the window to query, else 0.
func appliedWindow(query string, now time.Time, windowDays int) int {
	if len(ServerQuery(query, now, windowDays).Clauses) == len(searchquery.Parse(query, now).Clauses) {
		return 0
	}
	return windowDays
}

// SearchableMailboxes drops folders that cannot be searched or that hold only
// local state (Drafts are kept locally, not synced as mail).
func SearchableMailboxes(boxes []db.Mailbox) []db.Mailbox {
	var out []db.Mailbox
	for _, mb := range boxes {
		skip := false
		for _, f := range mb.Flags {
			switch strings.ToLower(f) {
			case `\noselect`, `\nonexistent`, `\drafts`:
				skip = true
			}
		}
		if !skip {
			out = append(out, mb)
		}
	}
	return out
}

// searchFolder searches one folder and downloads matches the cache lacks, the
// newest first, up to the per-folder cap.
func searchFolder(ctx context.Context, client *imapClient.Client, database *db.DB, mb db.Mailbox, criteria *imap.SearchCriteria) (found, fetched, capped int, err error) {
	uids, err := client.SearchUIDs(ctx, mb.Name, criteria)
	if err != nil {
		return 0, 0, 0, err
	}
	known, err := database.KnownMessageUIDs(mb.ID)
	if err != nil {
		return len(uids), 0, 0, err
	}
	var missing []uint32
	for _, uid := range uids {
		if !known[uid] {
			missing = append(missing, uid)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] > missing[j] })
	if len(missing) > ServerSearchPerFolder {
		capped = len(missing) - ServerSearchPerFolder
		missing = missing[:ServerSearchPerFolder]
	}
	for start := 0; start < len(missing); start += serverSearchBatch {
		end := min(start+serverSearchBatch, len(missing))
		msgs, err := client.FetchUIDs(ctx, mb.Name, missing[start:end])
		if err != nil {
			return len(uids), fetched, capped, err
		}
		if _, err := StoreFetchedMessages(database, mb.ID, msgs); err != nil {
			return len(uids), fetched, capped, err
		}
		fetched += len(msgs)
	}
	return len(uids), fetched, capped, nil
}

// Summary is one line saying what the search did, for the status bar or a
// terminal. failed is true when some folder could not be searched.
func (r ServerSearchResult) Summary() (text string, failed bool) {
	text = fmt.Sprintf("server search: %d match%s in %d folder%s", r.Found, pluralForm(r.Found, "", "es"), r.Folders, pluralForm(r.Folders, "", "s"))
	if r.Fetched > 0 {
		text += fmt.Sprintf(", %d new downloaded", r.Fetched)
	}
	if r.WindowDays > 0 {
		text += fmt.Sprintf(" (last %d days)", r.WindowDays)
	}
	if r.Capped > 0 {
		text += fmt.Sprintf(", %d more not downloaded (narrow the search)", r.Capped)
	}
	if len(r.Skipped) > 0 {
		text += "; " + DescribeTerms(r.Skipped) + " checked locally"
	}
	if len(r.Errs) > 0 {
		return text + fmt.Sprintf("; %d folder%s failed: %s", len(r.Errs), pluralForm(len(r.Errs), "", "s"), r.Errs[0]), true
	}
	return text, false
}

func pluralForm(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
