# Forward uses the quote marker (#20)

## Goal
A forward quotes the original the way a reply does: with the `⁑` marker, keeping
the original's tables, lists and links, with F3 (view, copy), `e` (expand) and
the inline-image switch, instead of a flattened `> ` text quote.

## Findings
- `NewForward` writes a text quote (`quoteForward`) and copies the original's
  attachments; it sets neither In-Reply-To nor anything the marker could be
  resolved from. A reply resolves from its In-Reply-To, which a forward must not
  carry (the recipient's client would thread it into the wrong conversation).
- A draft reopened from Drafts had no record that it was a forward at all
  (`isForward` was not restored).

## Design
- `drafts.forward_of` (Message-ID of the forwarded message): migration beside
  `sender_from`, written on every save (autosave sends it again), read by
  `GetDraft`/`ListDrafts`. Rejected: reusing `in_reply_to`.
- `mailapp.BuildForwardQuote`, heading = the forwarded-message header block;
  `WithForwardQuote(key, lookup)`; `PrepareCompose` takes the key from
  `draft.ForwardOf` when there is no In-Reply-To. Expansion keeps the header
  lines (Markdown hard breaks). Error texts say "forwarding" where it applies.
- UI: `ComposeModel.forwardOf`; `NewForward` starts with the marker when
  `compose_reply_marker` is on (one setting for both, no new knob) and the
  original has a Message-ID, else today's text forward; `quoteKey()` picks
  In-Reply-To else forward_of for F3, `e`, preview and send; reopening a draft
  restores the forward and its title. Attachments unchanged.
- CLI: `--forward cache:ID` (`Fwd:` subject, original's cached attachments, no
  threading, `--quote marker|text|none`, default marker, not with
  `--in-reply-to`, `messages.read` for agents), shared core in `mailapp`.

## Not done
A forward header `Date` in the sender's time zone (it follows the local zone like
the text forward); remote-imported drafts have no forward key (only drafts made
here do); inline images are both attachments and (with F10) in the quote.

## Tests
mailapp (header block in HTML/text, no In-Reply-To, key from the draft or the
option, missing original, expansion), db (round trip, autosave, reopen), UI
(start, setting off, send, F3/expand, reopen), CLI (draft, quote modes, refusals,
agent capability).
