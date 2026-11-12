# Inline images in composed mail (vitallish/tidemail#6 preview, #7 send)

Branch: `feat/inline-images`, cut from `main` (v1.9.1+8) on 2026-11-12. Previews through
`feat/compose-preview`'s browser preview (merged in `mine`); does not depend on it to build.

## Goal (owner, 2026-11-12)

The preview must show what the recipient sees. Ideally that is checked against a real render
(a screenshot), not just string tests. The preview may load remote images. Image sizing
should look reasonable at the receiving end; the owner is open to ideas.

## Why now

`![alt](/some/path.png)` today becomes `<img src="/some/path.png">` in the sent HTML (a dead
path for the recipient), `file://` becomes `src=""`, raw `<img>` is dropped by goldmark, and
the plain-text part carries the Markdown source, so it also shows the sender's local path.
Only `data:` renders in the preview and Gmail does not show `data:` images. Checked on
2026-11-12 with `harness/test-images/inline-images-test.md` in the vault project.

## Behaviour

- **Syntax:** standard `![alt](source "title")`. **No file paths** (owner, 2026-11-12). The
  image is attached to the message first, with the existing attach action (`Alt+F`, or the
  `file_picker_cmd` picker), and referenced by its file name. Sources:
  - a bare file name, e.g. `![logo](logo.png)`: matched (case-insensitively) against the
    message's attachments. A referenced image is sent inline instead of as an attachment;
    attached files that are not referenced stay ordinary attachments. A name that matches no
    attachment, or an attachment that is not an image, is an error that says which;
  - `data:image/...;base64,` URI: decoded and treated like an attached image (Gmail does not
    render `data:` images, so it is converted to a CID part too);
  - `http://` / `https://`: left as a remote image, never fetched or embedded by TideMail;
  - anything containing a path (`/`, `~`, `./`, `../`), `file:`, `cid:`, other schemes or an
    empty source: an error that says to attach the file and use its name.
- **On send:** every referenced attachment (and `data:` image) becomes an inline MIME part
  referenced by `cid:`; the same image used twice is one part. Structure: `multipart/alternative[text/plain,
  multipart/related[text/html, image...]]`, wrapped in `multipart/mixed` only when there are
  attachments. Parts carry `Content-ID`, `Content-Disposition: inline` and a filename.
