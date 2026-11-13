package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/tidemail/internal/db"
	imapClient "github.com/allisonhere/tidemail/internal/imap"
	"github.com/allisonhere/tidemail/internal/mailapp"
)

// Server search asks the IMAP server which mail matches the typed search and
// downloads what the cache does not hold yet. It does not show server results
// itself: once the matches are cached, the normal local search runs again, so
// results, ordering, tags and has:attachment all behave as for any search.
//
// It runs only on request (a key), never per keystroke: IMAP searches one
// folder at a time and some servers are slow.
// ServerSearchDoneMsg reports a finished server search.
type ServerSearchDoneMsg struct {
	Query  string
	Result mailapp.ServerSearchResult
	Err    error // the search could not run at all
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
	parsed := mailapp.ServerQuery(query, time.Now(), m.cfg.Display.ServerSearchWindowDays)
	_, skipped, ok := imapClient.SearchCriteria(parsed)
	if !ok {
		m.setStatus("the server cannot search that on its own — add a word, sender, subject or date ("+mailapp.DescribeTerms(skipped)+" is checked locally)", true)
		return m, m.clearStatusCmd()
	}
	scope := m.searchScopeAccounts()
	names := make([]string, 0, len(scope))
	for _, a := range scope {
		names = append(names, a.Name)
	}
	m.setStatus("searching the server ("+strings.Join(names, ", ")+")…", false)
	return m, m.serverSearchCmd(query, scope)
}

func (m Model) serverSearchCmd(query string, scope []db.Account) tea.Cmd {
	database := m.db
	sessions := m.sessions
	window := m.cfg.Display.ServerSearchWindowDays
	var targets []mailapp.SearchTarget
	var setupErrs []string
	for _, acc := range scope {
		acfg, err := m.accountConfigFor(acc)
		if err != nil {
			setupErrs = append(setupErrs, fmt.Sprintf("%s: %v", acc.Name, err))
			continue
		}
		targets = append(targets, mailapp.SearchTarget{Account: acc, Config: acfg})
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), mailapp.ServerSearchTimeout)
		defer cancel()
		res, err := mailapp.ServerSearch(ctx, database, sessions, targets, query, time.Now(), window)
		res.Errs = append(setupErrs, res.Errs...)
		if err == nil && res.Folders == 0 && len(res.Errs) > 0 {
			err = fmt.Errorf("%s", strings.Join(res.Errs, "; "))
		}
		return ServerSearchDoneMsg{Query: query, Result: res, Err: err}
	}
}

// handleServerSearchDone shows the outcome and re-runs the local search, which
// now sees what was downloaded. A search the user has since changed is ignored.
func (m Model) handleServerSearchDone(msg ServerSearchDoneMsg) (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.searchQuery) != msg.Query || !m.searchMode {
		return m, nil
	}
	if msg.Err != nil {
		m.setStatus("server search failed: "+msg.Err.Error(), true)
		return m, m.clearStatusCmd()
	}
	text, failed := msg.Result.Summary()
	m.setStatus(text, failed)
	cmds := []tea.Cmd{m.clearStatusCmd()}
	if msg.Result.Fetched > 0 {
		cmds = append(cmds, m.searchAllMessagesCmd(m.searchQuery))
	}
	return m, tea.Batch(cmds...)
}
