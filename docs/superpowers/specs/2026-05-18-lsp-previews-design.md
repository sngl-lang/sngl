# LSP Previews

Date: 2026-05-18
Status: Draft

## Summary

Add five user-visible preview features to the SNGL language server, shared by the Neovim and VS Code editor integrations:

1. **Component preview image on hover** — show a pre-rendered PNG when hovering a component identifier, sourced from `.sngl/previews/`.
2. **Color swatches** — `textDocument/documentColor` for `#hex` color literals.
3. **Measurement inlay hints** — resolved-pixel hint next to non-`px` measurement literals.
4. **Rich hover markdown** — replace plain hover text with structured per-kind markdown (signatures, doc comments, type details, value badges, image embeds).
5. **Side-panel live preview** — local browser side-panel rendering the enclosing window of the cursor, live-reloading on edit, click-to-jump-to-source.

All five route through `sngl lsp`. No new external dependencies beyond what the HTML platform already uses. No on-demand headless rendering — preview PNGs are pre-built artifacts.

## Goals

- One LSP server process serves both editor stacks.
- No editor-specific business logic; both clients are thin shells.
- All preview rendering reuses existing `internal/snapshot` and `codegen/platform/html` code paths.
- Features degrade gracefully: missing snapshot → no image, no browser available → no side-panel, etc. Never block other LSP features.

## Non-goals

- On-demand headless rendering from inside `sngl lsp`. PNGs come from a separate `sngl snapshot` step the user runs.
- Hot-reloading the language server itself.
- Rendering non-HTML platforms (BubbleTea, Android, Fyne) into the side-panel. HTML only.
- VS Code Marketplace publication. Ships as a local `.vsix` for now.

## Architecture

`sngl lsp` boots an embedded HTTP+WebSocket server on init, bound to `127.0.0.1:0` (OS-assigned port). On bind, the server emits a custom LSP notification `sngl/previewReady` with the chosen port and a session token. Editor clients listen for it, store the URL, and use it for browser launches and webview src.

```
┌─ sngl lsp process ──────────────────────────────────┐
│  LSP JSON-RPC (stdio)        HTTP/WS (127.0.0.1:N) │
│  ├─ hover (rich md)          ├─ /preview/<window>   │
│  ├─ documentColor             │   → HTML            │
│  ├─ inlayHint (measure)       ├─ /assets/<sha>.png  │
│  ├─ executeCommand            │   → PNG bytes       │
│  │  └─ sngl.openPreview       └─ /ws?token=…        │
│  └─ semanticTokens                → reload+jump     │
│                                                      │
│  shared state: workspace, doc cache, snapshot index │
└──────────────────────────────────────────────────────┘
```

Lifetime: server starts on `initialize`, stops on `shutdown`. Single instance per LSP process. All HTTP/WS endpoints require the session token (header or query param) to prevent cross-origin attacks from other localhost software.

### Packages

| Package                               | Role                                                                                   | Status   |
|---------------------------------------|----------------------------------------------------------------------------------------|----------|
| `internal/lsp/preview`                | HTTP+WS handlers, WS hub, HTML compile-per-request                                     | new      |
| `internal/lsp/previewindex`           | Walks `.sngl/previews/` and maps `(file URI, ident) → png path`                        | new      |
| `internal/lsp`                        | Upgrade `hover.go`; add `color.go`, `inlay.go`, `command.go`; emit `sngl/previewReady` | extended |
| `internal/snapshot`                   | Unchanged                                                                              | reused   |
| `codegen/platform/html`               | Reused for side-panel HTML generation                                                  | reused   |
| `editors/neovim/lua/sngl/preview.lua` | Notification listener, `:SnglPreview` command, port discovery                          | new      |
| `editors/vscode/`                     | TS extension: LSP client + webview side-panel                                          | new      |

## Feature details

### F1 — Component preview image on hover

**Trigger**: `textDocument/hover` over a component identifier.

**Source of image**: `.sngl/previews/<rel_path_to_sngl_file>/<ComponentName>.png`, where `rel_path_to_sngl_file` is relative to repo root (the directory containing `go.mod`).

**Production of image**: the existing `sngl snapshot` CLI command writes these, with two extensions required:

- New default output layout: write to `.sngl/previews/<rel_path>/<name>.png` (repo-root relative) when invoked without `--out`. Today's default is `<dir>/snapshots/<name>_<platform>.png`; preserved as opt-in via explicit `--out`.
- New flag `--components`: render each component declaration in isolation (scaffolded into a synthetic minimal window, matching the brainstorm decision). Today's snapshot only renders windows. Without this flag, only window snapshots are produced.

The LSP only reads these files; it never produces them.

**Index**: `previewindex.Index` watches the `.sngl/previews/` tree (fsnotify) and exposes `Lookup(uri lsp.DocumentURI, name string) (path string, ok bool)`. Index is built lazily on first hover request, then kept warm.

**Hover markdown**: embed via `![](<previewURL>)` where `previewURL` resolves to `/assets/<sha256-of-path>.png` on the embedded HTTP server. PNG bytes streamed with `Cache-Control: max-age=604800, immutable`.

