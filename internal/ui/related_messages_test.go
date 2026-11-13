package ui

import (
	"strings"
	"testing"

	"github.com/allisonhere/tidemail/internal/db"
	tea "github.com/charmbracelet/bubbletea"
)

func relatedTestModel(t *testing.T) Model {
	t.Helper()
	f := newServerSearchFixture(t)
	m := f.model
	m.searchMode = false
	m.messages = []db.Message{{ID: 41, MailboxID: f.inbox, UID: 1, Subject: "one"}, {ID: 42, MailboxID: f.inbox, UID: 2, Subject: "two"}}
	m.applyFilter()
	m.focused = paneMessages
	m.messageCursor = 1
	return m
}

func TestLShowsTheConversationOfTheHighlightedMessage(t *testing.T) {
	m := relatedTestModel(t)
	next, cmd := m.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	m = next.(Model)
	if m.searchQuery != "related:42" || !m.searchMode || m.searchEditing {
		t.Fatalf("query %q searchMode=%v editing=%v, want related:42, searching, not editing", m.searchQuery, m.searchMode, m.searchEditing)
	}
	if cmd == nil {
		t.Fatal("no search command started")
	}
	if !strings.Contains(m.statusMsg, "related messages") {
		t.Errorf("status %q", m.statusMsg)
	}
	// Esc leaves, like any search.
	next, _ = m.handleMainKey(tea.KeyMsg{Type: tea.KeyEsc})
	if got := next.(Model); got.searchMode || got.searchQuery != "" {
		t.Errorf("Esc left searchMode=%v query=%q", got.searchMode, got.searchQuery)
	}
}

func TestLWithNothingHighlightedSaysSo(t *testing.T) {
	m := relatedTestModel(t)
	m.messages, m.filteredMessages = nil, nil
	m.applyFilter()
	next, _ := m.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	got := next.(Model)
	if got.searchMode || !strings.Contains(got.statusMsg, "highlight a message first") {
		t.Errorf("searchMode=%v status=%q", got.searchMode, got.statusMsg)
	}
}

func TestRelatedIsInTheCommandPaletteOnlyWithAMessage(t *testing.T) {
	m := relatedTestModel(t)
	find := func(m Model) (enabled, found bool) {
		for _, item := range m.commandItems() {
			if item.id == "related" {
				return item.enabled, true
			}
		}
		return false, false
	}
	if enabled, found := find(m); !found || !enabled {
		t.Errorf("with a message: found=%v enabled=%v", found, enabled)
	}
	next, cmd := m.executeCommand("related")
	if cmd == nil || next.(Model).searchQuery != "related:42" {
		t.Errorf("palette command did not start the search")
	}
}

func TestRelatedSearchOpensTheThread(t *testing.T) {
	m := relatedTestModel(t)
	m.cfg.Display.ThreadedConversations = true
	conversation := []db.Message{
		{ID: 41, MailboxID: m.messages[0].MailboxID, UID: 1, Subject: "Budget", MessageID: "<a@t>"},
		{ID: 42, MailboxID: m.messages[0].MailboxID, UID: 2, Subject: "Re: Budget", MessageID: "<b@t>", InReplyTo: "<a@t>", References: "<a@t>"},
	}
	m.messages = conversation
	m.searchMode, m.searchQuery = true, "related:42"
	m.applyFilter()
	if len(m.messageThreads) != 1 || len(m.messageRows) != 2 {
		t.Fatalf("related search: %d threads, %d rows; want one thread opened to 2 rows", len(m.messageThreads), len(m.messageRows))
	}
	// An ordinary search keeps threads closed.
	m.expandedMessages, m.expansionView = nil, conversationView{}
	m.searchQuery = "budget"
	m.applyFilter()
	if len(m.messageRows) != 1 {
		t.Errorf("ordinary search: %d rows, want the thread collapsed to 1", len(m.messageRows))
	}
}
