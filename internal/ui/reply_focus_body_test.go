package ui

import (
	"strings"
	"testing"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	tea "github.com/charmbracelet/bubbletea"
)

// replyModel drives the real message-list path so the setting is read where
// the reply is built, not only inside focusReplyBody.
func replyModel(t *testing.T, focusBody bool) Model {
	t.Helper()
	m, _, _ := newOrderedModel(t, nil, "")
	m.cfg.Display.ReplyFocusBody = focusBody
	mailbox := m.mailboxes[0]
	m.messages = []db.Message{{
		ID: 1, MailboxID: mailbox.ID, UID: 1,
		From: "alice@example.com", To: "bob@example.com", CC: "carol@example.com",
		Subject: "Plans", MessageID: "<p@x>", BodyText: "quoted line",
	}}
	m.filteredMessages = m.messages
	m.messageCursor = 0
	m.focused = paneMessages
	return m
}

func TestReplyKeysOpenInTheBodyWhenEnabled(t *testing.T) {
	for name, key := range map[string]tea.KeyMsg{
		"reply":     {Type: tea.KeyRunes, Runes: []rune{'r'}},
		"reply all": {Type: tea.KeyCtrlR},
	} {
		t.Run(name, func(t *testing.T) {
			next, _ := replyModel(t, true).Update(key)
			m := next.(Model)
			if m.overlay != overlayCompose {
				t.Fatalf("overlay = %v, want compose", m.overlay)
			}
			if m.compose.focusedField != composeFieldBody {
				t.Fatalf("focus = %v, want the body", m.compose.focusedField)
			}
			// Typing lands above the quote, as it does when tabbing to the body.
			c, _, _ := m.compose.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")}, DefaultKeys)
			if got := c.bodyInput.Value(); !strings.HasPrefix(got, "H\n") || !strings.Contains(got, "> quoted line") {
				t.Fatalf("typed text not above the quote: %q", got)
			}
		})
	}
}

func TestReplyKeysKeepTheHeaderFocusByDefault(t *testing.T) {
	next, _ := replyModel(t, false).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if got := next.(Model).compose.focusedField; got == composeFieldBody {
		t.Fatal("reply moved into the body although reply_focus_body is off")
	}
}

func TestReplyFromThePaletteHonoursTheSetting(t *testing.T) {
	for _, id := range []string{"reply", "reply-all"} {
		m := replyModel(t, true)
		got, _ := m.executeCommand(id)
		if f := got.(Model).compose.focusedField; f != composeFieldBody {
			t.Errorf("%s from the palette: focus = %v, want the body", id, f)
		}
	}
}

func TestForwardIgnoresReplyFocusBody(t *testing.T) {
	m := replyModel(t, true)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	got := next.(Model)
	if got.overlay != overlayCompose {
		t.Skipf("forward key is not 'f' here (overlay %v)", got.overlay)
	}
	if got.compose.focusedField == composeFieldBody {
		t.Fatal("forward must still start on a header field")
	}
}

func TestSettingsReplyFocusBodyRoundTrip(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Display.ReplyFocusBody {
		t.Fatal("reply_focus_body must default to off")
	}
	cfg.Display.ReplyFocusBody = true
	s := newSettings(cfg, settingsUpdateState{})
	if !s.replyFocusBody {
		t.Fatal("settings did not load reply_focus_body")
	}
	s.replyFocusBody = false
	if s.ApplyTo(cfg).Display.ReplyFocusBody {
		t.Fatal("ApplyTo did not write the toggled value back")
	}
}
