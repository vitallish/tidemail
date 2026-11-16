package ui

import (
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// listFocusedReader is a model with a long message open in the reader and the
// focus on the message list.
func listFocusedReader(t *testing.T) Model {
	t.Helper()
	m := readerScrollModel(60*1024, 120, 40, true)
	m.focused = paneMessages
	m.viewport.GotoTop()
	return m
}

func TestReaderScrollKeysScrollFromTheMessageList(t *testing.T) {
	m := listFocusedReader(t)
	cursor, focus, id := m.messageCursor, m.focused, m.contentMessageID

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	m = next.(Model)
	if m.viewport.YOffset != 1 {
		t.Fatalf("ctrl+j: offset = %d, want 1", m.viewport.YOffset)
	}
	for i := 0; i < 4; i++ {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
		m = next.(Model)
	}
	if m.viewport.YOffset != 5 {
		t.Fatalf("after five ctrl+j: offset = %d, want 5", m.viewport.YOffset)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	m = next.(Model)
	if m.viewport.YOffset != 4 {
		t.Fatalf("ctrl+k: offset = %d, want 4", m.viewport.YOffset)
	}
	if m.messageCursor != cursor || m.focused != focus || m.contentMessageID != id {
		t.Fatalf("list state changed: cursor %d→%d focus %v→%v message %d→%d", cursor, m.messageCursor, focus, m.focused, id, m.contentMessageID)
	}
}

func TestReaderScrollKeysStopAtTheEnds(t *testing.T) {
	m := listFocusedReader(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	if got := next.(Model).viewport.YOffset; got != 0 {
		t.Fatalf("ctrl+k at the top moved to %d", got)
	}
	m.viewport.GotoBottom()
	bottom := m.viewport.YOffset
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if got := next.(Model).viewport.YOffset; got != bottom {
		t.Fatalf("ctrl+j at the bottom moved %d→%d", bottom, got)
	}
}

func TestReaderScrollKeysDoNothingWithoutAnOpenMessage(t *testing.T) {
	m := listFocusedReader(t)
	m.contentMessageID = 0
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if cmd != nil || next.(Model).viewport.YOffset != 0 {
		t.Fatal("ctrl+j with no open message must be a no-op")
	}
}

func TestReaderScrollKeysLeaveJKPagingAlone(t *testing.T) {
	m := listFocusedReader(t)
	offset := m.viewport.YOffset
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'J'}})
	got := next.(Model)
	if got.viewport.YOffset != offset {
		t.Fatal("J pages the list; it must not scroll the reader")
	}
	if got.messageCursor == m.messageCursor {
		t.Fatal("J no longer pages the message list")
	}
}

func TestReaderScrollFromListKeepsPaneReuse(t *testing.T) {
	m := listFocusedReader(t)
	if !m.readerOnlyNavigation(tea.KeyMsg{Type: tea.KeyCtrlJ}) {
		t.Fatal("scrolling the reader must keep the sidebar and list output (docs/reader-scrolling.md)")
	}
	m.contentMessageID = 0
	if m.readerOnlyNavigation(tea.KeyMsg{Type: tea.KeyCtrlJ}) {
		t.Fatal("with nothing to scroll the event is not a reader-only movement")
	}
	m = listFocusedReader(t)
	if m.readerOnlyNavigation(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'J'}}) {
		t.Fatal("J changes the list and must invalidate the cached panes")
	}
}

type keyRecorder struct{ got *[]string }

func (k keyRecorder) Init() tea.Cmd { return nil }
func (k keyRecorder) View() string  { return "" }
func (k keyRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		*k.got = append(*k.got, km.String())
		if km.String() == "q" {
			return k, tea.Quit
		}
	}
	return k, nil
}

// The terminal sends Ctrl+J as a bare line feed (0x0a) and Ctrl+K as 0x0b. Enter
// is a carriage return (0x0d), so the decoder must tell them apart; if it did
// not, Ctrl+J would be Enter and the binding would be unusable.
func TestDecoderDeliversCtrlJKDistinctFromEnter(t *testing.T) {
	var got []string
	input := &readerEventInput{packets: make(chan []byte, 8)}
	for _, p := range []string{"\n", "\v", "\r", "q"} {
		input.packets <- []byte(p)
	}
	close(input.packets)
	if _, err := tea.NewProgram(keyRecorder{&got}, tea.WithInput(input), tea.WithOutput(io.Discard), tea.WithoutSignalHandler()).Run(); err != nil {
		t.Fatal(err)
	}
	if want := "ctrl+j ctrl+k enter q"; strings.Join(got, " ") != want {
		t.Fatalf("decoded %q, want %q", strings.Join(got, " "), want)
	}
}
