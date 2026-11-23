# Attachment marker in the message list (#31)

## Goal
Show which messages have an attachment, without counting inline logos or
signature parts as attachments.

## Findings
- `messages.has_attachment` is set by `parseBody` whenever a part other than the
  body is kept, so inline signature logos and PGP/S-MIME signature parts set it.
- The reader depends on that broad flag (it loads cid images when it is set), so
  its meaning is not changed.
- Parts are cached in `attachments` (`inline`, `content_id`, `content_type`)
  once the body has been stored.

## Design
- A computed column `attachment_marker` (SQL expression `attachmentMarkerSQL`)
  added to every list query that `scanMessages` reads: true when
  `has_attachment` is set and either no parts are cached (fall back to the flag)
  or some part is not a cid-referenced inline image and not a signature part.
  No schema change, always in step with the cached parts.
- `Message.AttachmentMarker` (transient). `GetMessage` does not fill it.
- Setting `show_attachment_marker` (off by default, like upstream today). Row:
  an `ATTACH` chip in the tag area, like a mail tag (`$HAS_CAL`); it was first a
  two-cell glyph before the date, replaced 2026-11-23 because the owner found it too
  small. `⊕` when narrow, `[attach]` in the VT52 theme.
- CLI: `messages list|search --json` gains `attachment_marker` beside
  `has_attachment`, so scripts get the answer the list shows.

## Not done
- Changing the stored flag (it would break cid image loading and `has:attachment`).
- A marker in the reader header, the Drafts list, or the sidebar.

## Tests
db rule (logo, signature, pdf, mixed, inline photo, no cached parts, search),
row width and column stability, PlainUI glyph, settings round trip, CLI JSON.