- **Sizing (see decision below):** the image is shown at most 600 CSS px wide (the usual
  email column), never enlarged. Pixels are embedded at up to 2x that width for sharp
  high-DPI display. HTML gets `width` and `height` attributes (Outlook relies on them) plus
  `style="max-width:100%;height:auto"` (so narrow clients scale it down). Optional per-image
  override: `![alt](path){width=300}` or `{width=50%}` (pandoc's attribute syntax).
- **Metadata and privacy:** JPEGs are always re-encoded (quality 85): EXIF orientation is
  applied first, and EXIF including GPS position is dropped, since phone photos carry the
  sender's location. PNG is re-encoded only when it must be shrunk; GIF is sent untouched
  (animation survives, size limit enforced, display width set by the attribute).
- **Plain-text part:** an attached or `data:` image is replaced by `[image: alt]` (or
  `[image]`), so no file name reference syntax or base64 reaches the recipient. Remote images
  keep their Markdown.
- **Preview:** shows the exact bytes that will be sent (resized and re-encoded), inlined as
  `data:` URIs, plus remote images loaded live. The preview policy's `img-src` becomes
  `data: https: http:`; scripts and everything else stay blocked. Loading a remote image from
  the preview sends your IP to that host, which is the sender's own choice of image; the guide
  says so.
- **Errors, never silent:** a name with no matching attachment, a path, an oversize
  (> 20 MB each, > 20 MB total after processing) or unsupported image (SVG, WebP, HEIC, ...)
  blocks the send with a message naming it, like the signature-marker errors. The preview still renders, with the failing image
  as a broken-image box, and the status line names it.
- **Drafts and the Sent copy:** the draft stores the Markdown as typed and the attached bytes
  (as it already does for attachments), so a draft is self-contained: moving or deleting the
  original file does not break it. The Sent copy is the transmitted message with its `cid:` parts.
  PGP: protection already wraps the built entity, so images are inside the signed/encrypted
  content (tested).

## Sizing: options considered

| Option | Pros | Cons |
|---|---|---|
| A. Automatic fit: max 600 CSS px, embed 2x, never enlarge | Nothing to learn; phone photos become sane (4000 px -> 1200 px, hundreds of KB, not MB); small logos stay small | No control: a 900 px wide figure is capped at 600 |
| B. A plus `{width=N}` / `{width=N%}` override | Control where wanted; pandoc syntax the owner may already know; falls back to A | Goldmark does not parse it (prints it as text), so a small parser of our own is needed |
| C. Size in the title (`"t =300x"`) | No parser change | A hack: overloads the hover text and is not any standard |
| D. A config for the default width | Tunable | A fourth moving part before anyone has asked |

Recommendation: A + B. D can follow if 600 turns out to be wrong. Embedding at 2x (not 1x)
because phone and laptop screens are mostly high-DPI; the cost is about 4x the pixels of a
1x embed, bounded by the 20 MB cap and the 2000 px ceiling.

## Rejected alternatives

| Choice | Rejected | Why |
|---|---|---|
| Reference attached files by name | Absolute / `~/` file paths in the Markdown | Owner's call (2026-11-12). The draft stays self-contained (the bytes live in the draft); no path in the text or the sent mail; pasted or received Markdown cannot make TideMail read an arbitrary local file; reuses the attach flow. Cost: attach first. |
| Convert attached images to CID at send | Embed as `data:` URIs | Gmail and others do not render `data:` images; CID is the interoperable way (#7). |
| Always embed, never link | Fetch and embed remote images | Fetching on send leaks nothing new but makes sending slow and brittle; the recipient's client already handles remote images. Revisit if asked. |
| Preview from the same processed bytes | Preview the original files | The point is to see what is sent: the resized, re-encoded result and its real size. |
| Strip metadata always for JPEG | Keep EXIF | GPS location in a work email attachment is an avoidable leak; orientation must be baked in once EXIF is dropped. |
| One converter for both compose paths | | Already true (`smtp.NewMarkdown`, on `fix/gfm-tables`); this branch adds the image handling to that one place. NOTE: this branch is cut from `main`, which still has two converters; see "Dependencies". |

## Dependencies

`fix/gfm-tables` introduced `smtp.NewMarkdown` (one converter for both compose paths). This
feature needs that single converter, so it is stacked on `fix/gfm-tables`: `feat/inline-images`
is rebased onto it before implementation. Merge order in `mine`: after `fix/gfm-tables`,
before `feat/key-overrides`.

## Checking against a real render

String tests cannot say whether it looks right. For this work I check the preview with
headless Chromium screenshots (static preview file, server preview, and the sent HTML saved
from a message) and look at them; the sample set is `harness/test-images/` (logo 240x80, bars
220x180, banner 600x120; add a large phone-size JPEG with EXIF orientation and GPS). What
this cannot show is how Gmail or Outlook restyle the HTML: that stays a manual send test.
A later idea, not in scope: a "client simulation" mode that strips what Gmail strips.

## Implementation steps

1. Rebase onto `fix/gfm-tables`.
2. `internal/mailapp` (or a small `internal/inlineimg`): find the images in the converted
   HTML/AST, match names to the draft's attachments, validate, process (decode, EXIF orientation, resize,
   re-encode), assign Content-IDs (hash of the processed bytes), rewrite `src` to `cid:` and
   set width/height/style. Pure functions with explicit arguments.
3. Attribute parser for `{width=...}` after an image (AST transformer in the shared
   converter).
4. `OutgoingMessage` gets `InlineImages []InlineImage`; `buildEntity` writes `related`
   inside `alternative`; plain-text rewrite; error type carrying the file name.
5. `ComposeHTML` gains the attachments argument and (preview) returns the HTML with images
   as `data:` URIs; preview CSP widened;
   status shows which images failed.
6. Tests: MIME structure parsed back with `mime/multipart`, dedupe, size rules table,
   `{width}` parsing, plain-text rewrite (no path or base64 in the plain part), EXIF
   orientation and GPS removal on generated JPEGs, error cases, PGP entity contains the
   images, preview CSP.
7. Docs same change: guide ("Images in messages": syntax, sizing, privacy, what the plain part
   shows), help text, README, CHANGELOG. `keybindings.csv` unchanged (no key).
8. Before merge to `mine`: `gofmt -l .`, `go build ./...`, `go vet ./...`,
   `go test ./... -race`, `golangci-lint run`.
9. Manual: screenshots of the preview; send Proton -> Gmail and Gmail -> Proton with the
   test document; check inline display in both, the plain part, and Outlook if available.

## Open points

- The attach step could write `![name](name.png)` at the cursor when the attachment is an
  image (an "insert as inline image" choice); a follow-up. Marking referenced attachments
  "(inline)" in the compose attachment list is also a follow-up.
- Display width for `{width=N}` above 600 is allowed (the user asked for it); the 2000 px
  embed ceiling still applies.
- Animated GIFs are never resized; an oversize one is an error.
- Whether the Sent copy should keep the original (unprocessed) image: no, it is the
  transmitted message.
