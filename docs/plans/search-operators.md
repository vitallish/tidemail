# Search: operators, dates, server search, related (plan)

Issues: vitallish/tidemail #8 (operators, `has:attachment`), #15 (related
messages), groundwork for #12 (saved searches). Written 2026-11-12, before
implementation. Status: plan, awaiting the owner's answers to "Decisions".

## What exists today (read from the code, 2026-11-12)
- `/` searches the local cache only: `db.SearchAllMessages` ->
  `ParseSearch` (`internal/db/search_tags.go`) splits words from `#tags`;
  words become one FTS5 MATCH, all ANDed, last word as a prefix
  (`ftsQuery`, `internal/db/messages.go`).
- FTS columns: `subject, from_addr, to_addr, cc_addr, body_text`
  (`messages_fts`). So subject, body, from, to (and cc) are searchable today,
  but only as "anywhere in those columns": no way to say "from X".
- Dates: not searchable. `messages.date` is a unix column, unused by search.
- No OR, no negation, no `has:attachment` (the column `has_attachment` exists).
- Server: not used by search at all. The guide says so ("Search only reaches
  mail TideMail has cached", `docs/guide.md`). The IMAP client already runs
  `UIDSearch` for sync (`internal/imap/parse.go`) so the plumbing exists.

## Query language (one parser, two backends)
Implicit AND between terms. `OR` (uppercase) between terms, `-term` to
exclude, `"quoted phrase"`. Parentheses are NOT in the first cut.

| Term | Meaning | Local | Server (IMAP) |
|---|---|---|---|
| `word` | anywhere (subject, from, to, cc, body) | FTS5 | `TEXT word` |
| `subject:x` `from:x` `to:x` `cc:x` `body:x` | one field | FTS5 column filter | `SUBJECT` `FROM` `TO` `CC` `BODY` |
| `after:2026-01-01` `before:...` `on:...` | date | `messages.date` | `SINCE` `BEFORE` `ON` (date only, server time zone) |
| `after:7d` `after:3m` | relative date | same | same |
| `has:attachment` | has an attachment | `has_attachment` | see Decisions 5 |
| `is:unread` `is:starred` | flags | `read` `starred` | `UNSEEN` `FLAGGED` |
| `#tag` | tag on a row | as today | never (tags are local) |
| `related:` | the selected message's conversation | see below | `HEADER` searches |

Bare text with no operators behaves exactly as today (same results), so
nothing changes for people who do not use operators.

## Server search
IMAP SEARCH runs per mailbox, so a search over several folders is one query
per folder. Rules:
- Local results show immediately as you type (unchanged). Server search runs
  only when asked (a key / palette command, shown in the footer), never per
  keystroke: it costs a round trip per folder and some servers are slow.
- The same parsed query is compiled to `imap.SearchCriteria`; terms the
  server cannot express (`#tag`, maybe `has:attachment`) are applied locally
  after fetch, and the status line says so.
- Matching UIDs not yet cached are fetched (existing `fetchNumSet`) and
  stored (`storeFetchedMessages`), so opened results behave like any message.
- Capped per folder (default 100 newest hits) with a "more available" line.

## Related (#15) in the same search
`related:` expands the selected message to the closure of
Message-ID / In-Reply-To / References across all cached mailboxes of the
account (subject never used, case-insensitive IDs, cycles safe), shown as a
search result with the folder on each row. Server step: IMAP
`HEADER Message-ID|In-Reply-To|References <id>` per folder not fully cached
(Gmail All Mail covers everything in one query). Esc restores the previous view.

## Tests (no mail account needed)
- Parser: table tests for every term, quoting, OR, negation, bad dates.
- Local backend: seeded in-memory DB; each operator, OR/NOT, dates, tag
  combination, and "no operators == old results" regression.
- Server backend: `imapmemserver` (already used in `internal/imap`) for
  SEARCH criteria and the fetch-and-store path. GreenMail lacks UID SEARCH
  (known, `CLAUDE.md`) so it cannot test this.
- Live check on Proton Bridge and Gmail by the owner (Bridge's SEARCH
  support for BODY/TEXT is NOT verified; Gmail's is documented).

## Decisions for the owner
1. Trigger for server search: explicit key/command (recommended) vs
   automatic fallback when local results are empty.
2. Scope: current account's folders (recommended default) vs all accounts.
3. Results: cache what the server returns (recommended) vs show-only.
4. Date semantics: `after:` inclusive, `before:` exclusive, local time.
5. `has:attachment` on the server: Gmail `X-GM-RAW has:attachment` where
   available; elsewhere local-only with a status note (recommended) vs a
   `Content-Type: multipart/mixed` header heuristic (wrong too often).

## Docs and bookkeeping
Guide section (replaces the "Search covers..." paragraph and the cache note),
help text, palette entry, CHANGELOG, README feature line, `keybindings.csv`
row. Key hard-coded here per the key rule; `[keys]` registration later.
