# Canvas2D — Remaining Platforms Implementation Plan

> **For agentic workers:** Execute with superpowers:subagent-driven-development. One platform at a time. Write the test fixture FIRST. Per-platform: implement → spec review → quality review → commit.

**Date:** 2026-06-09
**Status:** Approved (design forks confirmed with user 2026-06-09)
**Prerequisite:** Canvas2D shared infra + HTML platform are DONE and green (`go tool verify` exit 0).

## Goal

Implement Canvas2D rendering on the platforms with a native 2D drawing surface:
**fyne**, **gtk4**, **android** (Kotlin/Compose). **bubbletea is deferred** (user decision — kitty/unicode raster left for a later round).

## Confirmed design forks (user, 2026-06-09)

- **bubbletea:** DEFER. Not in this plan.
- **fyne:** software raster — rasterize the draw func into an `image.RGBA` with `github.com/fogleman/gg` (immediate-mode `*gg.Context` API maps ~1:1 to our intrinsics), wrap in `canvas.NewImageFromImage`. `golang.org/x/image` is already a dep; `gg` is a new, reversible dep.
- **gtk4:** cairo via cgo. `GtkDrawingArea` + `gtk_drawing_area_set_draw_func`; the draw func receives a `cairo_t*`. Snapshot infra already rasterises via cairo.
- **android:** Kotlin / Compose `Canvas` + `DrawScope` only. go-on-android canvas is out of scope.
- **order:** fyne → gtk4 → android.

## Shared design (applies to every platform)

The reactive-redraw machinery is platform-neutral and already exists:
`ir.CanvasRedrawStmt{Canvas *NodeInst; DrawFunc *Func}`, `lower.Features.Canvas` /
`Features.ReactiveCanvas`, `passCanvas` (shape children → `_canvasDrawN` Synthesized
`*ir.Func` of canvas-intrinsic calls; sets `NodeInst.CanvasDraw`, clears `Children`),
`passCanvasReactivity` (injects `CanvasRedrawStmt` into handler/timer bodies that mutate
a var the draw func reads). Every IR walker already has a `CanvasRedrawStmt` case, so a
new platform never crashes a walker — it only needs to **translate**.

**The canvas intrinsics** (in `ir.CanvasIntrinsics`, looked up via the `IntrinsicEmitter`
registry for html): `CanvasSave`, `CanvasRestore`, `CanvasApplyStyle(ctx, style)`,
`CanvasDrawRect(ctx,x,y,w,h)`, `CanvasDrawCircle(ctx,cx,cy,r)`,
`CanvasDrawEllipse(ctx,cx,cy,rx,ry)`, `CanvasDrawLine(ctx,x1,y1,x2,y2)`,
`CanvasDrawPath(ctx,cmds)`, `CanvasDrawText(ctx,x,y,content)`, `CanvasDrawImage(ctx,x,y,w,h,src)`.

**CRITICAL — the registry is lang-keyed, not platform-keyed.** html registers canvas
intrinsics under lang `"js"`. fyne/gtk4/android all target `"golang"`/`"kotlin"` with
*different* native APIs, so a shared `RegisterIntrinsic("golang", ...)` would both panic
on the second registrant and be semantically wrong. **Therefore Go/Kotlin platforms must
translate canvas intrinsics inside their own `IntrinsicTranslator`, not via the shared
registry** — exactly how gtk4 already rewrites every widget call to native cgo in
`gtk4Translator` rather than relying on the golang registry.

**How the draw func reaches the translator.** fyne and gtk4 emit their Synthesized funcs
(e.g. `__renderSlotN`) by routing the func body through `codegen.WalkLowered(body, translator)`.
A canvas-intrinsic `CallStmt` is *not* a lower intrinsic (CreateNode/AppendChild/…), so
`walkOne` falls through to **`translator.OnDefault(stmt)`**. That is the single
interception point for: (a) the canvas-intrinsic `CallStmt`s inside `_canvasDrawN`, and
(b) `CanvasRedrawStmt` injected into handlers/timers. Each platform's `OnDefault`
currently just returns the stmt unchanged — that is where the work goes.

