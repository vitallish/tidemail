# Inline images in a reply's quote (#19)

## Goal
When the quote marker is resolved, carry the original's inline (`cid:`) pictures
into the reply instead of leaving only their alt text. Switchable per message,
with a Settings default.

## Findings
- The original's inline parts are cached with their bytes (`attachments`,
  `db.GetImageParts`), so no IMAP fetch is needed at send time.
- The quote HTML is inserted after the Markdown conversion, so it never passed the
  image resolver; `smtp` already builds `multipart/related` from
  `OutgoingMessage.InlineImages`, so nothing changes there.
- `quotehtml` drops every `cid:` image by policy (agreed 2026-11-12).

## Design
- `quotehtml.SanitizeWith(src, Options{CID})`; `Sanitize` is the wrapper, so the
  existing policy and tests stay. An unresolvable cid still degrades to alt text.
- `inlineimg.Resolver.EmbedQuoteImage`: decode, resize, strip EXIF, content-hash
  cid (the draft's code path), a separate 5 MB budget, and no problems recorded:
  an unsupported (webp, svg), corrupt or over-budget picture stays alt text and
  never fails a send. In preview it yields a `data:` URI of the same bytes.
- `mailapp.WithQuoteImages(loader)`; the loader reads the cached parts. The
  resolver of the message collects draft and quote parts together.
- UI: per-message `quoteImages` starting from `compose_reply_quote_images`
  (off). `Alt+I` in the message, `i` in the `F3` view, command palette item,
  state on the compose state line, help, `keybindings.csv`. Not stored with the
  draft (a reopened draft starts from the setting); noted in the docs.
- CLI: `--quote-images` / `--no-quote-images` with `--quote marker`.

## Not done
Content-Location references, picture parts not cached, a per-draft stored toggle,
carrying remote `https` images (they were already kept as links).

## Tests
quotehtml (callback, brackets, scheme case, pixel stays dropped, default policy),
mailapp (real PNG, built MIME is related with the part, text part clean, off
unchanged, unsupported/oversized skipped, budget, preview data URI, loader
error), UI (toggle, send, default, no marker), settings, CLI sizes and flags.
