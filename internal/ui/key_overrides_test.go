package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/tidemail/internal/config"
)

func resetComposeKeys(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { setComposeKeys(DefaultKeys) })
}

func TestApplyKeyOverrides(t *testing.T) {
	t.Run("no overrides leaves the defaults", func(t *testing.T) {
		got, problems := applyKeyOverrides(DefaultKeys, nil)
		if len(problems) != 0 || strings.Join(got.AttachFile.Keys(), ",") != "alt+f" {
			t.Fatalf("keys=%v problems=%v", got.AttachFile.Keys(), problems)
		}
	})

	t.Run("replaces the keys and keeps the help text", func(t *testing.T) {
		got, problems := applyKeyOverrides(DefaultKeys, map[string][]string{"AttachFile": {"ctrl+o", "f4"}})
		if len(problems) != 0 {
			t.Fatalf("problems = %v", problems)
		}
		if strings.Join(got.AttachFile.Keys(), ",") != "ctrl+o,f4" {
			t.Fatalf("keys = %v", got.AttachFile.Keys())
		}
		if h := got.AttachFile.Help(); h.Key != "^o" || h.Desc != "attach file" {
			t.Fatalf("help = %+v", h)
		}
		if strings.Join(DefaultKeys.AttachFile.Keys(), ",") != "alt+f" {
			t.Fatal("overriding must not change DefaultKeys")
		}
	})

	t.Run("unknown name is reported and the rest still apply", func(t *testing.T) {
		got, problems := applyKeyOverrides(DefaultKeys, map[string][]string{
			"AttachFil":  {"ctrl+o"},
			"AttachFile": {"ctrl+t"},
		})
		if len(problems) != 1 || !strings.Contains(problems[0], "AttachFil") || !strings.Contains(problems[0], "AttachFile") {
			t.Fatalf("problems = %v", problems)
		}
		if strings.Join(got.AttachFile.Keys(), ",") != "ctrl+t" {
			t.Fatalf("keys = %v", got.AttachFile.Keys())
		}
	})

	t.Run("empty list keeps the default and is reported", func(t *testing.T) {
		for _, keys := range [][]string{{}, {""}, {"  "}} {
			got, problems := applyKeyOverrides(DefaultKeys, map[string][]string{"AttachFile": keys})
			if len(problems) != 1 || strings.Join(got.AttachFile.Keys(), ",") != "alt+f" {
				t.Fatalf("%q: keys=%v problems=%v", keys, got.AttachFile.Keys(), problems)
			}
		}
	})
}

func TestHintKeyLabel(t *testing.T) {
	for in, want := range map[string]string{"ctrl+o": "^o", "alt+f": "alt+f", "f7": "f7", "ctrl+enter": "ctrl+enter"} {
		if got := hintKeyLabel(in); got != want {
			t.Errorf("hintKeyLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestModelAppliesKeyOverridesFromConfig(t *testing.T) {
	resetComposeKeys(t)
	t.Cleanup(func() { setComposeFilePickerCmd("") })
	setComposeFilePickerCmd("")
	cfg := config.Config{
		Accounts: []config.AccountConfig{{Name: "Personal", User: "me@example.com"}},
		Display:  config.DefaultConfig().Display,
		Keys:     map[string][]string{"AttachFile": {"ctrl+o"}},
	}
	m := NewModel(nil, cfg, "dev", false)
	m.compose = NewCompose(cfg.Accounts[0], cfg.Accounts, nil)

	// The configured key opens the picker; the old default no longer does.
	alt, _ := m.handleCompose(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f"), Alt: true})
	if alt.(Model).compose.picker.active {
		t.Fatal("alt+f must not attach once AttachFile is overridden")
	}
	got, _ := m.handleCompose(tea.KeyMsg{Type: tea.KeyCtrlO})
	if !got.(Model).compose.picker.active {
		t.Fatal("ctrl+o should open the attach picker")
	}

	// The footer shows the configured key.
	chrome := newManagerChrome(110, CatppuccinMocha, false)
	hints := plainComposeHintText(m.compose.composeHints(110, chrome))
	if !strings.Contains(hints, "^o attach") || strings.Contains(hints, "alt+f attach") {
		t.Fatalf("footer = %q", hints)
	}

	// The palette runs the action regardless of the key.
	pal, _ := m.executeCommand("compose-attach")
	if !pal.(Model).compose.picker.active {
		t.Fatal("palette attach must open the picker")
	}
}

func TestModelReportsBadKeyOverride(t *testing.T) {
	resetComposeKeys(t)
	cfg := config.DefaultConfig()
	cfg.Keys = map[string][]string{"Nope": {"ctrl+o"}}
	m := NewModel(nil, cfg, "dev", false)
	if !m.statusErr || !strings.Contains(m.statusMsg, "Nope") {
		t.Fatalf("status = %q (err=%v)", m.statusMsg, m.statusErr)
	}
}