**CRITICAL RISK — `CanvasDraw` survival through flattening.** `passCanvas` sets
`CanvasDraw` on the canvas `NodeInst`. html keeps the canvas as a `NodeInst` and reads
`n.CanvasDraw` in `renderRawElementIR`. But fyne/gtk4 fully flatten the visual tree to
`CreateNode("canvas")` intrinsic calls (NoDeclarative), and the `CanvasDraw` pointer is
**not** carried on the `CreateNode` call. The first job on each MutationModel platform is
to confirm where the canvas node lands after flattening and ensure the draw func + the
need-a-canvas-widget signal survive to the widget-emission point. Likely approaches:
detect `tag == "canvas"` in `OnCreateNode` and pull the matching `*ir.Func` from the
package's Synthesized funcs (match by the canvas element id / draw-func name), or have the
flatten step thread `CanvasDraw` onto the CreateNode metadata. Resolve this *before*
writing draw-call translation — it is the crux.

**`_canvasDrawN` is Synthesized** and lives on the package/main/window `Funcs` (like
`__renderSlotN`). Each platform already iterates Synthesized funcs through WalkLowered;
verify `_canvasDrawN` is included and emit it with the platform's native ctx param type
(`*gg.Context` / `*C.cairo_t` / Compose `DrawScope` receiver).

---

## Platform 1: fyne

**Files:** `codegen/platform/fyne/canvas.go` (create), `fyne.go` (Capabilities),
`intrinsic_translator.go` (OnDefault + OnCreateNode canvas case), `fyne.sngl` (maybe a
canvas widget override), `testdata`/test.

**Native model:** `ctx` = `*gg.Context` sized to the canvas width/height. The canvas
widget is a `*canvas.Image` (`canvas.NewImageFromImage(dc.Image())`) stored as a Model
field. Redraw = re-run `_canvasDrawN(dc)` into a fresh/cleared `gg.Context`, then
`img.Image = dc.Image(); img.Refresh()`.

- [ ] **Step 1 (fixture first):** Reuse/extend `examples/canvas` or add a fyne golden/snapshot
  fixture proving a `canvas` with `rect`/`circle` renders, plus a Grow/Shrink-style state
  change that triggers a redraw. Follow the fyne snapshot test pattern in
  `codegen/platform/fyne/snapshot.go` + `component_test.go`.
- [ ] **Step 2:** `Capabilities()` — set `f.Canvas = true` and `f.ReactiveCanvas = true`.
- [ ] **Step 3:** Resolve the `CanvasDraw`-survival risk (see Shared design). Add a canvas
  branch where the flattened canvas node is materialised so a `*canvas.Image` Model field
  is created and wired to the draw func.
- [ ] **Step 4:** Translate the canvas intrinsics in `fyneTranslator.OnDefault` (detect
  `CallStmt` whose `Call.Func.Intrinsic` has the `Canvas*` prefix) into `gg.Context` calls.
  Emit a SNGL-color→`color.RGBA` helper (analog of `_snglColor`). Fill fires when
  `style.fill.a > 0`, stroke when `style.stroke.a > 0` (use `FillPreserve` then `Stroke`).
- [ ] **Step 5:** Translate `CanvasRedrawStmt` (also in `OnDefault`) → re-rasterise +
  `img.Refresh()` for the matching canvas Model field.
- [ ] **Step 6:** Emit `_canvasDrawN` as a Go func `func _canvasDrawN(ctx *gg.Context)`;
  ensure `github.com/fogleman/gg` is imported by generated code (RequireImport path).
- [ ] **Step 7:** `go build ./...`, `go test ./codegen/platform/fyne/...`, fixture passes,
  full `go tool verify` green.
- [ ] **Step 8:** Commit `feat(fyne): canvas2d rendering via gg software raster`.

---

## Platform 2: gtk4

**Files:** `codegen/platform/gtk4/canvas.go` (create), `gtk4.go` (Capabilities),
`intrinsic_translator.go` (OnDefault + canvas widget creation), cairo helpers, test.

