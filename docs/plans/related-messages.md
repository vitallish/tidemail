# Related messages (plan)

Issue: vitallish/tidemail #15. Written 2026-11-12 before implementation.
Stacked on `feat/search-operators` (it adds a term to that query language).

## What it is
From a highlighted message, show every message in its conversation, from every
folder of the account, as an ordinary search result: earlier messages, later
ones, and my own replies (Sent, archive, anything). It needs a message to start
from, so:
- TUI: a key (`L`) or the palette command "Show related messages" on the
  highlighted message turns the search row into `related:<id>`; `Esc` leaves.
- Anywhere a search is accepted: the term `related:<id>` (the CLI takes
  `related:cache:123`, the id `messages list/search` print). It combines with the
  rest of the language (`related:123 from:ann`, `related:123 -is:read`).

## Matching rule (decided in #15, not changed here)
Only reply headers decide: `Message-ID`, `In-Reply-To`, `References`; the subject
is never used. The related set is the transitive closure of that header graph over
all cached mail of the message's account (all its folders, Trash and Sent
included), Message-IDs compared case-insensitively. This is the same connected
component `conversation.BuildThreads` builds for the message list; the db layer
cannot import `conversation` (it imports db), so `db.RelatedMessageIDs` carries a
small union-find of its own and a test in `conversation` asserts both agree on a
corpus, so they cannot drift.

## Beyond the cache (server)
`Ctrl+G` / `--server` on a `related:` search first expands the term into IMAP
header searches over the Message-IDs the closure knows (the message's own, and
every ID it references or is referenced by; at most 15 IDs): `HEADER Message-ID`,
`HEADER References` and `HEADER In-Reply-To` for each, ORed. Matches the cache
lacks are downloaded (existing server-search rules and caps), then the local
closure runs again and now includes them. One round only: a newly downloaded
message may reference further IDs; pressing again follows them. Gmail's All Mail
makes this one query; other servers search each folder (slower). Unverified on
Proton Bridge.

## Out of scope
- Restoring the previous cursor and selection on `Esc` (leaving search returns to
  the folder, as search does today).
- Threading display inside the results (they are a flat date-ordered list with
  the folder shown on each row, as in any search).
- `[keys]` rebinding of the new key (key-overrides branch, per the key rule).

## Tests
- db: closure across folders (both directions), case-insensitive IDs, missing
  headers, cycles, a message with no Message-ID, another account's mail excluded,
  a shared subject with no header link NOT related.
- conversation: agreement with `BuildThreads` on the same corpus.
- query: parse `related:123`, `related:cache:123`, bad values; server expansion
  to header criteria against the in-memory IMAP server, and against Dovecot (opt-in).
- UI: the key sets the query and runs the search; no message highlighted -> a
  status line; CLI: `messages search related:cache:N [--server]`.
