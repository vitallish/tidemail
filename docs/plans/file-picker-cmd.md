# External file picker for compose attachments (`file_picker_cmd`)

Branch: `feat/file-picker-cmd`, cut from `main` (v1.9.1+8).

## Why

`Alt+F` in compose opens the built-in directory browser
(`internal/ui/compose.go`, `filePicker`, `openPicker`). It starts in `$HOME`,
picks one file at a time and has no search, preview or bookmarks. Users who
already live in a terminal file manager (yazi, ranger, lf, nnn) want that tool
instead. The pattern is aerc's `file-picker-cmd`: run a command, let it write
the chosen paths to a file, attach what it wrote.

## Behaviour

- New setting `[display] file_picker_cmd` (string, default empty). Placed under
  `[display]` next to `browser`, the only other external-command setting.
- Empty: nothing changes, the built-in picker opens as today.
- Set: `Alt+F` suspends the TUI, runs the command, resumes the TUI, attaches the
  chosen files.
- The command is a shell command line run with `sh -c`. `{chooser}` is replaced
  by the shell-quoted path of a temp file the command must write the chosen
  paths to, one per line. Example for yazi:
  `file_picker_cmd = "yazi --chooser-file={chooser}"`.
- Every line of the chooser file is attached (yazi, ranger and nnn all write
  multiple selections one per line). Blank lines are ignored.
- A command without `{chooser}` is rejected with a status error, because
  nothing could be read back and "no file chosen" would mislead (added during
  implementation, 2026-11-11).
- An empty chooser file (user quit without choosing) is not an error: status
  "no file chosen".
- Failures are shown, never dropped: command not found or non-zero exit with an
  empty file, an unreadable path, a directory in the list. Status line plus the
  existing error styling (`statusMsg` / `isErr`); a path that fails does not
  stop the others from attaching.
- The temp file is removed afterwards.

## Decisions and rejected alternatives

| Choice | Rejected | Why |
|---|---|---|
| `{chooser}` placeholder substituted into a shell line | Fixed argv for yazi only | One mechanism covers yazi, ranger (`--choosefiles={chooser}`), lf, nnn. aerc does the same. |
| `sh -c` | Split on spaces, exec directly | Users need quoting, `$HOME`, flags with `=`. Cost: Unix only; the Windows build leaves the setting unsupported and keeps the built-in picker (documented). |
| Capture stdout | Chooser file | `tea.ExecProcess` hands the real terminal to the child, so stdout cannot be captured; yazi has `--chooser-file` for this. |
| Setting under `[display]` | New `[compose]` section | Matches how `browser` and the `compose_*` options are already stored. |
| `tea.ExecProcess` | PTY embedded in the TUI | Already in bubbletea v1.3.10; the child needs the full terminal. |
| Fall back to the built-in picker if the command fails | Show the error | A silent fallback hides a broken config. The error is shown; the built-in picker stays reachable by clearing the setting. |

## Implementation steps

1. `internal/config`: add `FilePickerCmd` to `DisplayConfig`; include in the
   default config test and the config round-trip.
2. `internal/ui`: pure helpers with explicit arguments, so they test without a
   terminal:
   - `pickerCommandLine(template, chooserPath string) string`
   - `readChooserFile(path string) ([]string, error)`
3. `compose.go`: on `AttachFile` with a command set, create the temp file and
   return `tea.ExecProcess(cmd, callback)`; the callback returns a message
   carrying paths or the error. Handle it in `ComposeModel.Update` and reuse
   the read/append logic from `pickerAttachSelected` (factor out
   `attachPath(path string)`).
4. Reach the setting from compose the way `ComposeVim` does (set from `cfg` in
   `model.go` at startup and on settings save).
5. Tests: template substitution and quoting, chooser-file parsing (blank
   lines, trailing newline, empty), attach with a missing file among valid
   ones, message handling in `ComposeModel`.
6. Docs in the same change (upstream AGENTS.md): `docs/guide.md` (setting plus
   a yazi example and the Unix-only note), in-app help text for `Alt+F`,
   `README.md`/`STATUS.md` if they list the attach flow, `CHANGELOG.md`
   (Unreleased, Added). `keybindings.csv` is unchanged because the key is the
   same; noted here so the omission is deliberate.
7. Before merging to `mine`: `gofmt -l .`, `go build ./...`, `go vet ./...`,
   `go test ./... -race`, `golangci-lint run`.
8. Manual check with the host build against the GreenMail test bed:
   attach one and several files with yazi, quit yazi without choosing, set a
   bad command.

## Open points

- Starting directory for the command: working directory of tidemail (not
  `$HOME` as the built-in picker). Users can put a path in the command line.
  Revisit if it is awkward in practice.
- The contact manager has its own `filePicker` use (import). Out of scope here;
  the same setting could be applied later.

## Addendum 2026-11-11: piecemeal key override for the attach key

Why: the owner cannot use Alt-chords, so `Alt+F` is unusable for them. Keys are
hard-coded (`DefaultKeys`, `internal/ui/keys.go`); no config reads them. A first
attempt rebound `AttachFile` to `Ctrl+O` in code; that broke the command
palette (it sends a synthetic `Alt+F`) and needed a literal `^o` footer hint,
so the change was spread over four places and would live in a `mine:` commit.

Decision (owner agreed): add a `[keys]` table in `config.toml`, a name ->
list-of-keys map, accepting only names in a small registry. The registry starts
with `AttachFile` and grows one key at a time. The default stays `Alt+F`, so
upstream behaviour is unchanged and the owner's `AttachFile = ["ctrl+o"]` is
configuration, not a code fork.

Rejected: overriding all ~100 bindings at once (hints, help and palette
literals would all need to follow the bindings; the editor library's own
`alt+c/alt+r/alt+enter` need separate handling); a `mine:` code commit per key
(permanent merge-conflict surface with upstream).

Behaviour:
- Unknown or misspelled names, and empty lists, are reported in the status
  line at startup and on settings save; they are never silently ignored.
- The compose footer hint and the command-palette action follow the binding:
  the hint reads it (`ctrl+o` is shown as `^o`, like the other hints), the
  palette calls the attach action directly instead of faking a keypress.
- No conflict detection for now (one key); documented.
- Applied after `DefaultKeys` is assigned and again after a settings save.

