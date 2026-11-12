# Preview the draft in the browser before sending (vitallish/tidemail#10)

Branch: `feat/compose-preview`, cut from `main` (v1.9.1+8) on 2026-11-12. Independent of
`feat/table-style`; it previews whatever the converter produces, styled or not.

## Why

Markdown in compose is sent as an HTML part (tables, lists, links, signature). Today the
only way to see how that renders is to send it. A key in compose should open the HTML
that would be sent in the browser, the way the reader's "view message in browser"
(Ctrl+B in the reader, `internal/ui/email_browser.go`) opens a received message.

## Behaviour

- Key `F4` in compose, plus a command-palette entry "Preview draft in browser", plus a
  footer hint. Works from any compose field.
- The HTML comes from `composeBody`, the step inside `mailapp.PrepareCompose` that turns
  the draft body and signature into the HTML part, exposed as `mailapp.ComposeHTML`.
  Sending calls the same function, so the preview contains exactly the HTML part that
  would go out, including table styling, the signature and the marker rules. Recipient and
  From validation (the rest of `PrepareCompose`) is deliberately not run: you can preview
  before addressing the message. If the body itself is invalid (two signature markers),
  the error shows in the status line as it does on send.
- The file is a minimal document around that HTML: `<!doctype html>`, UTF-8 meta, a
  title `Preview (not sent): <subject>`, a Content-Security-Policy that forbids all
  network access (`default-src 'none'; style-src 'unsafe-inline'; img-src data:`), then
  the HTML part verbatim. The part itself is byte-for-byte what is sent; the wrapper is
  the only difference and is documented.
- Written to the existing private export directory (`tidemail-browser-<uid>`, mode 0700),
  pruned by age like received-message exports, opened with `[display] browser` or the
  system default via `browser.OpenLocalFile`.
- Does not save the draft, does not send, does not modify the draft text.
- Not previewed: the plain-text part, attachments, PGP, and inline images (draft images
  are not supported yet: issues #6 and #7). Stated in the docs.
- A draft with no body still opens (an empty page), so the key never fails silently.

## Decisions and rejected alternatives

| Choice | Rejected | Why |
|---|---|---|
| `F4` | `Ctrl+B` (reader's key); Alt chords | In compose, header fields are bubbles text inputs where Ctrl+B moves the cursor left; Alt chords are unusable for the owner (2026-11-11). F-keys already carry compose actions (F6 templates, F7 spell, F8/F9 PGP) and are not used in tmux or the app for anything else. Rebindable later through `[keys]` (rule 6 in CLAUDE.md). |
| Share `composeBody` (via `mailapp.ComposeHTML`) with sending | Render Markdown separately for the preview; call all of `PrepareCompose` | A second path would drift from what is sent: the exact bug class of #2. All of `PrepareCompose` also demands a recipient and a valid From, which would block previewing an unaddressed draft. |
| Minimal wrapper with CSP | Reuse `sanitizeEmailHTML` | That function sanitises untrusted received mail (strips remote content, scripts). The draft HTML is generated from the user's own Markdown; raw HTML typed into a draft would still reach the sent part. The preview should show that faithfully, but must not load or run anything, so the CSP blocks network and scripts instead of rewriting markup. |
| Export dir shared with received mail | A new temp directory | Reuses the owner-only 0700 check and age pruning already there. |
| Package var for the browser command (`setComposeBrowser`) | Pass config through `NewCompose` | `NewCompose` has several callers and the repo uses this pattern for compose settings (`setEditorVimMode`). |

## Implementation steps

1. `email_browser.go`: split the write-and-open tail of `openEmailHTML` into
   `writeAndOpenHTML(html, configured string) error` with no behaviour change; add
   `previewDocument(subject, htmlPart string) string` (pure, tested) and
   `openDraftPreview(...)`.
2. `compose.go`: `PreviewDraft` binding (`f4`), key case, `draftPreviewMsg` handling in
   `ComposeModel.Update` (status "Preview opened in browser" or the error), footer hint
   (low priority so narrow footers drop it first), palette entry in `commands.go`.
3. `model.go`: inject the browser command at startup and after a settings save.
4. Tests: `previewDocument` wraps the HTML verbatim, has the CSP and escapes the subject
   in the title; the preview HTML equals `PrepareCompose(...).HTMLBody` for a draft with a
   table and signature, and still works with no recipient; an invalid draft (two markers) reports the error and opens
   nothing; the key and the palette entry call the same action; footer-hint tests updated.
5. Docs in the same change: `docs/guide.md` (Writing mail: what it shows, what it does not,
   the key), in-app help text, `keybindings.csv` row, README/STATUS if they list compose
   keys, `CHANGELOG.md`.
6. Before merge to `mine`: `gofmt -l .`, `go build ./...`, `go vet ./...`,
   `go test ./... -race`, `golangci-lint run`.
7. Manual check against the GreenMail test bed and with a table message: the preview in
   the browser matches the Gmail rendering from the table-style check.

## Open points

- The browser tab is a snapshot: edit the draft and press F4 again for a fresh one.
- Whether the preview should also show To/Subject as a header block. Left out: the point
  is the body HTML, and extra visible content would break "byte for byte".
