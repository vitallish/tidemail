package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
)

// keyOverrideTargets maps the names accepted in config.toml's [keys] table to
// the bindings they replace. Keys are added here one at a time; every binding
// listed must have its footer hint and palette action follow the binding
// instead of repeating the default key as a literal.
func keyOverrideTargets(k *KeyMap) map[string]*key.Binding {
	return map[string]*key.Binding{
		"AttachFile": &k.AttachFile,
	}
}

// applyKeyOverrides returns base with the [keys] overrides applied, plus one
// message per problem. A problem entry is skipped; it never disables the rest.
// The help text of an overridden binding is kept, so only its keys change.
func applyKeyOverrides(base KeyMap, overrides map[string][]string) (KeyMap, []string) {
	out := base
	targets := keyOverrideTargets(&out)
	supported := make([]string, 0, len(targets))
	for name := range targets {
		supported = append(supported, name)
	}
	sort.Strings(supported)

	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)

	var problems []string
	for _, name := range names {
		binding, ok := targets[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("[keys] %s: unknown name (supported: %s)", name, strings.Join(supported, ", ")))
			continue
		}
		var keys []string
		for _, k := range overrides[name] {
			if k = strings.TrimSpace(k); k != "" {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			problems = append(problems, fmt.Sprintf("[keys] %s: needs at least one key", name))
			continue
		}
		*binding = key.NewBinding(key.WithKeys(keys...), key.WithHelp(hintKeyLabel(keys[0]), binding.Help().Desc))
	}
	return out, problems
}

// composeAttachKeys is the attach binding the compose footer shows. It is a
// package var, set with the model's keys, because the footer is rendered
// without access to the model (see setComposeFilePickerCmd for the pattern).
var composeAttachKeys = DefaultKeys.AttachFile

// setComposeKeys passes the bindings the compose footer displays.
func setComposeKeys(k KeyMap) { composeAttachKeys = k.AttachFile }

// hintKeyLabel writes a key the way the footer hints do: ctrl+s as ^s, other
// keys as given.
func hintKeyLabel(k string) string {
	if rest, ok := strings.CutPrefix(k, "ctrl+"); ok && len(rest) == 1 {
		return "^" + rest
	}
	return k
}

// bindingHintLabel is the hint label for a binding's first key.
func bindingHintLabel(b key.Binding) string {
	keys := b.Keys()
	if len(keys) == 0 {
		return ""
	}
	return hintKeyLabel(keys[0])
}

// applyKeyConfig installs DefaultKeys plus the [keys] overrides from config and
// reports any problem in the status line.
func (m *Model) applyKeyConfig() {
	keys, problems := applyKeyOverrides(DefaultKeys, m.cfg.Keys)
	m.keys = keys
	setComposeKeys(keys)
	if len(problems) > 0 {
		m.setStatus(strings.Join(problems, "; "), true)
	}
}