**Native model:** `ctx` = `*C.cairo_t`. The canvas widget is a `GtkDrawingArea` created
via `gtk_drawing_area_new`, sized with `gtk_drawing_area_set_content_width/height`, and
wired with `gtk_drawing_area_set_draw_func` to a cgo draw callback that calls
`_canvasDrawN(cr)`. Mirror the existing `sngl_connect` callback-registry pattern used for
signals (a `snglDrawFuncs` slice + an exported cgo trampoline) since gtk draw funcs are C
callbacks.

- [ ] **Step 1 (fixture first):** gtk4 cairo snapshot fixture (snapshot infra already uses
  `gtk_widget_paintable` + cairo, gated `//go:build !js`). Prove rect/circle render and a
  state change redraws via `gtk_widget_queue_draw`.
- [ ] **Step 2:** `Capabilities()` — `f.Canvas = true`, `f.ReactiveCanvas = true`.
- [ ] **Step 3:** Resolve `CanvasDraw`-survival; create the `GtkDrawingArea` widget + draw
  callback wiring in `OnCreateNode`/constructor path.
- [ ] **Step 4:** Translate canvas intrinsics in `gtk4Translator.OnDefault` → cairo C calls
  (`cairo_rectangle`, `cairo_arc`, `cairo_move_to`/`cairo_line_to`, `cairo_set_source_rgba`,
  `cairo_set_line_width`, `cairo_fill_preserve`/`cairo_stroke`, `cairo_save`/`cairo_restore`,
  text via `cairo_show_text`). Use the existing `nativeCall`/`cgoCast` helpers.
- [ ] **Step 5:** Translate `CanvasRedrawStmt` → `gtk_widget_queue_draw(drawingArea)`.
- [ ] **Step 6:** Emit `_canvasDrawN(cr *C.cairo_t)`; wire the cgo trampoline.
- [ ] **Step 7:** build + `go test ./codegen/platform/gtk4/...` + full verify green.
- [ ] **Step 8:** Commit `feat(gtk4): canvas2d rendering via cairo`.

---

## Platform 3: android (Kotlin / Compose)

**Files:** `codegen/platform/android/canvas.go`-equivalent (the Kotlin/Compose emitter,
likely additions to `compose_ir.go`), `android.go` (Capabilities), test.

**Native model:** Compose `Canvas(modifier) { /* DrawScope */ }`. `ctx` = the `DrawScope`
receiver. Compose is RenderModel: the `@Composable` re-runs when observed state changes, so
the canvas redraws automatically — **`ReactiveCanvas` may be a no-op / free**. Map the draw
func to a `DrawScope` lambda: `drawRect`, `drawCircle`, `drawOval`, `drawLine`, `Path` +
`drawPath`, `drawText`/`drawIntoCanvas`. Style → `Brush`/`Color` + `Stroke`/`Fill` draw
styles. Confirm whether `_canvasDrawN` becomes a Kotlin top-level/extension function on
`DrawScope` or is inlined into the `Canvas {}` lambda.

- [ ] **Step 1 (fixture first):** android Compose snapshot/golden fixture (see
  `batchsnapshot.go`) proving rect/circle render and a state change recomposes the canvas.
- [ ] **Step 2:** `Capabilities()` — `f.Canvas = true`; set `f.ReactiveCanvas` only if the
  Compose recomposition path needs an explicit redraw (likely false/no-op — verify).
- [ ] **Step 3:** Emit the `Canvas(...) { }` composable for the canvas node; route the draw
  func into the `DrawScope` lambda.
- [ ] **Step 4:** Translate canvas intrinsics to `DrawScope` Kotlin calls. Emit a
  SNGL-color→`androidx.compose.ui.graphics.Color` helper.
- [ ] **Step 5:** Handle `CanvasRedrawStmt` — if recomposition is automatic, translate to a
  no-op (or a `mutableState` touch) and document why.
- [ ] **Step 6:** build + `go test ./codegen/platform/android/...` + full verify green.
- [ ] **Step 7:** Commit `feat(android): canvas2d rendering via Compose DrawScope`.

---

## Final

After all three platforms: dispatch a final whole-implementation code review, then run
`go tool verify` once more and confirm green. Update `docs/superpowers/specs/2026-06-09-canvas2d-design.md`
"Out of Scope" note (non-HTML platforms now partially implemented; bubbletea still deferred).