**Editor compatibility**: standard LSP `MarkupContent` of kind `markdown` with image references. Both Neovim's `vim.lsp.util.open_floating_preview` (with `markdown` filetype) and VS Code render this natively if image fetching is enabled. Neovim users without image-capable terminals see the URL text — acceptable degradation.

### F2 — Color swatches via `documentColor`

**Capability**: server advertises `colorProvider: true` in initialize result.

**Handler**: `textDocument/documentColor` walks the document AST and returns one `ColorInformation` per `#hex` color literal token. Hex parsed `#rgb`, `#rrggbb`, `#rrggbbaa`. Each yields LSP `Color {red, green, blue, alpha}` in the 0..1 range with the literal's range.

**Out of scope (v1)**: `color.rgb(...)` function calls. Resolving arbitrary expressions to a swatch would require partial evaluation in the LSP and the payoff is small. Can be added later.

**Color presentations**: `textDocument/colorPresentation` returns the picked color formatted as `#rrggbb` (alpha=255) or `#rrggbbaa` (otherwise). Single presentation, no alternates.

### F3 — Measurement inlay hints

**Capability**: server advertises `inlayHintProvider: true`.

**Handler**: `textDocument/inlayHint` walks measurement literals in the requested range. For any literal whose unit ≠ `px`, returns an inlay hint placed immediately after the literal with label `(<n>px)`.

**Viewport assumptions** (documented in spec and exposed as LSP init options):

- root font size: 16px
- viewport width: 1280
- viewport height: 800

| Unit  | Resolution                              |
|-------|-----------------------------------------|
| `px`  | no hint (would be redundant)            |
| `em`  | `n * 16`                                |
| `rem` | `n * 16`                                |
| `vw`  | `n / 100 * 1280`                        |
| `vh`  | `n / 100 * 800`                         |
| `pct` | no hint (no anchor — depends on parent) |

**Toggle**: init option `sngl.inlayMeasurements: bool` (default `true`).

### F4 — Rich hover markdown

**Replaces**: existing `lsp/hover.go` (36 lines, plain text).

**Per-kind layout** (single `MarkupContent` markdown block):

| Kind                 | Block contents                                                                                                                                |
|----------------------|-----------------------------------------------------------------------------------------------------------------------------------------------|
| Component            | Code-fenced signature `Foo(prop: int, ...) component` (sngl-fenced). Doc comment as prose. If image present (F1): `![Foo](url)`.              |
| Function             | Code-fenced signature with concrete type-args resolved at call site if applicable. Doc comment. Receiver type when method.                    |
| Variable / const     | `name: Type` fenced; if compile-time constant, value rendered (`= 42`).                                                                       |
| Struct               | Code-fenced declaration body (fields list). Doc comment.                                                                                      |
| Enum                 | Code-fenced declaration body (variants list).                                                                                                 |
| Unit                 | Code-fenced declaration body (variant=multiplier list).                                                                                       |
| Stdlib symbol        | Same as above + leading `> stdlib` blockquote + footer link `[Docs](https://sngl.duckfam.us/stdlib/<pkg>#<sym>)`.                             |
| `#hex` color literal | Swatch line `▇ #rrggbb` (the ▇ is colored via inline HTML span where supported; markdown-fallback shows just the hex) + `rgb(r, g, b)` tuple. |
| Measurement literal  | `n<unit> = m px` (matches F3).                                                                                                                |
| Keyword              | Brief one-line description.                                                                                                                   |

**Lookup**: identifier resolution reuses existing checker results (already cached per document). New helper `hoverInfo(doc, pos) HoverEntry` enumerates kinds.

### F5 — Side-panel live preview

**Open**: editor invokes LSP `workspace/executeCommand` with command `sngl.openPreview` and args `{uri, position}`. Server resolves the enclosing window declaration at that position; if none, returns the first window in the file (so a cursor in a component still shows something useful). Returns `{url: <preview-url>}`. Editor opens it (browser/webview).

**Render**: GET `/preview/<file-id>/<window-name>` compiles the current in-memory document state through `codegen/platform/html` with `--lang none` (static mode), returning the produced `index.html` with assets inlined. No filesystem write; rendered fresh per request from the LSP's doc cache.

**Click-to-source**: each rendered SNGL node gets `data-sngl-pos="<line>:<col>"` attribute. Page-injected JS captures clicks (Alt+click only, to not interfere with the rendered UI), sends `{type: "jump", line, col}` over WS. Server forwards to LSP client as custom notification `sngl/jumpToSource` `{uri, line, col}`. Editor plugin handles it.

**Live reload**: on `textDocument/didChange` (debounced 200ms), the WS hub broadcasts `{type: "reload"}` to all clients viewing the affected URI. Browser JS catches it and `location.reload()`s. Reload skipped if the change contained no AST-relevant edits (text-only inside comments) — heuristic, not required for correctness.

**Errors**: if the document fails to parse or check, server still renders, replacing the body with the most recent successful render plus a banner showing the current diagnostic. Stale render is preferable to a blank screen while typing.

