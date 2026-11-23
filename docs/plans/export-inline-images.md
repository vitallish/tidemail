# Show inline (cid:) images in View in Browser (#24, also #11)

Branch: `feat/export-inline-images`, cut from `main` (2026-11-23). Owner request: "I also need to
be able to see inline images when viewing an email in browser".

## Why

`Ctrl+B` exports the message's HTML and opens it. `cid:` sources pass through untouched
(`isInlineSource`), and a browser cannot resolve `cid:`, so every embedded logo, chart or
screenshot shows as a broken image. The guide says so (`Embedded CID images are not exported`).

## Behaviour

1. While building the export, each `cid:` reference in `src`, `background`, inline `style` and
   `<style>` blocks is replaced by a `data:` URI of the matching MIME part (matched by Content-ID,
   brackets and case ignored). The export CSP already allows `img-src data:`, so nothing changes
   there.
2. Embedded images belong to the message, make no network request, and need no opt-in. Remote
   images keep their rule: blocked until the second `Ctrl+B`.
3. Only raster image types are embedded (PNG, JPEG, GIF, WebP, BMP). SVG is left as is, the same
   policy as the reply quote. One image up to 10 MB and 25 MB in all; beyond that the reference
   stays unresolved.
4. A `cid:` with no matching part, or one over the limits, stays as it is (a broken image, as
   today) and is counted: the status line says "N inline images could not be shown".
5. Raw source is already fetched; no new network use.

## Design

- `imap.ExtractHTMLWithImages(raw)`: the HTML plus the image parts that carry a Content-ID, from the
  same `parseBody` that sync uses. `ExtractHTML` is unchanged. This is the one extraction function
  #23 (`viewer_cmd`) can call too.
- `ui.sanitizeEmailHTMLWith(src, allowImages, cid)` embeds before the existing stripping, so the
  stripper sees `data:` and keeps it. `sanitizeEmailHTML` stays as the wrapper (existing tests unchanged).
- The resolver tracks the byte budget and counts the references it could not resolve.

## Decisions and rejected alternatives

| Choice | Rejected | Why |
|---|---|---|
| `data:` URIs in the one HTML file | Sibling files in the export dir | Self-contained, allowed by the CSP, nothing extra to prune. #24 leaned the same way; sibling files can come with #23 on the same extraction. |
| Raster only | Also SVG | An SVG can carry script and external references; not worth the risk for a logo. Same call as the reply quote. |
| No CLI command | A new `messages export` | Nothing in the CLI exports to a browser today, so there is nothing to mirror; #23's subcommand will use `ExtractHTMLWithImages`. Recorded as a deliberate exception to the CLI-equivalent rule. |

## Tests

cid image embedded (data URI, right type); bracketed/mixed-case ids; a missing cid is left and
counted; a `data:` image already present is untouched; CSS `url(cid:)` in a `<style>` and a style
attribute; non-raster and over-limit parts left; remote images still blocked; the export through
`openEmailHTML` with a real multipart/related message. Docs: guide and README sentences, CHANGELOG.
