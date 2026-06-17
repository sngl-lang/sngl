# Design: bubbletea blueprint primitives (audit finding #3)

**Date:** 2026-06-14
**Status:** implemented for bubbletea (2026-06-17). All stdlib wrappers inline to
the `Layout`/`Styled`/`Widget` primitives via blueprint records; `NoStdlibWrappers`
is enabled and `renderStdlibComponent` is deleted. See finding #3 status block in
the audit for the resolved-state summary.
**Audit:** `docs/superpowers/audit/lowering-migration.md` finding #3
("Stdlib-component → native-widget translation done per platform")

## Goal

Make the bubbletea platform render stdlib components through a small set of
**platform primitives** described by **blueprint records**, so that:

1. Codegen no longer branches on stdlib component *names* (`vbox`, `button`,
   `input`, …) in any of its six subsystems.
2. New components are added by writing a blueprint record (data in
   `bubbletea.sngl`), not Go code.
3. **End users can wrap arbitrary terminal libraries** by writing their own
   component whose body is a `Widget(...)` blueprint — the record types are part
   of the bubbletea platform's public surface, not internal scaffolding.
4. Components that map to real Bubbles widgets (textinput, textarea, list,
   table, spinner, progress) become genuinely interactive. **Behavior changes
   are expected and intended** (e.g. a button is currently just styled text with
   a space for the focus indicator; it gains real focus + enter/space
   activation).

This is the *inline* realization of #3 (the audit's literal framing): wrappers
are inlined to blueprint-carrying primitive nodes, and codegen reads the
blueprint off the inlined nodes — codegen sees only primitives, fully decoupled
from stdlib names.

## Background: how the other platforms already do this

- **html** — `component sngl.X { platform html { <native html.* element> } }`
  (new form). `mergePlatformExtensions` (checker, `stdlib.go`) collects the
  `platform html { … }` body into the stdlib component's `PlatformBodies`;
  `passPlatformExtensionBody` swaps it into `Component.Body`; `passInlinePure`
  inlines it (pure native elements). html has no `renderStdlibComponent`.
- **fyne / gtk4** — same install path, but bodies are single blueprint-carrying
  nodes (`fyne.sngl` `Constructor{goFn,goType,args,…}`), consumed by a generic
  `renderFromBlueprint` (parsed at load by `blueprint.go` into `fyneBlueprint`).