**Scope per render**: just the chosen window. Multi-window navigation across the preview deferred to a v2.

### Security

All HTTP/WS routes require a session token issued at startup, passed as `Authorization: Bearer <token>` (HTTP) or `?token=<token>` (WS handshake). Token bound to the LSP process lifetime. Bind only to `127.0.0.1`. No CORS — webviews and editor-launched browsers can attach the token via the URL the LSP hands them.

## Data flow

### Hover on a component (F1+F4)

```
editor ──hover(uri, pos)──▶ lsp
                              │
                              ├─ resolve ident → ComponentDecl
                              ├─ build markdown (sig + doc)
                              ├─ previewindex.Lookup(uri, name)
                              │      └─ found? append ![](url)
                              └─ return MarkupContent
editor ◀──hover result──────  lsp
   │
   └── render markdown; fetch <img src=lsp-http>
       lsp ◀──GET /assets/<sha>.png──┘
       lsp ──PNG bytes──▶
```

### Open side-panel (F5)

```
editor ──executeCommand sngl.openPreview──▶ lsp
                                              │
                                              ├─ find enclosing window
                                              └─ return {url}
editor opens browser/webview to url
                              ┌──▶ lsp HTTP /preview/<id>/<win>
                              │        compile doc → HTML, inject JS+token
                              ◀─── HTML body
                              ┌──▶ lsp WS /ws?token=…
                              ◀─── connected
... user edits in editor ...
editor ──didChange──▶ lsp ──WS broadcast reload──▶ browser ──location.reload()──▶ lsp HTTP /preview/...
... user Alt+clicks rendered node ...
                              ──WS {type:jump, line, col}──▶ lsp ──sngl/jumpToSource──▶ editor
```

## Editor integrations

### Neovim (`editors/neovim/`)

- `lua/sngl/preview.lua` — registers handler for `sngl/previewReady`, stores URL in module-local state.
- Command `:SnglPreview` — sends `workspace/executeCommand sngl.openPreview` at cursor, opens returned URL via `vim.ui.open` (uses `xdg-open`/`open`).
- Handler for `sngl/jumpToSource` — jumps cursor to received position.
- Document-color / inlay-hint / hover features come from `vim.lsp` built-ins, no Lua needed. Image rendering in hover floats requires a capable terminal (kitty, wezterm); text fallback acceptable.

### VS Code (`editors/vscode/`)

- New TypeScript extension built with `vscode-languageclient`.
- Launches `sngl lsp` as the LSP server; listens for `sngl/previewReady`.
- Contributes command `sngl.openPreview` (Cmd+Shift+P or codelens on window decls) → opens webview pinned to the side, src=preview URL.
- Webview message bridge for `sngl/jumpToSource` is unnecessary — the WS already round-trips through the LSP, which posts the standard `showDocument` request VS Code understands.
- Built and shipped as `.vsix` artifact. CI builds it; no Marketplace publish step.

## Testing

- `internal/lsp/preview` — Go tests for HTTP routes, WS hub broadcast, token enforcement, snapshot index resolution. Use httptest and `nhooyr.io/websocket/wstest`.
- `internal/lsp` — table tests for hover output per identifier kind, `documentColor` over fixture files (covers `#rgb`, `#rrggbb`, `#rrggbbaa`, no-match cases), inlay hint range filtering and viewport math.
- `testdata/lsp/preview/` — `.sngl` fixtures with markers asserting expected hover markdown / color ranges / inlay positions (reuse `internal/testutil` style markers).
- VS Code extension — smoke test via `@vscode/test-electron` that launches the extension, hovers a component, asserts markdown includes image markup.
- Neovim — manual smoke; no automated harness yet.
- End-to-end side-panel — Go test that starts an in-process LSP, drives a browser via `go-rod` (already a dep), changes the doc via LSP, asserts WS reload event was received and re-rendered.

## Rollout

Implementable as independent slices, in this order, each shippable on its own:

1. F2 (documentColor) — smallest, smallest blast radius.
2. F3 (inlay hints) — same shape as F2.
3. F4 (rich hover) — replaces existing handler; needs care around markdown rendering on both editors.
4. F1 (hover images) — requires preview index + HTTP server; first user of the HTTP transport.
5. F5 (side-panel) — depends on HTTP server from F1; adds WS, click-to-source, webview/browser launch.

Each slice ships with tests, editor-plugin updates, and a fixture demonstrating the feature.

## Open questions

- VS Code marketplace publication and signing — deferred until after `.vsix` stabilizes.
- Should `sngl snapshot` watch the source tree and regenerate PNGs automatically? Not in this spec — user runs it explicitly for now.
- `sngl snapshot --components` scaffolding rules: how default prop values are picked (literal defaults if declared, else zero values). Confirmed in brainstorm, restated here for the implementation plan.
- Theme / dark-mode for the side-panel — render uses the document's own styling, so should be unaffected, but if components rely on CSS prefers-color-scheme it'll pick up the browser's setting. Leave as-is.
