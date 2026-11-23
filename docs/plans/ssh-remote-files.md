# Images and attachments when tidemail runs over ssh (plan)

Branch: `feat/ssh-remote-files` (plan only so far), 2026-11-23. Owner question: can pasted
inline images and "open attachment" work when tidemail runs on a remote host and the terminal
is on the owner's laptop (kitty, inside tmux)? Follows #29 (text copy over ssh, `feat/ssh-clipboard`).

## Short answer

Yes, both, and neither is huge. The hard part is not the code but that the thing you want
(your laptop's clipboard, your laptop's PDF viewer) is on the other side of the tty. There are
three ways across; the plan takes the cheapest first and keeps the largest for later.

## The two problems

1. **Paste an image.** `clipimage` runs `wl-paste`/`xclip` on the machine tidemail runs on. Over
   ssh that is the *remote* clipboard (and usually has no display), so it fails with "no Wayland or
   X11 session". Text paste already works (the terminal pastes into the tty); an image never travels
   through a terminal paste.
2. **Open an attachment.** `mailapp.OpenAttachment` writes the file to the remote temp dir and
   runs `xdg-open` there. Nothing opens on the laptop.

## Ways across (what exists, what each costs)

| Route | How | Verified here | Needs | Weak spot |
|---|---|---|---|---|
| A. kitty's own protocols | paste: `kitten clipboard -g out.png` (OSC 5522); open: `kitten transfer` copies the file to the laptop | `kitten` 0.48.2 present; both subcommands exist and document exactly these uses | kitty on the laptop, `kitten` on the remote (the kitty ssh kitten installs it) | kitty only. tmux: needs `allow-passthrough on`, and the reply to a clipboard read has to come back through tmux: **unverified**, must be tried. Clipboard read shows a permission popup (kitty `clipboard_control`). Cannot launch a viewer on the laptop, only deliver the file |
| B. A small bridge | a program on the laptop serves "read clipboard image", "receive file and open it" over a unix socket that `ssh -R` forwards to the remote | no | a local helper (`tidemail bridge`, a flag of the same binary) and one `RemoteForward` line in `~/.ssh/config` | most to build and to secure (the remote can ask the laptop to open files). Works in any terminal and under tmux, and also covers opening links and writing images to the clipboard |
| C. Do-it-by-hand recipes | `attachment_open_cmd` already runs any command with `{file}`: `scp`/`kitten transfer {file} ...` then open locally by hand; paste: save the image on the laptop, attach with the file picker | n/a | nothing | not a feature; no "paste" |

## Recommended plan, in steps (each its own commit, stop after any step)

**Step 0 (docs only): recipes (route C).** Guide section "Images and attachments over ssh":
`attachment_open_cmd = "kitten transfer {file} ~/Downloads/"` for kitty users, `scp` otherwise, and how
to attach an image from the laptop (`kitten transfer --direction=upload`, then Alt+F). Needs no code;
tells the owner today what works.

**Step 1: paste an image over ssh through kitty (route A).**
- `clipimage.Env.pick` gains a third backend, used when `clipboard.OverSSH()` (or setting
  `clipboard = osc52`) and `kitten` is on PATH: list types with `kitten clipboard -g -m . /dev/stdout`,
  read with `kitten clipboard -g <tmpfile.png>`. Same 5 s timeout, 20 MB cap, same PNG/JPEG/GIF rules.
- Smart Ctrl+V and the palette item need no change: they already go through `clipimage`.
- Error text when it cannot work says why (not kitty, no `kitten`, tmux passthrough off, popup refused).
- Tests with the existing fake `Env` (list output, read output, missing kitten, timeout).
- **Gate before writing code: try it by hand** inside the owner's ssh + tmux + kitty, with
  `kitten clipboard -g /tmp/x.png`. If it hangs or returns nothing under tmux, route A for paste is
  dead there and we go straight to step 3.

**Step 2: open an attachment over ssh (route A).**
- Setting `attachment_open_mode = "local" | "transfer"` (default `local`; `auto` picks `transfer`
  when over ssh and `kitten` exists). `transfer` runs `kitten transfer <copy> <dest dir>` into
  a configurable folder (default the laptop's Downloads) and reports "sent to your computer: <name>".
  It cannot open the file there; that is a kitty limit (decided against remote-control, which needs
  `allow_remote_control` and a forwarded socket, i.e. the bridge by another name).
- CLI: `messages open --attachment N` follows the same setting, so it is testable without the UI.
- If the owner wants it to *open*, not just arrive, that is step 3.

**Step 3 (only if 1 or 2 fall short): the bridge (route B).**
- `tidemail bridge` runs on the laptop: listens on `$XDG_RUNTIME_DIR/tidemail-bridge.sock` (0600),
  verbs `clip-types`, `clip-get <mime>`, `open <name>` (bytes follow), `copy <mime>` (bytes follow).
- The remote finds it through the forwarded socket (`RemoteForward` line documented, path in
  `[display] bridge_socket`). tidemail uses it when present; no bridge, behaviour as today.
- Safety, decided now so it is not retrofitted: the bridge only opens files that arrive as an
  *attachment of a cached message* (name sanitised, written to a private dir, `0600`, no execute
  bit, extension denylist for launchers such as `.desktop`, `.sh`, `.AppImage`), opens through the
  platform opener, asks for confirmation the first time per session (or per file when the setting is
  `confirm`), caps sizes (20 MB), and never lets the remote choose a path or a command.
  The socket is the trust boundary: anything on the remote that can reach it can ask, which is why
  it is per-user, `0600`, and why the open verb is the only one that runs a program.
- Largest step: roughly the size of `feat/ssh-clipboard` plus `feat/open-attachment` combined,
  plus a protocol and its tests; do it last and only if routes A and C are not enough.

## Decisions and rejected alternatives

| Choice | Rejected | Why |
|---|---|---|
| Docs first, then kitty protocols, bridge last | Build the bridge now | Routes A and C cost a fraction and may be enough; the bridge adds a security surface (remote can make the laptop open files). |
| Shell out to `kitten` | Speak OSC 5522 / 5113 ourselves | Escape-sequence replies need raw stdin handling inside Bubble Tea, tmux wrapping, chunking and the permission handshake; `kitten` already does all of it. Same reason `clipimage` shells out to `wl-paste`. |
| Transfer, not open, in step 2 | Remote-control to launch a viewer | Remote control needs a forwarded socket and `allow_remote_control`; that is the bridge's job. |
| Opt-in setting, default unchanged | Switch behaviour automatically | A user on a local session must see no change; `auto` only acts over ssh. |

## Verification (manual, the owner's setup)

kitty on the laptop, tmux on the remote, with and without tmux: copy an image to the laptop clipboard, Ctrl+V in compose;
open an attachment from a message; check the permission popup, the status text and that nothing
changes in a local session.

## Not planned

Writing images or files *to* the laptop clipboard (OSC 52 is text only; `kitten clipboard` can, with the same tmux caveat; fold into step 1 if the gate passes). macOS/Windows laptops running the bridge.
