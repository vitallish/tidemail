# Reply quoting with a placeholder (plan)

Issue: vitallish/tidemail #4 (keep HTML structure when quoting). Written
2026-11-12 before implementation. Status: AWAITING the owner's answers to
"Decisions"; nothing is built.

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

## Decisions for the owner (recommendation first)
1. **Where the original lives until sending.**
   - A. *Snapshot in the draft* (new `quote` columns/table: HTML, text,
     attribution) — recommended. + survives the original being deleted or
     re-synced; the send is exactly what the preview showed; works for queued
     and delayed sends. − schema change, drafts grow by the original's size.
   - B. *Look it up by Message-ID at send time.* + no schema change. − fails if
     the original left the cache; result can change between preview and send.
2. **Default behaviour.**
   - A. *Placeholder is the default for replies* — recommended for the fork.
   - B. *Opt-in setting, today's `> ` text stays the default*: the safest for an
     upstream PR (no behaviour change unless asked), at the cost of the benefit
     being off until enabled. Either way a setting `compose_reply_quote =
     "placeholder" | "text"` exists (Settings > Editor).
3. **Marker glyph and how it shows.** Show it as a dim line reading e.g.
   `⁅ quoted message ⁆` instead of a bare rune? A recognisable text token is
   friendlier than a lone glyph but is longer to guard against; recommended:
   a single reserved rune like `⁂`, rendered by the composer as a labelled dim
   line (the editor still holds the rune).
4. **Quote images:** drop remote and cid images (recommended, privacy and
   robustness) or keep remote ones.

## Tests
Resolution to HTML + text for tables, lists, nested quotes, hostile HTML
(script, style, event handlers, remote images); marker forgery in quoted mail;
move/delete the marker; two markers; draft save/reopen keeps the snapshot;
expand converts and cannot be redone; peek view and copy; CLI reply-to; Gmail and
one more client for the received result (manual).

## Docs
guide (composer, replies), CLI doc, CHANGELOG, `keybindings.csv`, help; hard-code
keys on this branch, register for `[keys]` on `feat/key-overrides` later.
