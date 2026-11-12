# Table styling for sent Markdown tables (`compose_table_style`) and one shared converter

Branches: `fix/gfm-tables` gets the shared converter (vault issue #2); `feat/table-style`
is stacked on it and adds the styling, config and docs (issue #1). Cut 2026-11-12.

## Why

Pipe tables now send as real `<table>` HTML (commits 2754279, 1826776), but Gmail web
shows them bare: no borders, columns nearly touching. Alignment and the bold header
survive. Mail clients drop or ignore `<style>` blocks, so the styling has to be inline.
Users who do not want it must not get it: it is opt-in.

Separately, two goldmark converters exist (`smtp.MarkdownToHTML`, and one built in
`mailapp.composeBody` when the body carries the signature marker). The missing-GFM bug
came from that duplication; the styling would be the next thing to be added to only one.

## Behaviour

- `[display] compose_table_style`: `"none"` (default, bare tables as today), `"basic"`,
  or `"custom"`.
- `basic`: `border-collapse:collapse` on `<table>`; `border:1px solid #999;padding:4px 8px`
  on every `<th>`/`<td>`, merged with the existing `text-align`. #999 reads on light and
  dark backgrounds.
- `custom`: the cell declaration comes from `[display] compose_table_css` (CSS
  declarations only, e.g. `border:1px solid #444;padding:6px 10px`); `<table>` still gets
  `border-collapse:collapse`. Empty `compose_table_css` behaves as `basic`.
- `compose_table_css` is HTML-escaped into the `style` attribute, so a stray quote cannot
  break out of it.
- Plain-text part, preview and reading of incoming mail are unchanged. Only the HTML part
  of a sent message changes.
- Unknown value: treated as `none`, shown as a warning where config warnings already
  surface (not silently).

## Decisions and rejected alternatives

| Choice | Rejected | Why |
|---|---|---|
| Custom goldmark node renderer for table nodes | Post-process the HTML string; a `<style>` block | Decided in #1: string edits break when goldmark output changes; clients strip `<style>`. |
| Opt-in, default `none` | Default `basic` | Keeps upstream behaviour unchanged for anyone who does not ask; the owner turns it on in their config. Reversible by flipping the default later. |
| `none/basic/custom` + `compose_table_css` | One free-form string | Presets are discoverable in docs and settings; the CSS string is only for people who want control. Cost: two keys. |
| Package-level setter (`smtp`), set once after config load | Pass the style through `Prepare`/`PrepareCompose` | `Prepare` has 5 callers (TUI, CLI, cliform); the repo already uses this pattern (`setEditorVimMode`, `setComposeFilePickerCmd`). Cost: global state, noted in a comment. |
| One `newMarkdown(opts)` constructor in `smtp`, exported for `mailapp` | Keep two builders | One place for extensions and options; `composeBody` adds only its signature renderer. |
| Config file only for now | Settings screen entry | Keeps the diff to one topic; a settings row can follow. |

## Implementation steps

1. `fix/gfm-tables`: `smtp.NewMarkdown(extra ...renderer.Option) goldmark.Markdown` with
   GFM; `MarkdownToHTML` and `composeBody` both use it. Test: a table renders on both
   paths (exists) and a test that fails if either path builds its own converter without
   GFM (assert both outputs contain `<table>`).
2. `feat/table-style`: `config.DisplayConfig.ComposeTableStyle`, `ComposeTableCSS`
   (end of struct, gofmt); round-trip test; default stays empty.
3. `internal/smtp`: table renderer (priority above the GFM one) emitting the inline
   styles, alignment merged; `SetTableStyle(style, css string)` rebuilds the converter.
4. Set it where config is loaded for sending: `main.go`, `cli/mail.go`,
   `cli/interactive.go`, `cli/mail_reads.go` (via one `mailapp.ApplyConfig(cfg)` helper).
5. Tests: none leaves HTML byte-identical to today; basic and custom emit the styles;
   alignment kept; header cells styled; quote in `compose_table_css` escaped; both
   Markdown paths; config round-trip.
6. Docs in the same change: `docs/guide.md` new "Tables in messages" section (how to write
   a pipe table, alignment, what is sent, which clients were checked, the three settings
   with examples), `README.md` config reference, `CHANGELOG.md`. In-app help and
   `keybindings.csv` unchanged: no key is involved (deliberate omission).
7. Before merge to `mine`: `gofmt -l .`, `go build ./...`, `go vet ./...`,
   `go test ./... -race`, `golangci-lint run`.
8. Manual check: send the sample table from `tidemail` (Proton) to Gmail with
   `basic`; record the Gmail result in the guide. Outlook stays unverified unless checked.

## Open points

- Dark-mode clients may invert or recolour; #999 was picked as neutral, not tested.
- Gmail mobile and Outlook unverified.
