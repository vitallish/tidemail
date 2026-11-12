package mailapp

import (
	"errors"
	"fmt"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
)

// ErrNoAccountConfig reports an account row with no reachable [[account]] block
// in config.toml. Every sync path must stop on it. Syncing anyway used to mean
// dialing a zero-value AccountConfig — empty host, port 0 — which surfaced to
// users as the baffling "dial tcp :0: connect: connection refused".
var ErrNoAccountConfig = errors.New("no account settings found for this account — open Accounts and re-enter its server details")

// AccountConfigFor resolves the config block that owns an account row, joining
// on the stable config ID. The name fallback covers rows the config_id
// migration left blank, and it only matches when exactly one config block
// claims that name — an ambiguous name is an unresolved account, not a guess.
func AccountConfigFor(configs []config.AccountConfig, acc db.Account) (config.AccountConfig, error) {
	if acc.ConfigID != "" {
		for _, acfg := range configs {
			if acfg.ID == acc.ConfigID {
				return acfg, nil
			}
		}
		return config.AccountConfig{}, fmt.Errorf("%w (%q)", ErrNoAccountConfig, acc.Name)
	}
	var found config.AccountConfig
	matches := 0
	for _, acfg := range configs {
		if acfg.Name == acc.Name {
			found = acfg
			matches++
		}
	}
	if matches != 1 {
		return config.AccountConfig{}, fmt.Errorf("%w (%q)", ErrNoAccountConfig, acc.Name)
	}
	return found, nil
}
