package ui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/emersion/go-imap/v2"

	"github.com/allisonhere/tidemail/internal/config"

	"github.com/allisonhere/tidemail/internal/db"
	imapClient "github.com/allisonhere/tidemail/internal/imap"
	"github.com/allisonhere/tidemail/internal/searchquery"
)

// Server search asks the IMAP server which mail matches the typed search and
// downloads what the cache does not hold yet. It does not show server results
// itself: once the matches are cached, the normal local search runs again, so
// results, ordering, tags and has:attachment all behave as for any search.
//
// It runs only on request (a key), never per keystroke: IMAP searches one
// folder at a time and some servers are slow.
const (
	// serverSearchPerFolder caps how many new messages one search downloads from
	// a folder. A fetch pulls whole bodies; the status line says when it stopped.
	serverSearchPerFolder = 50
	// serverSearchBatch is how many messages one FETCH asks for.
	serverSearchBatch = 25
	// serverSearchTimeout bounds one whole search over all folders.
	serverSearchTimeout = 3 * time.Minute
)

// ServerSearchDoneMsg reports a finished server search.
type ServerSearchDoneMsg struct {
	Query   string
	Folders int      // folders searched
	Found   int      // messages the server matched
	Fetched int      // of those, newly downloaded
	Capped  int      // matches left undownloaded by the per-folder cap
	Errs    []string // per-folder failures (a search carries on past them)
	Err     error    // the search could not run at all
}

// serverSearchSkippedHint explains terms the server did not search.
func serverSearchSkippedHint(skipped []searchquery.Term) string {
	var names []string
	for _, t := range skipped {
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

// searchScopeAccounts is the accounts a server search covers: the account of
// the highlighted folder or account row, else every account.
func (m Model) searchScopeAccounts() []db.Account {
	if row := m.selectedSidebarAccountID(); row != 0 {
		if acc := m.accountByID(row); acc != nil {
			return []db.Account{*acc}
		}
	}
	return append([]db.Account(nil), m.accounts...)
}

// selectedSidebarAccountID is the account of the highlighted folder or account
// row, or 0 for rows that span accounts (unified inbox and the like).
func (m Model) selectedSidebarAccountID() int64 {
	if mb := m.selectedMailbox(); mb != nil {
		return mb.AccountID
	}
	if id, ok := m.selectedAccountID(); ok {
		return id
	}
	return 0
}

func (m Model) startServerSearch() (tea.Model, tea.Cmd) {
	query := strings.TrimSpace(m.searchQuery)
	if !m.searchMode || query == "" {
		m.setStatus("type a search first (/), then search the server", false)
		return m, m.clearStatusCmd()
	}
	if m.sessions == nil || len(m.accounts) == 0 {
		m.setStatus("no account to search", true)
		return m, m.clearStatusCmd()
	}
	parsed := searchquery.Parse(query, time.Now())
	criteria, skipped, ok := imapClient.SearchCriteria(parsed)
	if !ok {
		m.setStatus("the server cannot search that on its own — add a word, sender, subject or date ("+serverSearchSkippedHint(skipped)+" is checked locally)", true)
		return m, m.clearStatusCmd()
	}
	scope := m.searchScopeAccounts()
	names := make([]string, 0, len(scope))
	for _, a := range scope {
		names = append(names, a.Name)
	}
	m.setStatus("searching the server ("+strings.Join(names, ", ")+")…", false)
	return m, m.serverSearchCmd(query, scope, criteria)
}

func (m Model) serverSearchCmd(query string, scope []db.Account, criteria *imap.SearchCriteria) tea.Cmd {
	database := m.db
	sessions := m.sessions
	type target struct {
		acc  db.Account
		cfg  config.AccountConfig
		boxs []db.Mailbox
	}
	var targets []target
	var setupErrs []string
	for _, acc := range scope {
		acfg, err := m.accountConfigFor(acc)
		if err != nil {
			setupErrs = append(setupErrs, fmt.Sprintf("%s: %v", acc.Name, err))
			continue
		}
		boxes, err := database.ListMailboxes(acc.ID)
		if err != nil {
			setupErrs = append(setupErrs, fmt.Sprintf("%s: %v", acc.Name, err))
			continue
		}
		targets = append(targets, target{acc: acc, cfg: acfg, boxs: searchableMailboxes(boxes)})
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), serverSearchTimeout)
		defer cancel()
		done := ServerSearchDoneMsg{Query: query, Errs: setupErrs}
		for _, t := range targets {
			err := sessions.Do(ctx, t.cfg, func(client *imapClient.Client) error {
				for _, mb := range t.boxs {
					found, fetched, capped, err := searchFolder(ctx, client, database, mb, criteria)
					done.Folders++
					done.Found += found
					done.Fetched += fetched
					done.Capped += capped
					if err != nil {
						done.Errs = append(done.Errs, fmt.Sprintf("%s/%s: %v", t.acc.Name, mb.Name, err))
					}
					if ctx.Err() != nil {
						return ctx.Err()
					}
				}
				return nil
			})
			if err != nil {
				done.Errs = append(done.Errs, fmt.Sprintf("%s: %v", t.acc.Name, err))
			}
		}
		if done.Folders == 0 && len(done.Errs) > 0 {
			done.Err = fmt.Errorf("%s", strings.Join(done.Errs, "; "))
		}
		return done
	}
}