- **bubbletea / android** — legacy `component sngl.X() { … }` (parens) form,
  which `mergePlatformExtensions` explicitly **ignores** ("until Phase C
  rewrites them"), so bodies are never installed. Widgets are instead handled by
  a ~150-line name-keyed Go switch (`renderStdlibComponent`) plus name-keyed
  logic in five other subsystems. This design is the "Phase C" rewrite for
  bubbletea.

## Prior probe findings (load-bearing)

1. Flipping `StdlibWrappers=false` alone is **inert**: bubbletea's `sngl.vbox`
   node resolves to the empty `lib/` stub (`pure=false`, `platImp=false`), so
   `passInlinePure` skips it. Bodies must be *installed* first (new-form).
2. Converting `bubbletea.sngl` to new form *does* trigger inlining, but the
   generated model breaks across **six** name-coupled subsystems:
   - **Model field allocation** (`compiler_ir.go` `analyzeIR`) keys on
     `n.Name == "input"` to allocate `inputN textinput.Model`.
   - **Init()** emits `textinput.Blink` based on input count.
   - **Update()** forwards `textinput.Model.Update(msg)` and syncs bind targets
     based on input count; button/checkbox handler routing
     (`emitIRButtonHandlers`) keys on names `"button"`/`"checkbox"` and event
     names `"click"`/`"change"`.
   - **View** (`renderStdlibComponent`) — the name switch.
   - **Focus** (`internal/lower/focus_order.go`) — `nodeEffectiveFocusable`
     keys on the `focusable` metadata prop (already prop-based — the good case),
     but handler routing downstream keys on names.
   - **Prop substitution** — caller args (`text(value=…)`) failed to reach the
     inlined `Styled(content=value)` node (rendered `Render("")`). The inliner's
     `substitute` *does* build a param→arg bindings map; the loss is in the
     new-form **platform-body check** not binding the param Sym — fixable.

## Architecture

### Primitive vocabulary (3)

After inlining, codegen sees only these node kinds, distinguished structurally:

- **`Layout`** — a container that joins its children. Carries a `join` value of
  an **enum** type (see below). Replaces the ad-hoc `VJoin`/`HJoin`.
- **`Styled`** — styled text content; optional focus and event bindings. Used
  for components with no Bubbles widget (text, badge, link, divider, avatar,
  image, button, checkbox, toggle, radio, chip, spacer, …).
- **`Widget`** — a Bubbles (or arbitrary library) model-widget wrapper, fully
  described by a `Model` blueprint record plus optional focus/binds/events.

### Blueprint record types (public API surface)

Declared in `bubbletea.sngl` and exported as part of the platform package so
user code can reference them. Shapes (SNGL structs/enums):

```sngl
enum JoinDir { vertical, horizontal }

struct Model {
    type string
    new string
    view string
    update string
    init string
    pkg string
}
// Go type, e.g. "textinput.Model"
// construction expr, e.g. "textinput.New()"
// view accessor, e.g. ".View()"
// update call, e.g. ".Update(msg)"
// optional Init() cmd, e.g. "textinput.Blink" ("" = none)
// Go import path to add

struct Focus {
    enabled bool
}

struct Bind {
    prop string
    get string
}
// sngl prop the widget two-way-binds (e.g. value)
// Go accessor read back into the bound var (e.g. ".Value()")

struct Event {
    on string
    key string
}
// sngl event name (e.g. "click", "change")
// terminal key that triggers it (e.g. "enter", "space")
// handler body is supplied by the user's @event handler at the call site,
// spliced in via the normal event-substitution path.

// Primitive nodes:
//   Layout(join=JoinDir.vertical) { slot }
//   Styled(content=expr, focus=Focus{...}, events=[Event{...}])
//   Widget(model=Model{...}, focus=Focus{...}, binds=[Bind{...}], events=[...], <extra widget props>)
```

Exact field sets may be refined during implementation, but the record names and
the enum-for-join decision are fixed.

### bubbletea.sngl (new form)

Every stdlib component is rewritten to:

```sngl
component sngl.vbox {
    platform bubbletea {
        Layout(join=JoinDir.vertical) { slot }
    }
}

component sngl.text {
    platform bubbletea {
        Styled(content=value) {}
    }
}

component sngl.input {
    platform bubbletea {
        Widget(model=Model{type = "textinput.Model", new = "textinput.New()", view = ".View()", update = ".Update(msg)", init = "textinput.Blink", pkg = "charm.land/bubbles/v2/textinput"}, focus=Focus{enabled = true}, binds=[Bind{prop = value, get = ".Value()"}], placeholder=placeholder) {}
    }
}

component sngl.button {
    platform bubbletea {
        Styled(content=text, focus=Focus{enabled = true}, events=[Event{on = "click", key = "enter"}]) {}
    }
}
```

The `sngl.`-prefixed new form makes `mergePlatformExtensions` install the body;
the body is pure (no vars/funcs/timers) so `passInlinePure` inlines it.

### Six subsystems → blueprint-driven

All read metadata off the inlined primitive nodes; none key on stdlib names.

1. **Field allocation** — scan inlined tree for `Widget` nodes; allocate one
   field `widgetN <model.type>` each (stable ordering by tree position).
2. **Init()** — for each `Widget` with `model.init != ""`, emit the init cmd
   (batched). Construction `widgetN = <model.new>` in the model constructor;
   first focusable widget gets `.Focus()`.
3. **Update()** — for each `Widget`: forward `widgetN, cmd = widgetN<model.update>`
   gated on its focus expr; after handlers, sync each `Bind` target var
   `= widgetN<bind.get>`. `Event` records route key messages to the spliced
   handler body, gated on focus. Tab/Shift-Tab drive the focus index (existing
   `passFocusOrder` machinery, made to read `Focus.enabled` off primitives).
4. **View()** — `Widget` → `widgetN<model.view>`; `Styled` → styled content
   (with `__focused` prefix when focusable, as today); `Layout` → lipgloss
   `JoinVertical`/`JoinHorizontal` by `join`.
5. **Focus** — `passFocusOrder` and consumers read `Focus.enabled` off the
   primitive (already prop-based via `focusable`; generalize to the record).
6. **Events** — generic: `Event{on,key}` + the user's call-site `@on` handler →
   a `case msg key == key && focused: <handler body>` arm. Replaces the
   hardcoded button/checkbox routing.

### Phase 0 prerequisite — param binding through new-form bodies

Fix the checker so a `platform bubbletea { … }` body's references to the stdlib
component's props (e.g. `value`, `text`, `placeholder`) resolve to the param
symbol, so `passInlinePure`'s `substituteParams` rebinds them to caller args.
Without this, inlined primitives render empty content (observed in probe 2).
Investigate `checkPendingExtensions` scoping (the new-form body is checked with
a temporarily-installed AST.Body; ensure the component's props are in scope).
This must land and be verified (a converted component renders its content)
before the subsystem rewrites.

### Bubbles wrapping plan

| SNGL component(s)                                                                        | Primitive               | Bubbles widget       |
|------------------------------------------------------------------------------------------|-------------------------|----------------------|
| vbox, stack, scroll, card, drawer, tooltip, popover, table*, tree*, menu*, radio*, modal | Layout (vertical)       | —                    |
| hbox, tabs, splitview, menubar, toolbar                                                  | Layout (horizontal)     | —                    |
| text, badge, link, divider, avatar, image, spacer, datepicker, progress†, spinner†       | Styled                  | — / (†→Widget later) |
| button, checkbox, toggle, chip, select                                                   | Styled + focus + events | —                    |
| input                                                                                    | Widget                  | textinput            |
| textarea                                                                                 | Widget                  | textarea             |

`*`/`†` Components with a natural Bubbles widget (list for menu/tree/select,
table, spinner, progress) are wrapped as `Widget` where it improves behavior;
the exact set is finalized in the plan. The abstraction makes adding each one a
blueprint record. The minimum bar for "done": every existing stdlib component
renders correctly through a primitive, `renderStdlibComponent` is deleted, and
input/textarea (at least) are real Bubbles widgets.

## Migration strategy

Component-by-component, not big-bang:

1. Land Phase 0 (param binding) and the blueprint record types + the generic
   blueprint-driven subsystems, behind the existing name switch as a fallback.
2. Convert components a few at a time to new form. Each converted component
   inlines via the **pure path** (no cap needed yet) → primitive → handled by
   the blueprint-driven subsystems; unconverted components still hit
   `renderStdlibComponent`. Both paths coexist during migration.
3. When all components are converted, enable the strict `NoStdlibWrappers` cap
   (enforces that every stdlib wrapper inlines; errors on any impure one),
   delete `renderStdlibComponent` and the dead `expandStdlibComponent` /
   `propVals` / `slotChildren` machinery.

## Verification

Byte-exact output preservation is **explicitly out** — behavior changes are
intended. Gates per step:

- `go build ./...` and `go tool verify` (0 failures).
- The bubbletea snapshot suite — goldens are **regenerated deliberately** and
  reviewed; a changed snapshot is acceptable when it reflects an intended
  behavior change, not a regression.
- Generated `examples/showcase` + `examples/todo` for bubbletea compile and
  render sanely (`sngl run`/snapshot inspection).
- The `tmp/gen-matrix.sh` harness is used for *diffing* (to see what changed),
  not as a pass/fail gate, since changes are expected.

## Risks / open questions

- **Param-binding fix scope.** If `checkPendingExtensions` scoping is deeper
  than expected, Phase 0 could grow. Mitigation: it's isolated and verifiable
  before the rest.
- **Focus/event generality.** The current focus machinery is intricate
  (`__focusID`, per-loop cursors). Generalizing it to read `Focus`/`Event` off
  primitives must preserve loop-focus behavior; covered by snapshot tests.
- **Bubbles widget breadth.** Wrapping list/table/spinner/progress each has its
  own init/update/message quirks; scope the first cut to input + textarea, add
  others as follow-on blueprint records.
- **android** is the analogous platform (`renderStdlibComposable`); out of scope
  here, but the blueprint record types should be designed so the same approach
  ports to Compose later.
