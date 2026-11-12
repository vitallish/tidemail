package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// showRelated turns the search row into related:<id> for the highlighted (or
// open) message: its whole conversation from every folder of the account, found
// by reply headers. It is an ordinary search, so everything else about searching
// applies: edit the query, add terms, or press Ctrl+G to also look on the server.
func (m Model) showRelated() (tea.Model, tea.Cmd) {
	msg := m.commandMessage()
	if msg == nil || msg.ID == 0 || m.selectedDraftsMailbox() {
		m.setStatus("highlight a message first to see its related messages", false)
		return m, m.clearStatusCmd()
	}
	next, cmd := m.handleSearchCharacter(fmt.Sprintf("related:%d", msg.ID))
	m = next.(Model)
	m.searchMode, m.searchEditing = true, false
	m.searchInput.Blur()
	m.focused = paneMessages
	m.setStatus("related messages (reply headers, all folders) — Ctrl+G also searches the server, Esc leaves", false)
	return m, tea.Batch(cmd, m.clearStatusCmd())
}
