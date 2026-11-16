# Paste an image from the clipboard (Ctrl+V in compose)

Branch: `feat/clipboard-image`, stacked on `feat/inline-images` (2026-11-12). Owner request, with
the choices below approved the same day.

## Why

Inline images (feat/inline-images) need the image attached first and referenced by file name. A
screenshot on the clipboard has no file, so the user would save it, attach it and type the
reference. Pasting it should do all three.

## Why the terminal cannot do it

A terminal paste carries text only: with an image on the clipboard, Ctrl+V (and the terminal's
own paste) send nothing. Today `Ctrl+V` in compose reads text through `atotto/clipboard`. So
tidemail must ask the system clipboard for the image itself, like `file_picker_cmd` shells out
to a file manager. A terminal clipboard protocol (kitty OSC 5522) was rejected: terminal
specific, asynchronous, and fragile under tmux.

## Behaviour (approved choices)

1. **Ctrl+V is smart.** It looks at what the clipboard holds. An image and no text: paste the
   image. Anything else (text, or text plus an image, as when copying from a web page): paste
   text exactly as today. If the clipboard cannot be inspected (no tool, no display), the old text
   paste runs, so nothing gets worse. The command palette gets **Paste image from clipboard**
   to force the image case, and its error says why when there is none.
2. **Attach and reference.** The image is added to the draft as an attachment and `![](image-1.png)`
   is written at the cursor in the body (focus moves to the body if it was in a header field; in
   Vim mode it goes in as inserted text, not as keystrokes). It is then an inline image by the
   normal rules of feat/inline-images, and the live preview picks it up.
3. **Name.** `image-1.png`, `image-2.png`, ... skipping any name already attached (case-
   insensitive). A JPEG or GIF on the clipboard keeps its type (`.jpg`, `.gif`). The same
   bytes pasted again reuse the existing attachment and just insert another reference.
4. **Platforms.** Linux: Wayland through `wl-paste` (when `WAYLAND_DISPLAY` is set), X11 through
   `xclip` (when `DISPLAY` is set). macOS and Windows report "image paste is not supported on
   this platform yet": they cannot be tested here and guesses are not shipped.
5. **Types.** PNG, JPEG and GIF are taken as they are. BMP, TIFF and WebP are decoded and
   re-encoded as PNG (the `x/image` decoders are already a dependency). Preference order when
   several are offered: png, jpeg, gif, webp, bmp, tiff.
6. **Limits and errors.** 20 MB (the inline-image limit), a 5 s timeout, and every failure shows
   in the status line: no tool installed, no display, no image, too large, not decodable. The
   clipboard is read only when the key is pressed, never polled.

## Decisions and rejected alternatives

| Choice | Rejected | Why |
|---|---|---|
| Shell out to `wl-paste` / `xclip` | kitty OSC 5522; a cgo or X11/Wayland library | Works in any terminal and under tmux; no new dependency; the same approach as the file picker. |
| Smart `Ctrl+V` + palette entry | A separate key | Ctrl+V is where the hand goes; the palette covers the forced case. No new binding to keep free of Alt. |
| Text wins when text is also present | Image always wins | Copying from a page or a spreadsheet puts text and an image on the clipboard; text is almost always meant. |
| Insert `![](name)` at the cursor | Attach only | A bare attachment is not inline; with no file name there is nothing for the user to type. |
| `image-N.png` | Timestamped names | Short in the editor and in the Markdown; collisions are handled. |
| Linux only for now | Guess at macOS/Windows | Untestable here. The package takes a command runner and a platform, so adding them later is local. |

## Notes for the docs

tmux: `update-environment` in the owner's `tmux.conf` lists only `DISPLAY` and `KRB5CCNAME`, so
after logging out and in, an old tmux server keeps a stale `WAYLAND_DISPLAY` and `wl-paste`
fails. Fix: `set -ga update-environment WAYLAND_DISPLAY`. Over SSH the clipboard read is the
remote machine's. Both go in the guide.

## Implementation steps

1. `internal/clipimage`: `Probe` (what the clipboard holds), `Read` (the image, normalised),
   injectable runner and environment; table tests with a fake runner (Wayland, X11, no tool,
   no display, image only, text and image, preference order, BMP and TIFF conversion, size limit,
   timeout, unsupported platform).
2. `internal/ui`: `ClipboardImageMsg`; `pasteClipboardCmd` (probe, then image or text); the
   palette command; a `pasteImage` method on the composer (validate, dedupe, name, attach,
   insert); the model handler; dirty flag and status.
3. Tests: Ctrl+V with an image only, with text and image, with an unreadable clipboard; the
   palette command with and without an image; naming and dedupe; focus from a header field;
   Vim mode; the live preview following a pasted image.
4. Docs: guide (a "Pasting an image" paragraph in "Images in messages", the tmux note), help
   text, README, CHANGELOG, `keybindings.csv` (the PasteText description).
5. Before merge to `mine`: `gofmt -l .`, `go build ./...`, `go vet ./...`, `go test ./... -race`,
   `golangci-lint run`.
6. Manual: copy a real screenshot (`wl-copy` an image, or the desktop screenshot tool) and press
   Ctrl+V in compose with the live preview on. Ask the owner before overwriting their clipboard
   for any test of my own.

## Open points

- Alt text is empty (`![]()`): a recipient who blocks images sees nothing, and the plain-text
  part shows `[image]`. The owner can type a description between the brackets.
- A very large clipboard image is processed by the normal inline-image rules at send time
  (shown at most 600 px, embedded at 2x).
