package ui

import (
	"github.com/allisonhere/tidemail/internal/config"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func markerModel(t *testing.T, on bool) Model {
	t.Helper()
	m := expandableModel(t)
	m.cfg.Display.ThreadedConversations = false
	m.cfg.Display.ShowAttachmentMarker = on
	m.messages[1].AttachmentMarker = true // Subject2
	m.applyFilter()
	return m
}

func TestAttachmentMarkerIsAChipOnlyOnMessagesWithOneAndKeepsColumns(t *testing.T) {
	m := markerModel(t, true)
	with, without := ansi.Strip(rowLine(t, m, "Subject2")), ansi.Strip(rowLine(t, m, "Subject3"))
	if !strings.Contains(with, "ATTACH") || strings.Contains(without, "ATTACH") {
		t.Fatalf("chip: with=%q without=%q", with, without)
	}
	if rowTextColumn(t, with, "Subject2") != rowTextColumn(t, without, "Subject3") {
		t.Errorf("the marker moved the subject column:\n%q\n%q", with, without)
	}
	if ansi.StringWidth(with) != ansi.StringWidth(without) {
		t.Errorf("rows differ in width: %d vs %d", ansi.StringWidth(with), ansi.StringWidth(without))
	}
}

func TestAttachmentMarkerIsOffByDefaultAndPlainUIUsesBrackets(t *testing.T) {
	off := markerModel(t, false)
	if strings.Contains(ansi.Strip(rowLine(t, off, "Subject2")), "ATTACH") {
		t.Error("marker shown with the setting off")
	}
	plain := markerModel(t, true)
	plain.setStyles(BuildStyles(VT52, "compact", "square"))
	if row := ansi.Strip(rowLine(t, plain, "Subject2")); !strings.Contains(row, "[attach]") {
		t.Errorf("PlainUI marker should be the bracketed word: %q", row)
	}
}

func TestAttachmentMarkerSettingIsOffByDefaultAndSaves(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Display.ShowAttachmentMarker {
		t.Fatal("the marker must start off")
	}
	s := newSettings(cfg, settingsUpdateState{})
	s.setActiveSection(ssDisplay)
	s.setFocusedPane(settingsPaneDetail)
	s.setFocusedField(sfShowAttachmentMarker)
	on, _, _ := s.Update(tea.KeyMsg{Type: tea.KeySpace}, DefaultKeys)
	if !on.ApplyTo(cfg).Display.ShowAttachmentMarker {
		t.Fatal("Space must turn it on")
	}
	if !newSettings(on.ApplyTo(cfg), settingsUpdateState{}).ApplyTo(config.DefaultConfig()).Display.ShowAttachmentMarker {
		t.Fatal("an enabled setting must be read from config")
	}
}
