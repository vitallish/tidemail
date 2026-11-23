# Copy over SSH (#29)

## Goal
Copying inside an SSH session should reach the user's own clipboard, say what it
did, and show that the session is remote.

## Findings
- `internal/clipboard` already had an OSC 52 fallback (go-osc52), used only when no
  local tool (`wl-copy`, `xclip`, `xsel`, `pbcopy`) worked. Over SSH with X11
  forwarding or a stale `DISPLAY`, a tool can succeed and fill the remote
  clipboard while the status says "copied".
- The escape was written to stderr, not to the controlling terminal, with no
  tmux/screen wrapping and no size limit.
- Bubble Tea v1.3.10 has no clipboard command; `go-osc52` is already a dependency.

## Design
- `OverSSH()` from `SSH_CONNECTION`/`SSH_TTY`/`SSH_CLIENT`. Mode `auto` (default):
  terminal escape first when over SSH, local tools first otherwise. `[display]
  clipboard = "osc52"|"local"` overrides (config file only), for a tmux server
  started outside SSH.
- Escape goes to `/dev/tty` (stderr only as fallback). Under tmux send the plain
  escape and the DCS-wrapped one; screen gets its wrapper. Refuse above 100 KB.
- `CopyVia` returns the method; `ClipboardCopiedMsg.Method`; the status says
  "sent to your terminal's clipboard" when only the terminal was asked.
- Status-bar `ssh` badge after the outbox part, so truncation keeps it.
- Ctrl+V read error over SSH points at the terminal's own paste.

## Not done
Files and images: OSC 52 is text only. Documented alternatives (save with `Ctrl+D`
and `scp`; inline image display). Kitty's OSC 5522 (MIME types) is future work.
A Settings row for `clipboard` (config file only, like `compose_table_css`).

## Tests
Order of mechanisms, fallback when the terminal write fails, tmux double escape,
size limit, mode override, error text, badge, status wording, config round trip.