// searchableMailboxes drops folders that cannot be searched or that hold only
// local state (Drafts are kept locally, not synced as mail).
func searchableMailboxes(boxes []db.Mailbox) []db.Mailbox {
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
	if len(missing) > serverSearchPerFolder {
		capped = len(missing) - serverSearchPerFolder
		missing = missing[:serverSearchPerFolder]
	}
	for start := 0; start < len(missing); start += serverSearchBatch {
		end := min(start+serverSearchBatch, len(missing))
		msgs, err := client.FetchUIDs(ctx, mb.Name, missing[start:end])
		if err != nil {
			return len(uids), fetched, capped, err
		}
		if _, err := storeFetchedMessages(database, mb.ID, msgs); err != nil {
			return len(uids), fetched, capped, err
		}
		fetched += len(msgs)
	}
	return len(uids), fetched, capped, nil
}

// serverSearchSummary is the status line after a search.
func serverSearchSummary(msg ServerSearchDoneMsg, skipped string) (string, bool) {
	if msg.Err != nil {
		return "server search failed: " + msg.Err.Error(), true
	}
	text := fmt.Sprintf("server search: %d match%s in %d folder%s", msg.Found, pluralForm(msg.Found, "", "es"), msg.Folders, pluralForm(msg.Folders, "", "s"))
	if msg.Fetched > 0 {
		text += fmt.Sprintf(", %d new downloaded", msg.Fetched)
	}
	if msg.Capped > 0 {
		text += fmt.Sprintf(", %d more not downloaded (narrow the search)", msg.Capped)
	}
	if skipped != "" {
		text += "; " + skipped + " checked locally"
	}
	if len(msg.Errs) > 0 {
		return text + "; " + fmt.Sprintf("%d folder%s failed: %s", len(msg.Errs), pluralForm(len(msg.Errs), "", "s"), msg.Errs[0]), true
	}
	return text, false
}

func pluralForm(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// handleServerSearchDone shows the outcome and re-runs the local search, which
// now sees what was downloaded. A search the user has since changed is ignored.
func (m Model) handleServerSearchDone(msg ServerSearchDoneMsg) (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.searchQuery) != msg.Query || !m.searchMode {
		return m, nil
	}
	skipped := ""
	_, left, _ := imapClient.SearchCriteria(searchquery.Parse(msg.Query, time.Now()))
	if len(left) > 0 {
		skipped = serverSearchSkippedHint(left)
	}
	text, isErr := serverSearchSummary(msg, skipped)
	m.setStatus(text, isErr)
	cmds := []tea.Cmd{m.clearStatusCmd()}
	if msg.Fetched > 0 {
		cmds = append(cmds, m.searchAllMessagesCmd(m.searchQuery))
	}
	return m, tea.Batch(cmds...)
}
