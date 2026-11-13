# Reply quoting with a placeholder (plan)

Issue: vitallish/tidemail #4 (keep HTML structure when quoting). Written
2026-11-12 before implementation. Status: decisions answered by the owner
2026-11-12 (below); implementation next.

## What exists today (read from the code, 2026-11-12)
- A reply starts with the original as editable text: `quoteReply` (internal/ui/
  compose.go) puts `On <from> wrote:` and every line prefixed `> ` in the body,
  under the signature marker. The quote comes from `quotableText(original)`, the
  plain-text rendering, so tables and layout are already flattened by the time
  the user sees them.
- Sending converts the whole body as Markdown, so `> ` lines become a
  `<blockquote>` of flattened text. Nothing of the original HTML survives (#4).
- The signature already solves a similar problem: a reserved marker line `⁂`
  stands for "the signature goes here", is resolved at send time
  (`mailapp.composeBody`), can be moved or deleted, and is never edited inline.
- Drafts store `InReplyTo` and `References` only; there is no copy of the
  original (`db.Draft`). `ui.sanitizeEmailHTML` already cleans received HTML for
  viewing. The CLI cannot create replies at all.

## Proposal
1. **Placeholder.** A reply's body gets a second reserved marker line, next to
   `⁂` (glyph to be chosen, one rune, checked like `⁂` so quoted mail cannot
   forge it). It means "the quoted original goes here". Where the user leaves it
   decides top, bottom or inline posting; deleting it sends no quote.
2. **Resolved at send time**, from a snapshot kept with the draft (see Decision
   1): HTML part gets `On <date>, <name> wrote:` plus
   `<blockquote type="cite">` holding the original's sanitized HTML, so tables
   and lists survive; the text part gets the `> ` quoted plain text. Remote
   images are dropped from the quote (alt text kept) and `cid:` images are
   dropped: nothing in the quote should phone home or break.
3. **See or copy it without expanding.** A key in the composer opens a read-only
   scrollable view of the quoted original (rendered like the reader) with a copy
   action that copies its plain text. Closing it changes nothing.
4. **Expand.** A composer command converts the placeholder into editable
   Markdown (`> ` quote, tables as GFM tables) in place. This is deliberate and
   one-way: after it the original HTML is no longer attached, so there is no
   re-creating the placeholder. Editor undo (Ctrl+Z) still reverts the body edit
   while the composer is open.
5. **Everywhere.** `tidemail draft create --reply-to cache:ID [--reply-all]`
   builds the same draft (placeholder plus snapshot), and the sender resolves it
   identically, so the whole path is testable without the TUI. Forward can adopt
   the same mechanism later; not in this change.

## Decisions (owner, 2026-11-12)
1. **Lookup, not snapshot.** The original is found by the draft's `In-Reply-To`
   Message-ID in the cache when the draft is previewed or sent; no schema change.
   Owner's reason: few drafts sit around. Consequence accepted: if the original
   left the cache, sending/preview fails with a message saying to expand the quote
   or delete the marker. The alternative (snapshot in the draft) was rejected for
   the schema change; it remains the fix if lookups ever fail in practice.
2. **Opt-in.** `[display] compose_reply_quote = "text" | "placeholder"`, default
   `"text"` (today's behaviour, so an upstream PR changes nothing unasked); Settings
   > Editor picker. The owner's own config is set to `"placeholder"`.
3. **Marker:** one reserved rune, shown by the composer as a dim labelled line
   `⁅ quoted message ⁆`; the editor still holds the rune.
4. **Images in the quote** (recommended by me after comparing with what Gmail does;
   the owner asked, not yet confirmed): keep remote `https` images, drop `http`
   images and 1x1 pixels, drop inline (`cid:`) images keeping their alt text;
   re-embedding inline images is a follow-up.

## Amendments made while building (2026-11-12)
- **Setting is a toggle, not a picker:** `[display] compose_reply_marker = true|false`
  (Settings > Editor > "Reply quote marker"), not `compose_reply_quote = "text" |
  "placeholder"` as decided above. Same meaning and same default (off); a toggle
  reuses the existing widget and the setting has only two states. Decision 2 is
  otherwise unchanged.
- **Undo:** the editor's own Ctrl+Z cannot see a whole-body replacement, so the
  "Ctrl+Z still reverts" claim in point 4 was false. Instead, Ctrl+Z immediately
  after expanding restores the marker, and only while the body is exactly what
  expanding produced (tested); after any other edit it no longer can.
- **Stacking:** the branch sits on `feat/inline-images` (not `main`): `composeBody`
  and the image resolver it changes live there.
- **Images:** as confirmed by the owner (decision 4).

## Tests
Resolution to HTML + text for tables, lists, nested quotes, hostile HTML
(script, style, event handlers, remote images); marker forgery in quoted mail;
move/delete the marker; two markers; draft save/reopen keeps the snapshot;
expand converts and cannot be redone; peek view and copy; CLI reply-to; Gmail and
one more client for the received result (manual).

## Docs
guide (composer, replies), CLI doc, CHANGELOG, `keybindings.csv`, help; hard-code
keys on this branch, register for `[keys]` on `feat/key-overrides` later.
