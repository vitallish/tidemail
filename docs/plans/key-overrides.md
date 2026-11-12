# Piecemeal key overrides (`[keys]`), starting with the attach key

Branch: `feat/key-overrides`, stacked on `feat/file-picker-cmd` (the palette
action calls `startAttachPick`, added there). Merge that branch first.

## Why

Keys are hard-coded (`DefaultKeys`, `internal/ui/keys.go`); no config reads
them. The owner cannot use Alt-chords, so `Alt+F` (attach) is unusable for
them. A first attempt rebound `AttachFile` to `Ctrl+O` in code; that broke the
command palette (it sent a synthetic `Alt+F`) and needed a literal `^o` footer
hint, so the change was spread over four places and would have been a personal
code fork to carry across every upstream merge.

## Decision (owner agreed, 2026-11-11)

A `[keys]` table in `config.toml`: key name -> list of keys, accepting only
names in a small registry (`keyOverrideTargets`). The registry starts with
`AttachFile` and grows one key at a time. The default stays `Alt+F`, so
upstream behaviour is unchanged; the owner's `AttachFile = ["ctrl+o"]` is
configuration, not a code fork.

Rejected:
- Overriding all ~100 bindings at once: hints, help and palette literals would
  all have to follow the bindings, and the editor library's own
  `alt+c`/`alt+r`/`alt+enter` need separate handling.
- A `mine:` code commit per key: a permanent merge-conflict surface with
  upstream.

## Behaviour

- Unknown or misspelled names, and empty lists, are reported in the status
  line at startup and after a Settings save; they are never silently ignored.
  The bad entry is skipped; other entries still apply.
- The overridden binding keeps its help text; only its keys change. The first
  key is the one shown in hints.
- The compose footer gains an `attach` hint that reads the binding (`ctrl+o`
  shows as `^o`, like the other hints). It has the lowest priority, so it is
  the first hint a narrow window drops.
- The command-palette "attach" action calls the attach function directly
  instead of sending a fake keypress, so it works whatever the key is.
- No conflict detection yet (one key); documented in the guide.

## Adding the next key

1. Add the name to `keyOverrideTargets`.
2. Make every footer hint, palette action and help line for that binding read
   the binding instead of repeating the default key.
3. Add a test that the old default stops working and the new key works.
4. Add the name and default to the table in `docs/guide.md`.

## Not done / known limits

- `alt+m` (maximize), `alt+p` (previous link, password dialog), and the Find
  keys `alt+enter`, `alt+c`, `alt+r` have no non-Alt alternative. The Find keys
  live in the ripple editor library, so they cannot be remapped from `keys.go`
  alone.
- Literal key strings remain in `compose_hints.go`, `pgp_compose.go`,
  `help.go` and `account_manager.go`; each must be converted when its key
  becomes overridable.
