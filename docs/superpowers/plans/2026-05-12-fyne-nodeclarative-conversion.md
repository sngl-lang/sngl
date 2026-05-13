# Fyne NoDeclarative Conversion Implementation Plan (Plan B.2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the fyne side of the reactivity-lowering consolidation: enable `NoDeclarative` so the entire component body becomes a flat lowered-IR stream, rewrite fyne's `BuildUI` emission onto `WalkLowered + fyneTranslator`, resolve the `__root` sentinel binding, address Plan B reviewer findings, and rip the parallel-reactive pipeline (`renderStmt`/`renderConditional`/`renderFor`/`updaters`/`FindAffected`/`lateReactive`/`localMode`/`updateIf`/`updateFor` + scaffold updater fields).

**Architecture:** Today fyne consumes a NodeInst tree via `renderStmt` plus a lowered-IR overlay for reactivity. Plan B's `fyneTranslator` is wired only for `__renderSlot<N>` Funcs. Plan B.2 turns on `NoDeclarative` so the component body itself becomes intrinsic calls (`lower.CreateNode`, `AppendChild`, `AttachHandler`, prop Assigns), then routes the whole BuildUI body through the existing `fyneStmtDispatch` plus a small top-level-collection wrapper. The blueprint system stays as the per-tag widget-and-setter dispatcher (consulted by `fyneTranslator`); everything else parallel to that comes out.

**Tech Stack:** Go, fyne v2, the codegen `IntrinsicTranslator` interface, `codegen.WalkLowered`, fyne's existing `blueprint.go` widget table, `lib/lower.sngl` namespace.

**Spec:** `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md` §2.2 (fyne conversion).

**Predecessor:** Plan B (`docs/superpowers/plans/2026-05-12-fyne-intrinsic-translator.md`) landed Phase 1-2 (`fyneTranslator` built, slot-Func emission validated). All Plan B commits are on `main`.

**Successor:** Plans C/D apply the same conversion to gtk4 / html. Plan E is the final audit.

---

## What Plan B left undone (Plan B.2's punch list)

From Plan B's final reviewer report:

| # | Finding | Plan B.2 task |
|---|---------|---------------|
| 1 | `__n`/`__slot` prefix sniffing in fyne (fragile, breaks on user names starting with `__n*`) | Tasks 1-2 |
| 2 | `fyneStmtDispatch` has 8 special cases (slot reset, ListPush, For/__slot, For/general, If, element-ref Assign, CreateNode LocalVar, AppendChild/Remove/Attach CallStmts) — interface under-specified | Task 3 (widen interface), Tasks 4-5 (collapse special-cases) |
| 3 | `container, _ := parent.(*fyne.Container)` runtime type-assertion leaks into generated code | Task 6 (typed-parent slot signature) |
| 4 | Integration test only string-matches; emitted Go never compile-checked | Task 7 |
| 5 | `__root` sentinel unresolved in BuildUI call sites | Tasks 8-9 |
| 6 nit | `zeroArgsFor` hardcoded constructor arity in fyneTranslator | Task 10 |
| 6 nit | `splitTrLines` string round-trip | Task 11 |

Then the bigger structural work:

- Task 12: enable `NoDeclarative` on fyne.
- Tasks 13-18: rewrite BuildUI emission onto `WalkLowered + fyneTranslator`.
- Tasks 19-24: remove the parallel-reactive pipeline.
- Tasks 25-26: final verification.

26 tasks total.

---

## File Structure

**Modify (substantially):**
- `codegen/platform/fyne/compiler_ir.go` — kills tree-walk emission for BuildUI; routes through `WalkLowered`.
- `codegen/platform/fyne/view_ir.go` — most of this file goes away. The pieces that survive (blueprint constructor arg synthesis, entry-sync recording) move into `compiler_ir.go` or `intrinsic_translator.go`.
- `codegen/platform/fyne/fyne.go` — `Capabilities()` returns `{NoReactivity: true, NoDeclarative: true}`.
- `codegen/platform/fyne/scaffold.go` — drop `UpdaterNames`, `AffectedUpdaters`, `doRefresh`-related fields.
- `codegen/platform/fyne/templates/model.go.tmpl` — drop the `doRefresh` method body, the timer-affected-updater injection.
- `codegen/platform/fyne/intrinsic_translator.go` — wider interface methods, no string-sniffing.
- `codegen/platform/fyne/blueprint.go` — store `Constructor.ZeroArgs` for slot-time emission.
- `codegen/intrinsic_walker.go` — interface extension.
- `ir/ir.go` (or `ir/expr.go`) — add `Synthesized bool` to `*ir.Ident`.
- `internal/lower/reactivity.go` — set `Synthesized: true` on synthesized Idents at emission time.

**Delete (entirely):**
- `view_ir.go` after its blueprint-handling moves out. (Or rename to `blueprint_dispatch.go` if the surviving fragment is small.)

**No structural changes** to `internal/lower/declarative.go` — it already produces the needed intrinsic-call shape.

---

## Phase A: Reviewer-finding cleanups

### Task 1: Add `Synthesized bool` to `*ir.Ident`

**Files:**
- Modify: `ir/expr.go` (or wherever `*ir.Ident` is declared — `grep -n "type Ident struct" ir/*.go`).
- Modify: `internal/lower/reactivity.go` — set the flag at every synthesized-Ident emission site.

**Goal:** Ident references to synthesized vars (`__slot<N>`) and to widget refs (`__nN`) carry an explicit synthetic marker, so fyne can stop string-sniffing names.

- [ ] **Step 1: Locate `*ir.Ident`'s struct definition**

Run: `grep -n "type Ident struct" ir/*.go`

Open the file at that line.

- [ ] **Step 2: Add the field**

Add to the `Ident` struct, alongside the existing `IsElementRef bool` field (mirror its style):

```go
// Synthesized marks Idents emitted by a lowering pass for refs to
// pass-introduced Vars or Funcs (e.g. __slot<N>, __renderSlot<N>,
// __n<N> widget refs). Codegen consumers use this to distinguish
// pass-synthesized refs from user-named identifiers without string
// prefix matching.
Synthesized bool `json:"-"`
```

- [ ] **Step 3: Set the flag in passReactivity emissions**

In `internal/lower/reactivity.go`, search for every `&ir.Ident{Name: "__` literal and add `Synthesized: true`. Specific sites (line numbers approximate — locate via grep):

```bash
grep -n "&ir.Ident{Name:" internal/lower/reactivity.go
```

Sites that need the flag (verify via grep):
- `synthesizeSlotVar` returns `&ir.Var{...}` — no Ident there; this Var's IsElementRef-style markers come elsewhere.
- `synthesizeRenderSlotFunc` constructs Idents for slotID (teardown Iter and the reset target) and for the parent param. ALL slotID refs get `Synthesized: true`. The parent param ref (`Sym: parentParam`) does NOT — it's a real parameter.
- `slotCall`'s `__root` fallback Ident → `Synthesized: true`.
- `updatersFor`'s slot-updater splice → `__root` Ident gets `Synthesized: true`.
- `synthesizeRenderSlotFunc`'s `pushToSlot` closure constructs slotID Idents → `Synthesized: true`.
- `synthesizeRenderSlotFunc`'s `entryVar` ("__entry") Iter-binding name and loop body — `__entry` Ident → `Synthesized: true`.
- `recordSlotParent`'s `cloneIdent` clone — preserve the flag from the original.

Concrete edits (each is a one-line `Synthesized: true,` addition):

```go
// In synthesizeRenderSlotFunc, teardown For body:
teardown := &ir.For{
    Key: entryVar,
    Iter: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn), Synthesized: true},
    Body: []ir.Stmt{
        &ir.CallStmt{Call: &ir.Call{
            // ... RemoveChild call ...
            Args: []ir.CallArg{
                {Value: &ir.Ident{Name: parentParam.Name, Type: ir.TypDyn, Sym: parentParam, IsElementRef: true}}, // NOT synthesized — real param
                {Value: &ir.Ident{Name: entryVar, Type: ir.TypDyn, Synthesized: true}},
            },
        }},
    },
}

// Reset:
reset := &ir.Assign{
    Target: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn), Synthesized: true},
    Op:     ast.AssignSet,
    Value:  &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
}
```

```go
// In renderSlotBody's pushToSlot closure:
return &ir.Assign{
    Target: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn), Synthesized: true},
    Op:     ast.AssignSet,
    Value: &ir.Call{
        // ... ListPush ...
        Args: []ir.CallArg{
            {Value: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn), Synthesized: true}},
            {Value: &ir.Ident{Name: nodeID, Type: ir.TypDyn, IsElementRef: true, Synthesized: true}},
        },
    },
}
```

```go
// In slotCall, the __root fallback:
parentRef = &ir.Ident{Name: "__root", Type: ir.TypDyn, IsElementRef: true, Synthesized: true}
```

```go
// In updatersFor, slot-call splice fallback:
parentRef := slot.ParentRef
if parentRef == nil {
    parentRef = &ir.Ident{Name: "__root", Type: ir.TypDyn, IsElementRef: true, Synthesized: true}
}
```

The `__n<N>` widget refs are emitted in `internal/lower/declarative.go` (see `lowerNode` and `LowerNodeForSlot`). Set `Synthesized: true` on every `&ir.Ident{Name: id, ...}` and `&ir.Ident{Name: cn.ID, ...}` in those functions. Look for `IsElementRef: true` — those are the widget refs that should be marked.

- [ ] **Step 4: Write a regression test**

Append to `internal/lower/reactivity_structural_test.go`:

```go
func TestSlotIdentsAreSynthesized(t *testing.T) {
	src := `
component main {
    var visible bool = true
    button(@click { visible = !visible })
    if visible {
        text(value="hi")
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	// The CallStmt __renderSlot0(__root) at the top level should have
	// its __root arg marked Synthesized.
	var checked bool
	for _, s := range comp.Body {
		cs, ok := s.(*ir.CallStmt)
		if !ok || cs.Call == nil || cs.Call.Func == nil || cs.Call.Func.Name != "__renderSlot0" {
			continue
		}
		if len(cs.Call.Args) != 1 {
			t.Fatalf("expected 1 arg on __renderSlot0 call, got %d", len(cs.Call.Args))
		}
		id, ok := cs.Call.Args[0].Value.(*ir.Ident)
		if !ok {
			t.Fatalf("expected *ir.Ident arg, got %T", cs.Call.Args[0].Value)
		}
		if !id.Synthesized {
			t.Errorf("expected __root Ident to have Synthesized=true; got false")
		}
		checked = true
	}
	if !checked {
		t.Fatal("did not find __renderSlot0 CallStmt at top level")
	}
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/lower/ -run TestSlotIdentsAreSynthesized -v`
Expected: PASS.

Run: `go test ./internal/lower/...`
Expected: PASS (no regressions).

Run: `go tool verify 2>&1 | grep -E "^(---|not ok)" | tail -3`
Expected: 33/109 baseline preserved.

- [ ] **Step 6: Commit**

```bash
git add ir/expr.go internal/lower/reactivity.go internal/lower/declarative.go internal/lower/reactivity_structural_test.go
git commit -m "ir+lower: mark synthesized Idents with Synthesized flag

passReactivity and passDeclarative now set Synthesized=true on every
*ir.Ident they emit for refs to pass-introduced symbols (__slot<N>,
__renderSlot<N>, __nN widget refs, __root sentinel). Codegen
consumers can consult the flag instead of prefix-sniffing names.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Replace fyne string-sniffing with Synthesized checks

**Files:**
- Modify: `codegen/platform/fyne/intrinsic_translator.go` — `modelRef` consults the flag.
- Modify: `codegen/platform/fyne/compiler_ir.go` — `isSlotReset`, `isSlotListPush`, the For-iter slot check all use the flag.

The translator API is `OnAppendChild(parent, child string)` etc. — takes strings, not Idents. So the FLAG check happens at the dispatch site (`fyneStmtDispatch`), and the dispatched name carries a hint via convention OR we add a struct-level marker.

Simplest path: extend the strings passed in with a sentinel, OR (better) pre-resolve the names at dispatch time and pass them down as `(name, isSynthesized)` tuples to the translator. Easier alternative: keep using strings but flip the lookup so `modelRef` consults an explicit *idTags-style* synthetic-set populated at dispatch time.

Even simpler: have `fyneStmtDispatch` resolve the names AT DISPATCH SITE using the Ident's `Synthesized` flag, then pass the appropriately prefixed (`m.foo` or `foo`) string to the translator. The translator methods become dumb concatenators.

Recommend the last option. Concrete steps:

- [ ] **Step 1: Update fyneStmtDispatch to pre-qualify**

In `codegen/platform/fyne/compiler_ir.go`, find `fyneStmtDispatch`'s `*ir.LocalVar` case. Change the `tag, _ := lowerStringArg(...)` line and add an extra resolution helper:

```go
// identRef returns "m.<name>" when the Ident references a synthesized
// symbol (a passReactivity-emitted ref), or the bare name otherwise.
// Used to decide whether a name resolves through the Model receiver.
func identRef(e ir.Expr) string {
	id, ok := e.(*ir.Ident)
	if !ok {
		return ""
	}
	if id.Synthesized {
		return "m." + id.Name
	}
	return id.Name
}
```

(Put this alongside `lowerIdentArg` / other small helpers.)

Then in `fyneStmtDispatch`'s `*ir.CallStmt` cases, replace `lowerIdentArg(...)` followed by `modelRef(name)` with direct `identRef(call.Args[i].Value)`:

```go
case *ir.CallStmt:
    if n.Call != nil {
        switch {
        case isLowerIntrinsic(n.Call, "AppendChild"):
            child := identRef(n.Call.Args[1].Value)
            return splitTrLines(tr.OnAppendChild("container", child))
        case isLowerIntrinsic(n.Call, "RemoveChild"):
            child := identRef(n.Call.Args[1].Value)
            return splitTrLines(tr.OnRemoveChild("container", child))
        case isLowerIntrinsic(n.Call, "AttachHandler"):
            node := identRef(n.Call.Args[0].Value)
            evt, _ := lowerStringArg(n.Call, 1)
            h := identRef(n.Call.Args[2].Value)
            return splitTrLines(tr.OnAttachHandler(node, evt, h))
        }
    }
```

- [ ] **Step 2: Drop the `modelRef` helper**

In `codegen/platform/fyne/intrinsic_translator.go`, delete `modelRef`. Replace every callsite (inside translator methods) with the bare name — the dispatch wrapper now passes prequalified names. So:

```go
func (t *fyneTranslator) OnAppendChild(parent, child string) string {
	return parent + ".Add(" + child + ")\n"
}

func (t *fyneTranslator) OnRemoveChild(parent, child string) string {
	return parent + ".Remove(" + child + ")\n"
}
```

(No more `modelRef(child)`.)

`OnCreateNode` already emits `m.<id>` from the bare `id` string — adjust to NOT add `m.` since the dispatch now passes the unqualified name when handing `id` to `OnCreateNode`:

Hmm actually `OnCreateNode` receives `id` as the LocalVar name from `fyneStmtDispatch`'s `*ir.LocalVar` case. That LocalVar's Name is `__n0` — and we WANT it to become `m.__n0` because LocalVars from lower.CreateNode resolve as Model fields. So `OnCreateNode` should keep emitting `m.<id>` directly. Don't touch it.

Same for `OnPropAssign` — it receives the Select's operand Ident name (e.g. `__n0`); it should emit `m.<id>.<setter>(...)`. The Synthesized check happens implicitly because we only call OnPropAssign for Selects on IsElementRef Idents.

Hmm, but `OnPropAssign` currently uses `modelRef(nodeID)` to qualify. If we drop modelRef, we need to inline `m.` directly:

```go
return "m." + nodeID + binding.Target + "(" + val + ")\n"
```

OK so the rule simplifies:
- `OnCreateNode(id, tag)` always emits `m.<id> = ctor()` — id is always synthetic.
- `OnPropAssign(nodeID, prop, ...)` always emits `m.<nodeID>.setter(...)` — nodeID is always synthetic.
- `OnAttachHandler(node, ...)` receives a string from `identRef(...)` which already has `m.` if needed.
- `OnAppendChild`/`OnRemoveChild` receive pre-qualified strings.

That's cleaner. Apply the changes.

- [ ] **Step 3: Replace `isSlotReset` / `isSlotListPush` with flag checks**

These helpers check `strings.HasPrefix(id.Name, "__slot")`. Replace with `id.Synthesized`:

```go
func isSlotReset(a *ir.Assign) bool {
	id, ok := a.Target.(*ir.Ident)
	if !ok || !id.Synthesized {
		return false
	}
	ll, ok := a.Value.(*ir.ListLit)
	if !ok {
		return false
	}
	return len(ll.Elems) == 0
}

func isSlotListPush(a *ir.Assign) bool {
	id, ok := a.Target.(*ir.Ident)
	if !ok || !id.Synthesized {
		return false
	}
	call, ok := a.Value.(*ir.Call)
	if !ok || call.Func == nil || call.Func.Intrinsic != "ListPush" {
		return false
	}
	return len(call.Args) == 2
}
```

In the For case:

```go
case *ir.For:
    iterExpr := ""
    if id, ok := n.Iter.(*ir.Ident); ok && id.Synthesized {
        iterExpr = "m." + id.Name
    } else {
        iterExpr = gc.EvalExpr(n.Iter)
    }
    // ... rest unchanged
```

- [ ] **Step 4: Run translator unit tests**

Run: `go test ./codegen/platform/fyne/ -run TestFyneTranslator -v`
Expected: PASS — but several tests build Idents WITHOUT the Synthesized flag. They'll fail. Update each test that builds an `&ir.Ident{Name: "__slot..."}` or `&ir.Ident{Name: "__n..."}` to include `Synthesized: true`.

Specifically:
- `TestFyneStmtDispatch_SlotListPush` — `Target: &ir.Ident{Name: "__slot0", Synthesized: true}` and inside Args `{Value: &ir.Ident{Name: "__slot0", Synthesized: true}}`, `{Value: &ir.Ident{Name: "__n0", IsElementRef: true, Synthesized: true}}`.
- `TestFyneStmtDispatch_SlotReset` — `Target: &ir.Ident{Name: "__slot0", Synthesized: true}`.
- `TestFyneStmtDispatch_SlotTeardownFor` — `Iter: &ir.Ident{Name: "__slot0", Type: ir.ListOf(ir.TypDyn), Synthesized: true}`. Body's `{Value: &ir.Ident{Name: "__entry", Synthesized: true}}`.
- `TestFyneStmtDispatch_IfRecurses` — same pattern.
- `TestFyneStmtDispatch_ReactiveForRecurses` — non-slot iter stays bare (no Synthesized). Body's `__n5` LocalVar name stays the same (LocalVars don't carry Synthesized — only Idents).

Update each test to include the flag.

- [ ] **Step 5: Run dispatch tests**

Run: `go test ./codegen/platform/fyne/...`
Expected: PASS.

- [ ] **Step 6: Run integration test**

Run: `go test ./codegen/platform/fyne/ -run TestIntegration -v`
Expected: PASS — the integration test exercises real lowering output, which now has Synthesized flags set.

- [ ] **Step 7: Commit**

```bash
git add codegen/platform/fyne/intrinsic_translator.go codegen/platform/fyne/compiler_ir.go codegen/platform/fyne/intrinsic_translator_test.go
git commit -m "fyne: consult Ident.Synthesized instead of name prefix matching

modelRef + isSlotReset + isSlotListPush + the For iter check no
longer string-sniff for __n/__slot prefixes. Dispatch now pre-
qualifies synthesized names through identRef() and the translator
methods become straight concatenators.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Widen `IntrinsicTranslator` interface with structural methods

**Files:**
- Modify: `codegen/intrinsic_walker.go`
- Modify: `codegen/platform/fyne/intrinsic_translator.go`
- Test: `codegen/intrinsic_walker_test.go`

The reviewer flagged that `fyneStmtDispatch`'s 8 special cases live entirely in fyne. Other platforms (gtk4, html) would copy-paste the same dispatcher. Move the four lowering-output structural patterns (slot reset, slot list-push, slot teardown For, slot gate If) into the shared interface so platforms only implement the leaf emissions.

The new interface methods:

- `OnSlotReset(slotID string) string` — emit the "clear slot accumulator" code.
- `OnSlotAppend(slotID, childID string) string` — emit "push child ref into accumulator."
- `OnSlotTeardown(slotID, entryVar, bodyEmissions string) string` — emit the teardown loop (wraps `RemoveChild` calls in the body).
- The slot gate If recursion stays in the shared walker (not platform-specific).

Wait — actually a simpler design: move the recursion into `WalkLowered` itself, and have it call back into the translator for leaf shapes. The translator's interface remains five methods (the original four + a `OnDefault(stmt) []string` fallback for stmts WalkLowered doesn't recognize). That moves Less platform-specific code.

Recommend the second design. Concrete changes:

- [ ] **Step 1: Extend the interface with `OnDefault`**

In `codegen/intrinsic_walker.go`, add to `IntrinsicTranslator`:

```go
// OnDefault handles statements the walker doesn't recognize as lower
// intrinsics. The translator delegates to its target language's stmt
// emitter (e.g. fyne uses golang.GoIRContext.EvalStmt). Returning nil
// means "no emission" — preserves the current silent-skip semantics
// for unrecognized shapes during build-out.
OnDefault(stmt ir.Stmt) []string

// OnSlotReset emits the "clear slot accumulator" code for a Plan A
// __slot<N> = [] assignment. slotID is the slot's symbol name; the
// translator decides whether to prefix with the Model receiver.
OnSlotReset(slotID string) string

// OnSlotAppend emits the "push child ref into accumulator" code for
// the Plan A __slot<N> = stdlib.ListPush(__slot<N>, child) pattern.
OnSlotAppend(slotID, childID string) string
```

- [ ] **Step 2: Move slot-reset and slot-list-push detection into WalkLowered**

In `codegen/intrinsic_walker.go`, extend `walkOne` to detect the two shapes and dispatch to the new methods. The detection rule: the Assign target is a Synthesized Ident (the slot accumulator). Use the `Synthesized` flag added in Task 1.

```go
case *ir.Assign:
    if id, ok := n.Target.(*ir.Ident); ok && id.Synthesized {
        // Slot reset: __slotN = []
        if ll, ok := n.Value.(*ir.ListLit); ok && len(ll.Elems) == 0 {
            return t.OnSlotReset(id.Name)
        }
        // Slot append: __slotN = stdlib.ListPush(__slotN, #childID)
        if call, ok := n.Value.(*ir.Call); ok && call.Func != nil && call.Func.Intrinsic == "ListPush" && len(call.Args) == 2 {
            childArg, ok := call.Args[1].Value.(*ir.Ident)
            if ok {
                return t.OnSlotAppend(id.Name, childArg.Name)
            }
        }
    }
    if sel, ok := n.Target.(*ir.Select); ok {
        // ... existing element-ref Assign branch ...
    }
```

- [ ] **Step 3: Add Walker-side fallback to OnDefault**

At the bottom of `walkOne`:

```go
// Unrecognized — delegate to the translator's OnDefault.
defaultLines := t.OnDefault(s)
return strings.Join(defaultLines, "\n") + "\n"
```

(Or extend the return type of WalkLowered to return `[]string` and skip the string-join; see Task 11.)

- [ ] **Step 4: Implement the new methods on fyneTranslator**

In `codegen/platform/fyne/intrinsic_translator.go`:

```go
func (t *fyneTranslator) OnDefault(stmt ir.Stmt) []string {
	return t.gc.EvalStmt(stmt)
}

func (t *fyneTranslator) OnSlotReset(slotID string) string {
	return "m." + slotID + " = nil\n"
}

func (t *fyneTranslator) OnSlotAppend(slotID, childID string) string {
	return "m." + slotID + " = append(m." + slotID + ", m." + childID + ")\n"
}
```

Compile-time interface check:

```go
var _ codegen.IntrinsicTranslator = (*fyneTranslator)(nil)
```

(Already there; no change.)

- [ ] **Step 5: Replace fyneStmtDispatch with WalkLowered + small wrapper**

Currently `emitIRSlotFunc` calls `fyneStmtDispatch` for each stmt. Now it can call a wrapper that uses `WalkLowered` for top-level intrinsic detection and special-cases For/If recursion (which the walker doesn't yet handle).

Hmm — actually `WalkLowered` walks a list of stmts. The For/If cases inside need recursion. The cleanest path: extend `WalkLowered` to recurse into For/If bodies too, treating those as structural delegation rather than translator hooks.

For Plan B.2's scope: KEEP `fyneStmtDispatch` for the For/If recursion (still platform-specific because of the slot-iter rewrite), but have its leaf cases call into `WalkLowered(stmt-as-list, translator)`. This shifts the leaf dispatching out of fyne.

Pragmatic compromise: leave `fyneStmtDispatch` as the For/If wrapper, but inside it the slot-reset / slot-listpush / element-ref Assign / lower.* CallStmt / CreateNode LocalVar cases all become `WalkLowered([]ir.Stmt{s}, tr)` (single-stmt walk). The walker does the dispatch. fyne keeps only the recursion.

Concrete edits to fyneStmtDispatch:

```go
func fyneStmtDispatch(s ir.Stmt, tr *fyneTranslator, gc *golang.GoIRContext) []string {
	switch n := s.(type) {
	case *ir.For:
		// Recurse body through fyneStmtDispatch so any intrinsic calls
		// translate, regardless of slot iter vs general iter.
		iterExpr := ""
		if id, ok := n.Iter.(*ir.Ident); ok && id.Synthesized {
			iterExpr = "m." + id.Name
		} else {
			iterExpr = gc.EvalExpr(n.Iter)
		}
		var bodyLines []string
		for _, s := range n.Body {
			for _, l := range fyneStmtDispatch(s, tr, gc) {
				bodyLines = append(bodyLines, "\t"+l)
			}
		}
		lines := []string{"for _, " + n.Key + " := range " + iterExpr + " {"}
		lines = append(lines, bodyLines...)
		return append(lines, "}")
	case *ir.If:
		cond := gc.EvalExpr(n.Cond)
		var lines []string
		lines = append(lines, "if "+cond+" {")
		for _, s := range n.Body {
			for _, l := range fyneStmtDispatch(s, tr, gc) {
				lines = append(lines, "\t"+l)
			}
		}
		if len(n.Else) > 0 {
			lines = append(lines, "} else {")
			for _, s := range n.Else {
				for _, l := range fyneStmtDispatch(s, tr, gc) {
					lines = append(lines, "\t"+l)
				}
			}
		}
		return append(lines, "}")
	default:
		// Single-stmt walk: WalkLowered handles all leaf shapes via the
		// translator (CreateNode LocalVar, lower.* CallStmt, element-
		// ref Assign, slot reset, slot append) and falls back to
		// OnDefault → gc.EvalStmt for anything else.
		_ = n
		out := codegen.WalkLowered([]ir.Stmt{s}, tr)
		if out == "" {
			return nil
		}
		return strings.Split(strings.TrimRight(out, "\n"), "\n")
	}
}
```

Remove the now-unused helpers `isSlotReset`, `isSlotListPush` from compiler_ir.go. Remove `isLowerIntrinsic`, `lowerStringArg`, `lowerIdentArg`, `splitTrLines` IF they're not used elsewhere (`splitTrLines` likely still needed for the For/If body recursion's inner emission flow — verify).

Actually `splitTrLines` is the translator-output → []string converter. After widening the interface to return []string directly (Task 11), it goes away. For now it stays.

- [ ] **Step 6: Update walker tests**

In `codegen/intrinsic_walker_test.go`, the existing trace type needs `OnDefault`, `OnSlotReset`, `OnSlotAppend` methods:

```go
func (t *trace) OnDefault(stmt ir.Stmt) []string {
	t.add("default %T", stmt)
	return nil
}
func (t *trace) OnSlotReset(slotID string) string {
	t.add("reset %s", slotID)
	return ""
}
func (t *trace) OnSlotAppend(slotID, childID string) string {
	t.add("append-slot %s %s", slotID, childID)
	return ""
}
```

Add tests:

```go
func TestWalkLowered_SlotReset(t *testing.T) {
	stmt := &ir.Assign{
		Target: &ir.Ident{Name: "__slot0", Synthesized: true},
		Value:  &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
	}
	tr := &trace{}
	WalkLowered([]ir.Stmt{stmt}, tr)
	if len(tr.lines) != 1 || tr.lines[0] != "reset __slot0" {
		t.Errorf("dispatch mismatch: %v", tr.lines)
	}
}

func TestWalkLowered_SlotAppend(t *testing.T) {
	listPush := &ir.Func{Name: "ListPush", Intrinsic: "ListPush"}
	stmt := &ir.Assign{
		Target: &ir.Ident{Name: "__slot0", Synthesized: true},
		Value: &ir.Call{
			Func: listPush,
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__slot0", Synthesized: true}},
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true, Synthesized: true}},
			},
		},
	}
	tr := &trace{}
	WalkLowered([]ir.Stmt{stmt}, tr)
	if len(tr.lines) != 1 || tr.lines[0] != "append-slot __slot0 __n0" {
		t.Errorf("dispatch mismatch: %v", tr.lines)
	}
}
```

- [ ] **Step 7: Run tests**

Run: `go test ./codegen/... ./internal/lower/...`
Expected: PASS.

If `splitTrLines` undefined or other build errors, fix them as you go. Don't remove helpers until you confirm they're unused.

- [ ] **Step 8: Commit**

```bash
git add codegen/intrinsic_walker.go codegen/intrinsic_walker_test.go codegen/platform/fyne/intrinsic_translator.go codegen/platform/fyne/compiler_ir.go
git commit -m "codegen+fyne: widen IntrinsicTranslator with slot+default hooks

Adds OnDefault (fallback to language EvalStmt), OnSlotReset
(__slotN = []), OnSlotAppend (ListPush). WalkLowered now handles
these shapes itself, so fyneStmtDispatch shrinks to just the
For/If structural recursion (which still needs platform context
for the slot-iter rewrite).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Move structural recursion (For/If) into WalkLowered

**Files:**
- Modify: `codegen/intrinsic_walker.go`
- Modify: `codegen/platform/fyne/compiler_ir.go` (delete `fyneStmtDispatch`)
- Modify: `codegen/platform/fyne/intrinsic_translator.go` (new method)

The For/If recursion in fyneStmtDispatch is the last platform-specific piece. The slot-iter rewrite (`range m.__slotN` vs `range expr`) IS platform-specific because it knows about Model field qualification — but we can express that via a translator method too.

Add to interface:

- `OnSlotIter(slotID string) string` — translator returns the platform-side iter expression for a slot ident. For fyne: `"m." + slotID`.

Then `WalkLowered`'s For case rewrites:

```go
case *ir.For:
    var iterExpr string
    if id, ok := n.Iter.(*ir.Ident); ok && id.Synthesized {
        iterExpr = t.OnSlotIter(id.Name)
    } else {
        // Defer to OnDefault — let the language emit the iter expression
        // inline. (Wrapping non-slot Fors stays as raw EvalStmt path.)
        return strings.Join(t.OnDefault(s), "\n") + "\n"
    }
    // ... emit `for _, key := range iterExpr {` + recurse body via WalkLowered ...
```

Hmm — this is getting tangled. The non-slot For case (e.g. ranging a Model field) needs intrinsic-aware body recursion too, not just OnDefault. Let me re-think.

Simpler: WalkLowered ALWAYS handles For/If structurally (recursing the body through WalkLowered), but uses OnSlotIter for the iter expression when the Iter is synthesized, else asks the translator for a "generic iter expression" via a new `OnExpr(ir.Expr) string` method.

But OnExpr is essentially the language's expression evaluator (gc.EvalExpr for Go). Exposing that through the translator is fine.

Concrete design:

```go
type IntrinsicTranslator interface {
    // ... existing methods ...
    OnDefault(stmt ir.Stmt) []string
    OnSlotReset(slotID string) string
    OnSlotAppend(slotID, childID string) string

    // OnIter returns the platform-side expression for a For loop's
    // Iter. For synthesized slot accumulators, this typically
    // qualifies through the Model receiver. Otherwise the translator
    // delegates to its language expression emitter.
    OnIter(iter ir.Expr) string

    // OnCond returns the platform-side expression for an If condition.
    // Almost always delegates to the language expression emitter.
    OnCond(cond ir.Expr) string
}
```

In WalkLowered's `walkOne`:

```go
case *ir.For:
    iter := t.OnIter(n.Iter)
    body := WalkLowered(n.Body, t) // recurse
    return "for _, " + n.Key + " := range " + iter + " {\n" + indent(body) + "}\n"

case *ir.If:
    cond := t.OnCond(n.Cond)
    body := WalkLowered(n.Body, t)
    out := "if " + cond + " {\n" + indent(body) + "}"
    if len(n.Else) > 0 {
        out += " else {\n" + indent(WalkLowered(n.Else, t)) + "}"
    }
    return out + "\n"
```

(`indent` is a small private helper that prefixes each line with `\t`.)

fyne's implementation:

```go
func (t *fyneTranslator) OnIter(iter ir.Expr) string {
	if id, ok := iter.(*ir.Ident); ok && id.Synthesized {
		return "m." + id.Name
	}
	return t.gc.EvalExpr(iter)
}

func (t *fyneTranslator) OnCond(cond ir.Expr) string {
	return t.gc.EvalExpr(cond)
}
```

`fyneStmtDispatch` goes away entirely. `emitIRSlotFunc` calls `codegen.WalkLowered(fn.Block, tr)` directly.

- [ ] **Step 1: Extend the interface**

Add `OnIter` and `OnCond` to `IntrinsicTranslator` in `codegen/intrinsic_walker.go`.

- [ ] **Step 2: Move For/If into walkOne**

In `codegen/intrinsic_walker.go`, add to `walkOne`:

```go
case *ir.For:
    iter := t.OnIter(n.Iter)
    body := WalkLowered(n.Body, t)
    return "for _, " + n.Key + " := range " + iter + " {\n" + indentLines(body) + "}\n"

case *ir.If:
    cond := t.OnCond(n.Cond)
    body := WalkLowered(n.Body, t)
    out := "if " + cond + " {\n" + indentLines(body) + "}"
    if len(n.Else) > 0 {
        out += " else {\n" + indentLines(WalkLowered(n.Else, t)) + "}"
    }
    return out + "\n"
```

Add `indentLines`:

```go
func indentLines(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = "\t" + l
	}
	return strings.Join(lines, "\n") + "\n"
}
```

- [ ] **Step 3: Implement fyne's OnIter/OnCond**

Add to `codegen/platform/fyne/intrinsic_translator.go`:

```go
func (t *fyneTranslator) OnIter(iter ir.Expr) string {
	if id, ok := iter.(*ir.Ident); ok && id.Synthesized {
		return "m." + id.Name
	}
	return t.gc.EvalExpr(iter)
}

func (t *fyneTranslator) OnCond(cond ir.Expr) string {
	return t.gc.EvalExpr(cond)
}
```

- [ ] **Step 4: Replace emitIRSlotFunc's dispatch loop with WalkLowered**

In `codegen/platform/fyne/compiler_ir.go`, replace `emitIRSlotFunc`'s `for _, stmt := range fn.Block { lines := fyneStmtDispatch(...) ... }` loop with:

```go
body := codegen.WalkLowered(fn.Block, tr)
for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
    b.WriteString("\t")
    b.WriteString(l)
    b.WriteString("\n")
}
```

- [ ] **Step 5: Delete fyneStmtDispatch and the now-unused helpers**

Remove `fyneStmtDispatch`, `isLowerIntrinsic`, `lowerStringArg`, `lowerIdentArg`, `splitTrLines`, `isSlotReset`, `isSlotListPush`, `identRef` from `compiler_ir.go`. (Some of these moved into walker; others are dead now.)

- [ ] **Step 6: Update walker tests for structural cases**

Add to `codegen/intrinsic_walker_test.go`:

```go
func (t *trace) OnIter(iter ir.Expr) string {
	if id, ok := iter.(*ir.Ident); ok {
		return id.Name
	}
	return "?"
}
func (t *trace) OnCond(cond ir.Expr) string {
	if id, ok := cond.(*ir.Ident); ok {
		return id.Name
	}
	return "?"
}

func TestWalkLowered_ForIfRecursion(t *testing.T) {
	// for x = items { if x { ... AppendChild ... } }
	appendChild := &ir.Func{Name: "AppendChild", Intrinsic: "AppendChild"}
	stmt := &ir.For{
		Key: "x",
		Iter: &ir.Ident{Name: "items"},
		Body: []ir.Stmt{
			&ir.If{
				Cond: &ir.Ident{Name: "x"},
				Body: []ir.Stmt{
					&ir.CallStmt{Call: &ir.Call{
						Func: appendChild,
						Args: []ir.CallArg{
							{Value: &ir.Ident{Name: "p", IsElementRef: true}},
							{Value: &ir.Ident{Name: "c", IsElementRef: true}},
						},
					}},
				},
			},
		},
	}
	tr := &trace{}
	WalkLowered([]ir.Stmt{stmt}, tr)
	got := strings.Join(tr.lines, "\n")
	want := "append p c"
	if got != want {
		t.Errorf("structural recursion mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}
```

- [ ] **Step 7: Run tests**

Run: `go test ./codegen/... ./codegen/platform/fyne/...`
Expected: PASS.

If fyne integration test (`TestIntegration_ReactiveIfEmitsRenderSlot`) breaks because emitted code changed format slightly (e.g. fewer/more blank lines), update the assertions to match — preserve the substantive snippets but tolerate whitespace shifts.

- [ ] **Step 8: Commit**

```bash
git add codegen/intrinsic_walker.go codegen/intrinsic_walker_test.go codegen/platform/fyne/intrinsic_translator.go codegen/platform/fyne/compiler_ir.go codegen/platform/fyne/intrinsic_translator_test.go
git commit -m "codegen+fyne: move For/If structural recursion into WalkLowered

Adds OnIter and OnCond to IntrinsicTranslator. WalkLowered now
handles For/If recursion itself, calling the translator for the
iter/cond expressions and recursing the body through WalkLowered.

fyneStmtDispatch goes away — emitIRSlotFunc calls codegen.WalkLowered
directly. The intrinsic-aware dispatch is now entirely in the shared
walker; fyne contributes only the platform-specific iter/cond/leaf
emissions.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Drop redundant slot-translation helpers

After Tasks 3-4, `splitTrLines` and several lookups should be unused. Audit and remove.

- [ ] **Step 1: Audit**

Run: `grep -rn "splitTrLines\|isSlotReset\|isSlotListPush\|isLowerIntrinsic\|lowerStringArg\|lowerIdentArg\|identRef\|modelRef" codegen/platform/fyne/`

For each result, decide: still used or dead? Delete the dead ones. Keep only what survives.

- [ ] **Step 2: Build + test**

Run: `go build ./codegen/... && go test ./codegen/... ./codegen/platform/fyne/...`
Expected: clean.

- [ ] **Step 3: Commit (if anything was removed)**

```bash
git add codegen/platform/fyne/
git commit -m "fyne: prune unused dispatch helpers after WalkLowered widening

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Typed-parent slot signature

The runtime `parent.(*fyne.Container)` assertion leaks. Resolve by emitting `__renderSlot<N>(parent *fyne.Container)` directly — strongly typed.

This requires fyne to "know" at slot-Func emission time that the parameter is a `*fyne.Container`. The IR-level type is `dyn`. fyne's emitter overrides the Go type at the param-rendering step.

- [ ] **Step 1: Locate the slot-Func signature emission**

In `codegen/platform/fyne/compiler_ir.go`, `emitIRSlotFunc` emits:

```go
fmt.Fprintf(b, "func (m *Model) %s(parent fyne.CanvasObject) {\n", fn.Name)
b.WriteString("\tcontainer, _ := parent.(*fyne.Container)\n")
b.WriteString("\tif container == nil { return }\n")
```

- [ ] **Step 2: Replace with typed parent**

Change to:

```go
fmt.Fprintf(b, "func (m *Model) %s(container *fyne.Container) {\n", fn.Name)
```

(No assertion needed — caller is responsible for passing a container.)

- [ ] **Step 3: Verify the integration test still passes**

Run: `go test ./codegen/platform/fyne/...`
Expected: PASS.

If `TestIntegration_ReactiveIfEmitsRenderSlot` checks for the assertion line, update the assertion: replace `"container, _ := parent.(*fyne.Container)"` with `"func (m *Model) __renderSlot0(container *fyne.Container)"`.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/fyne/compiler_ir.go codegen/platform/fyne/intrinsic_integration_test.go
git commit -m "fyne: typed __renderSlot<N>(container *fyne.Container) signature

Drops the parent.(*fyne.Container) runtime assertion. Caller-side
discipline: BuildUI passes the root container directly. Plan B.2's
Task 9 makes this concrete by establishing the root container as a
named field.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Compile-check the integration test output

**Files:**
- Modify: `codegen/platform/fyne/intrinsic_integration_test.go`

Today the test only string-matches. Build the emitted Go to catch downstream errors that string-matching misses.

- [ ] **Step 1: Add the build helper**

Extend `TestIntegration_ReactiveIfEmitsRenderSlot` after the snippet assertions:

```go
// Write the emitted source to a temp dir and try to compile it
// against the same Go module. This catches __root-style "compiles
// to undefined ident" failures the snippet check misses.
tmp, err := os.MkdirTemp("", "fyne-emit-")
if err != nil {
    t.Fatal(err)
}
defer os.RemoveAll(tmp)

// Minimal go.mod so go build can resolve fyne imports.
if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(`module integration

go 1.22

require fyne.io/fyne/v2 v2.4.5
`), 0o644); err != nil {
    t.Fatal(err)
}
if err := os.WriteFile(filepath.Join(tmp, "model.go"), []byte("package integration\n\n"+out), 0o644); err != nil {
    t.Fatal(err)
}

cmd := exec.Command("go", "build", "./...")
cmd.Dir = tmp
cmd.Env = append(os.Environ(), "GOPROXY=off")
combined, err := cmd.CombinedOutput()
if err != nil {
    t.Errorf("emitted Go failed to compile: %v\n--- output ---\n%s\n--- source ---\n%s", err, combined, out)
}
```

Add imports: `os`, `os/exec`, `path/filepath`.

The `GOPROXY=off` flag avoids reaching out to the network; the fyne module must be in the local module cache (which it is, since the fyne package is a dependency of the main module).

- [ ] **Step 2: Run the test — expect failure**

Run: `go test ./codegen/platform/fyne/ -run TestIntegration_ReactiveIfEmitsRenderSlot -v`
Expected: FAIL with compile errors mentioning `__root` and probably other undefined references.

The failure output is now your TODO list for Plan B.2 — these are the gaps the rest of the plan closes. Capture the failures in your report.

- [ ] **Step 3: DO NOT fix the compile errors yet**

This task is just adding the harness; subsequent tasks (8-9 for `__root`, 12+ for the BuildUI rewrite) make it pass. Mark the test with `t.Skip("Plan B.2 incomplete; expected to pass after Task 12")` so it doesn't block CI:

Actually no — keep the test failing so we have a tripwire. Use `t.Errorf` (not `t.Fatalf`) on the compile failure so the rest of the assertions still run.

Confirm the existing `t.Errorf("emitted Go failed to compile: ...")` you added uses `Errorf` not `Fatalf` — Errorf logs the failure but continues, which matches the desired tripwire behavior.

- [ ] **Step 4: Commit (test currently fails)**

```bash
git add codegen/platform/fyne/intrinsic_integration_test.go
git commit -m "fyne(test): compile-check integration-test output

go build the emitted model.go against fyne. Currently FAILS because
Plan A's __root sentinel is unresolved in BuildUI call sites. Tasks
8-12 of this plan close the gap.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

(This is one of the rare cases where a commit lands a failing test deliberately — the failure documents the remaining work.)

---

## Phase B: Resolve `__root` sentinel

### Task 8: Add `__root` as a real component-level Var in passReactivity

**Files:**
- Modify: `internal/lower/reactivity.go`

The cleanest fix: passReactivity synthesizes a `__root dyn` Var on every component/window that has any reactive slot. fyne (and other platforms) emit it as a Model field. The Var is `Synthesized: true` (from Task 1's flag).

- [ ] **Step 1: Locate the rewriteAndInject entry**

In `internal/lower/reactivity.go`, find `rewriteAndInject` (the per-owner pass-2 entry). It already synthesizes `__slot<N>` Vars; extend to also synthesize a single `__root` if any slot exists.

- [ ] **Step 2: Synthesize `__root` once per owner**

Add at the top of `rewriteAndInject`:

```go
// If any reactive slots exist on this owner, synthesize a __root Var
// of type dyn. Platforms bind this to their root-container reference
// at codegen time (e.g. fyne: a *fyne.Container field initialized in
// BuildUI before slot-calls fire).
hasSlots := false
for _, slots := range st.reverseSlots {
    if len(slots) > 0 {
        hasSlots = true
        break
    }
}
if hasSlots {
    st.synthesizeRootVar()
}
```

Add the helper:

```go
// synthesizeRootVar creates the `__root dyn` Var on the current owner
// if it doesn't exist yet. Idempotent. Marked Synthesized so codegen
// can detect it without name-prefix matching.
func (st *reactivityState) synthesizeRootVar() {
    if existing := st.findSlotVar("__root"); existing != nil {
        return
    }
    v := &ir.Var{
        Name: "__root",
        Type: ir.TypDyn,
        // Init left nil — platforms initialize at BuildUI time.
    }
    // Synthesized marker: re-use existing convention; if ir.Var has
    // a Synthesized bool added in Task 1, set it. If not, the var's
    // name still starts with "__" which other passes treat as
    // pass-internal.
    if synthesizable, ok := any(v).(interface{ MarkSynthesized() }); ok {
        synthesizable.MarkSynthesized()
    }
    st.owner.addVar(v)
}
```

Actually — Plan A already added `Synthesized bool` to `ir.Var`. Just set it directly:

```go
v := &ir.Var{
    Name:         "__root",
    Type:         ir.TypDyn,
    Synthesized:  true,
}
```

Drop the type-assertion ceremony.

- [ ] **Step 3: Test**

Append to `internal/lower/reactivity_structural_test.go`:

```go
func TestRootVarSynthesizedWhenSlotExists(t *testing.T) {
    src := `
component main {
    var visible bool = true
    if visible {
        text(value="hi")
    }
}
`
    doc, _ := parser.Parse("t.sngl", []byte(src))
    pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
    if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
        t.Fatal(err)
    }
    comp := pkg.Components[0]
    var found *ir.Var
    for _, v := range comp.Vars {
        if v.Name == "__root" {
            found = v
            break
        }
    }
    if found == nil {
        t.Fatal("__root Var not synthesized")
    }
    if !found.Synthesized {
        t.Error("__root Var not marked Synthesized")
    }
}

func TestRootVarOmittedWhenNoSlots(t *testing.T) {
    src := `
component main {
    var count int = 0
    button(text=string(count), @click { count = count + 1 })
}
`
    doc, _ := parser.Parse("t.sngl", []byte(src))
    pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
    if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
        t.Fatal(err)
    }
    comp := pkg.Components[0]
    for _, v := range comp.Vars {
        if v.Name == "__root" {
            t.Error("__root unexpectedly synthesized for component with no reactive slots")
        }
    }
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/lower/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/reactivity.go internal/lower/reactivity_structural_test.go
git commit -m "lower(reactivity): synthesize __root Var on owner with reactive slots

Adds a single __root dyn Var (marked Synthesized) to every component
or window that has any reactive __slot<N>. Platforms now have a real
symbol to bind to their root-container reference, replacing the
__root sentinel Ident referenced from slot call sites.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Initialize `__root` in fyne's BuildUI

**Files:**
- Modify: `codegen/platform/fyne/compiler_ir.go` — BuildUI emission.
- Modify: `codegen/platform/fyne/intrinsic_translator.go` — OnCreateNode handling of `__root` field.

fyne needs:
1. A `__root *fyne.Container` field on Model (via the existing `info.binds` mechanism).
2. BuildUI initializes `m.__root = container.NewVBox()` at start, populates it, and returns it.
3. The renderSlot call sites that today emit `m.__renderSlot0(__root)` should emit `m.__renderSlot0(m.__root)`.

- [ ] **Step 1: Emit `__root` as a typed Model field**

In `codegen/platform/fyne/compiler_ir.go` `analyzeIR`'s synthesized-var branch (added in Plan B Task 8), special-case `__root`:

```go
if v.Synthesized {
    if v.Name == "__root" {
        // Root container — initialized in BuildUI, used by renderSlot
        // calls.
        info.binds = append(info.binds, irBind{
            name:        v.Name,
            goType:      "*fyne.Container",
            init:        "container.NewVBox()",
            noAccessors: true,
        })
        continue
    }
    // Existing __slot<N> branch:
    info.binds = append(info.binds, irBind{
        name:        v.Name,
        goType:      "[]fyne.CanvasObject",
        init:        "nil",
        noAccessors: true,
    })
    continue
}
```

- [ ] **Step 2: Update BuildUI to use m.__root**

In `emitIRBuildUI` (search for it in compiler_ir.go), find where the current emission appends children and returns. Replace the existing `return container.NewVBox(parts...)` pattern with:

```go
fmt.Fprintf(b, "func (m *Model) BuildUI() fyne.CanvasObject {\n")
fmt.Fprintf(b, "%s", buildBuf.String()) // existing emission
fmt.Fprintf(b, "\treturn m.__root\n")
fmt.Fprintf(b, "}\n")
```

(The existing emission needs to append top-level widgets into `m.__root` via `m.__root.Add(...)` instead of accumulating `parts`. Adjusting that is part of the BuildUI rewrite in Phase C.)

For Plan B.2 phase B's narrow scope: leave the existing parts/VBox path AND ALSO assign `m.__root = container.NewVBox(parts...)` before returning, so renderSlot calls have a real container to point to:

```go
// Before the return:
fmt.Fprintf(b, "\tm.__root = container.NewVBox(parts...)\n")
fmt.Fprintf(b, "\treturn m.__root\n")
```

Actually `m.__root` was already initialized to `container.NewVBox()` by analyzeIR's bind init. So just before the return, reassign it to include the parts:

```go
fmt.Fprintf(b, "\tm.__root.Objects = parts\n")
fmt.Fprintf(b, "\treturn m.__root\n")
```

That's cleaner.

- [ ] **Step 3: Update renderSlot call sites to use `m.__root`**

Plan A's `rewriteReactiveStructures` and `updatersFor` emit `&ir.Ident{Name: "__root", Synthesized: true}` as the parent arg. With the Synthesized flag and the `__root` Var on Model, fyne's expression evaluator (`gc.EvalExpr`) should already resolve `__root` to `m.__root` via Model field lookup.

Verify: trace what `gc.EvalExpr(&ir.Ident{Name: "__root", Synthesized: true})` produces. If it returns `__root` (bare), fyne needs a hook to recognize the Synthesized flag and prefix with `m.`. The hook is the existing `OnIter`/`OnCond` machinery, plus a new place: when fyne emits a CallStmt's args via `gc.EvalStmt` fallback.

Actually `gc.EvalExpr` for an Ident with `Sym == nil` typically emits the bare name. The Sym pointer needs to point to the `__root` Var on Model for it to resolve to `m.__root`.

Cleanest fix: pass-2 of passReactivity, when emitting the `__root` Ident, sets `Sym: <__root Var>`. Then fyne's expression evaluator resolves it correctly.

Update `slotCall` in `internal/lower/reactivity.go`:

```go
func (st *reactivityState) slotCall(slotID string, parentRef ir.Expr) *ir.CallStmt {
    if parentRef == nil {
        // Top-level reactive If/For: use the synthesized __root Var
        // on the owner. The expression resolves through the platform's
        // expression evaluator to the platform-specific root reference.
        rootVar := st.findSlotVar("__root")
        parentRef = &ir.Ident{Name: "__root", Type: ir.TypDyn, IsElementRef: true, Synthesized: true, Sym: rootVar}
    }
    // ... rest unchanged
}
```

Same edit in `updatersFor`'s `__root` fallback.

- [ ] **Step 4: Run integration test**

Run: `go test ./codegen/platform/fyne/ -run TestIntegration -v`
Expected: PASS — compile check from Task 7 should succeed now that `__root` resolves.

If it still fails with `__root` undefined, the gc.EvalExpr path isn't using Sym. Investigate `codegen/lang/golang/ircontext.go`'s Ident handling — verify that an Ident with `Sym: <Var>` and `Synthesized: true` produces `m.<name>`.

If the lang/golang path treats `Sym: <Var>` as "look up in Model fields" only when the var is in a particular scope (e.g. Component.Vars), confirm that's the case for `__root` (it is — added via owner.addVar).

If gc.EvalExpr emits bare `__root` despite Sym set, find the codepath and add a Synthesized-aware override at fyne's level: in `fyneTranslator.OnDefault`, detect the `__root` reference in the args of a CallStmt and prefix. Hack of last resort; prefer fixing gc.EvalExpr properly.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/fyne/compiler_ir.go internal/lower/reactivity.go
git commit -m "fyne+lower: bind __root sentinel to a real Model field

passReactivity now sets Sym on the __root Ident so the expression
evaluator resolves it through Model.__root (a *fyne.Container field
synthesized by analyzeIR). BuildUI initializes m.__root early and
returns it; renderSlot call sites now compile against a real
container.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase C: Reviewer nits

### Task 10: Move `zeroArgsFor` data into the blueprint

**Files:**
- Modify: `codegen/platform/fyne/blueprint.go` — extend `ctorMeta` with `ZeroArgs`.
- Modify: `codegen/platform/fyne/fyne.sngl` — set `ZeroArgs` on each component.
- Modify: `codegen/platform/fyne/intrinsic_translator.go` — `OnCreateNode` reads `bp.Constructor.ZeroArgs`.

- [ ] **Step 1: Extend ctorMeta**

In `codegen/platform/fyne/blueprint.go`, add a `ZeroArgs string` field to `ctorMeta`. Read the surrounding code to confirm field placement.

- [ ] **Step 2: Set ZeroArgs on relevant blueprints**

Two routes:
- **A:** Annotate `fyne.sngl` declaratively (extend the blueprint syntax). Requires parser/loader changes to read the new attribute. More work.
- **B:** Hard-code in `loadBlueprints()` (in blueprint.go): after each blueprint is loaded, set `bp.Constructor.ZeroArgs` based on the tag name via a switch table. Centralized in Go.

Recommend **B** for now:

```go
// In loadBlueprints() after the blueprint table is populated:
zeroArgs := map[string]string{
    "text":   `""`,
    "label":  `""`,
    "button": `"", nil`,
}
for name, args := range zeroArgs {
    if bp, ok := blueprintByName[name]; ok && bp.Constructor != nil {
        bp.Constructor.ZeroArgs = args
    }
}
```

Route A is a follow-up cleanup; not in scope.

- [ ] **Step 3: Update OnCreateNode**

In `codegen/platform/fyne/intrinsic_translator.go`, replace `zeroArgsFor(tag)` with `bp.Constructor.ZeroArgs`:

```go
func (t *fyneTranslator) OnCreateNode(id, tag string) string {
    bp, ok := t.blueprints[tag]
    if !ok || bp.Constructor == nil || bp.Constructor.GoType == "" {
        return ""
    }
    t.idTags[id] = tag
    t.fieldSink(id, bp.Constructor.GoType)
    return "m." + id + " = " + bp.Constructor.GoFn + "(" + bp.Constructor.ZeroArgs + ")\n"
}
```

Delete the local `zeroArgsFor` function.

- [ ] **Step 4: Run tests**

Run: `go test ./codegen/platform/fyne/...`
Expected: PASS. Existing tests for `text` should still work.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/fyne/blueprint.go codegen/platform/fyne/intrinsic_translator.go
git commit -m "fyne: move zeroArgs constructor data into blueprint table

ZeroArgs is now a Constructor field looked up at slot emission
time. Drops the parallel switch in intrinsic_translator.go.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Return `[]string` from translator methods (drop `splitTrLines` round-trip)

**Files:**
- Modify: `codegen/intrinsic_walker.go` — interface methods return `[]string`.
- Modify: `codegen/platform/fyne/intrinsic_translator.go` — methods return `[]string`.

Methods like `OnCreateNode` currently return a single string with trailing `\n`. WalkLowered then concatenates. fyne's emitIRSlotFunc then `strings.Split` to indent each line. Round-trip is wasteful.

Change methods to return `[]string`:

```go
type IntrinsicTranslator interface {
    OnCreateNode(id, tag string) []string
    OnAppendChild(parent, child string) []string
    OnRemoveChild(parent, child string) []string
    OnAttachHandler(node, event, handlerRef string) []string
    OnPropAssign(nodeID, prop string, valueExpr ir.Expr) []string
    OnSlotReset(slotID string) []string
    OnSlotAppend(slotID, childID string) []string
    OnIter(iter ir.Expr) string  // expression, single line — stays string
    OnCond(cond ir.Expr) string  // ditto
    OnDefault(stmt ir.Stmt) []string
}
```

WalkLowered's return type becomes `[]string` (drop the join).

- [ ] **Step 1: Update interface + walker**

Mechanical change. Replace `string` with `[]string` in the four leaf methods and in WalkLowered. Adjust callers.

- [ ] **Step 2: Update fyne impls**

Each `return "m." + ... + "\n"` becomes `return []string{"m." + ... }`.

- [ ] **Step 3: Update emitIRSlotFunc**

```go
body := codegen.WalkLowered(fn.Block, tr)
for _, l := range body {
    b.WriteString("\t")
    b.WriteString(l)
    b.WriteString("\n")
}
```

- [ ] **Step 4: Update walker tests**

Each `tr.lines` capture needs to handle `[]string` returns. The trace struct's add-string approach already does the right thing.

- [ ] **Step 5: Run all tests**

Run: `go test ./codegen/... ./codegen/platform/fyne/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add codegen/intrinsic_walker.go codegen/intrinsic_walker_test.go codegen/platform/fyne/intrinsic_translator.go codegen/platform/fyne/intrinsic_translator_test.go codegen/platform/fyne/compiler_ir.go
git commit -m "codegen: IntrinsicTranslator methods return []string

Drops the splitTrLines round-trip. Translator emits the same lines
the caller consumes directly.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase D: Enable NoDeclarative + rewrite BuildUI

> **Caveat for the implementer.** Phases D and E are the most speculative
> part of this plan. Tasks 12-15 are concrete; Tasks 16-18 are
> *discovery checkpoints* — concrete TDD-shape only emerges once you
> see what `NoDeclarative` on fyne actually breaks. Treat 16-18 as
> "run the tests, pick a failing category, diagnose, fix, commit" loops
> rather than pre-written task scripts. If a failure category is too
> large for one commit, split it. If the plan as written misses a
> category entirely, add a task and continue.
>
> Tasks 19-23 (dead-code removal) are mechanical *after* Phase D
> stabilizes, but trying them before Phase D's BuildUI rewrite is solid
> will leave fyne in a non-emitting state. Stage the removal carefully.

### Task 12: Turn on NoDeclarative in fyne's Capabilities

**Files:**
- Modify: `codegen/platform/fyne/fyne.go`

This is the moment everything else has been building toward. fyne's component bodies become flat intrinsic-call streams; BuildUI's renderStmt walker becomes obsolete.

EXPECT this commit to break existing fyne tests significantly. Subsequent tasks (13-18) bring them back to green by rewriting BuildUI emission onto the new IR shape.

- [ ] **Step 1: Update Capabilities**

In `codegen/platform/fyne/fyne.go`:

```go
func (g *Generator) Capabilities() lower.Caps {
    return lower.Caps{NoReactivity: true, NoDeclarative: true}
}
```

Update the comment accordingly.

- [ ] **Step 2: Run the fyne tests; capture the breakage**

Run: `go test ./codegen/platform/fyne/... 2>&1 | head -40`

Expected: WIDESPREAD failures. Capture the failure pattern in your report — the rest of the plan addresses each.

- [ ] **Step 3: Commit**

```bash
git add codegen/platform/fyne/fyne.go
git commit -m "fyne: enable NoDeclarative in Capabilities()

Component bodies now lower to flat lower.CreateNode/AppendChild/
AttachHandler/prop-Assign sequences. Existing tree-walk emission
(renderStmt + renderNode + ...) is now stale; Tasks 13-18 rewrite
BuildUI onto WalkLowered. Tests break in this commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 13: Skeleton — emit BuildUI through WalkLowered

**Files:**
- Modify: `codegen/platform/fyne/compiler_ir.go` — replace the Phase 1 BuildUI rendering loop.

Today the Phase 1 loop (lines ~202-236) walks the source-position body through `renderStmt` (NodeInst tree walker). With NoDeclarative on, the body is now a flat intrinsic stream — perfect for `WalkLowered`.

- [ ] **Step 1: Locate Phase 1**

In `codegen/platform/fyne/compiler_ir.go`, find where `vc.renderStmt(bodyStmts[0], "content")` is called (around line 215).

- [ ] **Step 2: Replace with WalkLowered**

The exact replacement depends on whether the body has zero / one / multiple top-level NodeInsts. With NoDeclarative on, NodeInsts have already become LocalVars (each `var __nN dyn = lower.CreateNode(...)`). The body is a flat sequence.

Replace the single-vs-multi-root branching with a unified WalkLowered call:

```go
if len(wins) > 0 && len(wins[0].Body) > 0 {
    bodyStmts := wins[0].Body

    tr := newFyneTranslator(gc, platformBlueprints(), func(name, goType string) {
        widgetFields = append(widgetFields, irWidgetField{name: name, goType: goType})
    })
    body := codegen.WalkLowered(bodyStmts, tr)

    for _, l := range body {
        fmt.Fprintf(&buildBuf, "\t%s\n", l)
    }
}
```

The `parts` accumulator goes away (the new emission directly references `m.__root.Add(...)` per top-level widget — handled by Task 14).

Multi-window emission is similar — apply the same change inside the `for _, w := range wins` loop.

- [ ] **Step 3: Test compile**

Run: `go build ./codegen/platform/fyne/...`
Expected: clean.

Run: `go test ./codegen/platform/fyne/...`
Expected: most tests fail (Task 12 already broke them); INTEGRATION test should make progress.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/fyne/compiler_ir.go
git commit -m "fyne: route BuildUI body through WalkLowered

Replaces the Phase 1 renderStmt tree-walk with a flat WalkLowered
pass over the lowered body. Single-window and multi-window paths
share the dispatch. Top-level widget collection (Task 14) and
parent-tracking (Task 15) are still WIP.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 14: Collect top-level widget refs into `m.__root`

**Files:**
- Modify: `codegen/platform/fyne/intrinsic_translator.go` — track top-level refs.
- Modify: `codegen/platform/fyne/compiler_ir.go` — emit Add() calls after WalkLowered.

The lowered IR doesn't AppendChild top-level NodeInsts (those have no enclosing parent NodeInst). fyne needs to append them to `m.__root` itself.

Strategy: the translator tracks every ref created via `OnCreateNode` (added to a `created` set) and every ref appended via `OnAppendChild` (removed from the set). After WalkLowered, the residual set is the top-levels. fyne emits `m.__root.Add(m.<ref>)` for each.

- [ ] **Step 1: Track creates and appends in fyneTranslator**

Add to the struct:

```go
type fyneTranslator struct {
    // ... existing ...
    topLevel []string // ordered list of refs not yet AppendChild'd
}
```

Update `OnCreateNode`:

```go
func (t *fyneTranslator) OnCreateNode(id, tag string) []string {
    // ... existing checks ...
    t.idTags[id] = tag
    t.fieldSink(id, bp.Constructor.GoType)
    t.topLevel = append(t.topLevel, id)
    return []string{"m." + id + " = " + bp.Constructor.GoFn + "(" + bp.Constructor.ZeroArgs + ")"}
}
```

Update `OnAppendChild`:

```go
func (t *fyneTranslator) OnAppendChild(parent, child string) []string {
    // Remove child from top-level set.
    for i, name := range t.topLevel {
        if name == child {
            t.topLevel = append(t.topLevel[:i], t.topLevel[i+1:]...)
            break
        }
    }
    return []string{parent + ".Add(m." + child + ")"}
}
```

(The child names here are bare `__nN` — qualify with `m.` since they're always Synthesized widget refs.)

Hmm — note OnAppendChild's `parent` parameter. The dispatch passes either `"container"` (inside slot Funcs, removed by Task 6 in favor of typed signature) or some other ref. For top-level BuildUI emission, AppendChild can target arbitrary parent widgets created by CreateNode.

The parent string: when the parent is a slot/synthesized ref, it's qualified as `m.<name>` by the dispatch already (via the Synthesized flag check). When it's the `container`/typed param inside slot, it stays bare.

Adjust the dispatch in WalkLowered's CallStmt case to pre-qualify parent and child uniformly:

```go
case isLowerIntrinsic(n.Call, "AppendChild"):
    p, c := identRefForFyne(n.Call.Args[0].Value), identRefForFyne(n.Call.Args[1].Value)
    return t.OnAppendChild(p, c)
```

Where `identRefForFyne` checks `Synthesized` and prefixes. But that's fyne-specific in shared walker — not ideal.

Alternative: walker passes the raw Ident's Name, and translator decides qualification. Move the Synthesized→m. prefix logic into the translator's OnAppendChild itself:

```go
func (t *fyneTranslator) OnAppendChild(parent, child string) []string {
    // Both parent and child should be qualified through the translator's
    // own naming convention. For fyne, all widget refs live on Model
    // (m.<name>). The exception is the typed slot parameter
    // `container` (a *fyne.Container function arg) which stays bare.
    return []string{t.qualify(parent) + ".Add(" + t.qualify(child) + ")"}
}

func (t *fyneTranslator) qualify(name string) string {
    if name == "container" || name == "parent" {
        return name // typed function params
    }
    return "m." + name
}
```

This is cleaner. Apply same pattern to `OnRemoveChild`, `OnAttachHandler` (for the `node` arg), etc.

- [ ] **Step 2: After WalkLowered, emit Add() calls for top-levels**

In `compiler_ir.go` after the WalkLowered loop:

```go
body := codegen.WalkLowered(bodyStmts, tr)
for _, l := range body {
    fmt.Fprintf(&buildBuf, "\t%s\n", l)
}
// Append top-level widgets to __root in source order.
for _, ref := range tr.topLevel {
    fmt.Fprintf(&buildBuf, "\tm.__root.Add(m.%s)\n", ref)
}
```

- [ ] **Step 3: Run integration test**

Run: `go test ./codegen/platform/fyne/ -run TestIntegration -v`
Expected: closer to PASS. The compile check should resolve more refs. If `m.__root.Add(...)` doesn't compile because top-levels include the button + the implicit slot-call, debug.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/fyne/intrinsic_translator.go codegen/platform/fyne/compiler_ir.go
git commit -m "fyne: append top-level widgets to m.__root after WalkLowered

Translator tracks every ref created via OnCreateNode minus those
appended via OnAppendChild; residuals are top-level widgets that
need explicit attachment to BuildUI's root container.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 15: Handle user-component creation

**Files:**
- Modify: `codegen/platform/fyne/intrinsic_translator.go` — `OnCreateNode` for user components.

`lower.CreateNode("UserComp")` for a user-defined component needs different emission: not `widget.NewLabel("")` but `newUserComp()` (the Model constructor for that component). Current renderUserComponent (view_ir.go:875) handles this.

- [ ] **Step 1: Extend OnCreateNode**

```go
func (t *fyneTranslator) OnCreateNode(id, tag string) []string {
    if bp, ok := t.blueprints[tag]; ok && bp.Constructor != nil && bp.Constructor.GoType != "" {
        t.idTags[id] = tag
        t.fieldSink(id, bp.Constructor.GoType)
        t.topLevel = append(t.topLevel, id)
        return []string{"m." + id + " = " + bp.Constructor.GoFn + "(" + bp.Constructor.ZeroArgs + ")"}
    }
    // User-component fallback: assume the tag is the name of a SNGL
    // component, emit a constructor call against its Model type.
    if t.isUserComponent(tag) {
        goType := "*" + golang.ExportName(tag)
        ctor := "new" + golang.ExportName(tag)
        t.idTags[id] = tag
        t.fieldSink(id, goType)
        t.topLevel = append(t.topLevel, id)
        return []string{"m." + id + " = " + ctor + "()"}
    }
    return nil
}

// isUserComponent reports whether the tag refers to a SNGL component
// declared in the user's package (not a platform-shipped blueprint).
// Compares against the analysis context's components list.
func (t *fyneTranslator) isUserComponent(tag string) bool {
    // ... look up in t.gc.Ctx().Pkg.Components or similar
    // Adapt to whatever the codegen ctx exposes
    return false // placeholder — implement based on what fyne already has
}
```

The placeholder needs filling. Look at `renderUserComponent` in `view_ir.go:875` to see how it determines "user component" — probably via the NodeInst's `Component *ir.Component` field. But translator receives a tag string, not a NodeInst.

Workaround: pass the resolved Component into the translator at dispatch time. Update IntrinsicTranslator's OnCreateNode signature:

```go
OnCreateNode(id, tag string, comp *ir.Component) []string
```

(Optional `comp` — nil for stdlib blueprints, non-nil for user components.)

In WalkLowered's CreateNode dispatch, pass the LocalVar's component info. The lowered LocalVar's Init `lower.CreateNode(tag)` doesn't carry the Component field today — passDeclarative emits `n.Component` ONLY on the LocalVar's Type:

```go
varType := ir.TypDyn
if n.Component != nil {
    varType = &ir.Type{Kind: ir.TypeComponent, Decl: n.Component}
}
```

So LocalVar.Type.Decl is the Component pointer when it's a user component. Use that:

```go
case *ir.LocalVar:
    if call, ok := n.Init.(*ir.Call); ok && isLowerIntrinsic(call, "CreateNode") {
        tag, _ := extractStringLit(call.Args[0].Value)
        var comp *ir.Component
        if n.Type != nil && n.Type.Kind == ir.TypeComponent {
            comp, _ = n.Type.Decl.(*ir.Component)
        }
        return t.OnCreateNode(n.Name, tag, comp)
    }
```

And `OnCreateNode` becomes:

```go
func (t *fyneTranslator) OnCreateNode(id, tag string, comp *ir.Component) []string {
    if comp != nil {
        // User component
        goType := "*" + golang.ExportName(comp.Name)
        ctor := "new" + golang.ExportName(comp.Name)
        // ... etc
    }
    // ... blueprint fallback ...
}
```

Update interface, walker, fyne impl, tests.

- [ ] **Step 2: Update OnCreateNode signature and callers**

Apply the signature change in `codegen/intrinsic_walker.go` and `codegen/platform/fyne/intrinsic_translator.go`. Update existing tests to pass `nil` for comp.

- [ ] **Step 3: Test with a fixture that uses a user component**

Append to `codegen/platform/fyne/intrinsic_integration_test.go`:

```go
func TestIntegration_UserComponent(t *testing.T) {
    src := `
component child {
    text(value="hello")
}
component main {
    child()
}
`
    // ... same flow as TestIntegration_ReactiveIfEmitsRenderSlot ...
    // assert m.__n0 = newChild() appears
}
```

(Fill in with the same parse/check/lower/generate/build flow.)

- [ ] **Step 4: Run + commit**

Run: `go test ./codegen/platform/fyne/...`

Commit:

```bash
git add codegen/platform/fyne/intrinsic_translator.go codegen/platform/fyne/intrinsic_integration_test.go codegen/intrinsic_walker.go codegen/intrinsic_walker_test.go
git commit -m "fyne: OnCreateNode handles user-component instantiation

Adds the Component pointer to OnCreateNode's signature. User
components emit `m.__nN = newUserComp()`; built-in tags continue
to dispatch through blueprints.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Tasks 16-18: Address remaining BuildUI breakage

After Tasks 12-15, run the full fyne test suite and address each failure category:

**Task 16: Init bindings on widgets (e.g. button text at construction).** The lowered IR sets initial props via Assign-after-CreateNode. Today blueprints emit these as constructor args. Decide: keep the lowered Assign approach (translator's OnPropAssign already handles it) or detect init-time assigns and rewrite the constructor call. Lowered-Assign approach is simpler — let it be.

But fyne's existing constructor args for buttons take both label and handler (`widget.NewButton(label, handler)`). With zero-arg construction + post-Assign setters, the button is constructed without a handler — that has to come via OnAttachHandler. Verify Button's blueprint binds OnTapped via an Event binding (per Task 4 of Plan B's findings, button events are inlined in the constructor, NOT in bindings). This needs blueprint adjustment: add Event bindings for button's click event so OnAttachHandler can wire it.

- [ ] Update `codegen/platform/fyne/fyne.sngl` to add `Event{prop:"click", target:".OnTapped", signature:"func()"}` to the button blueprint's bindings.
- [ ] Test that lowered button code wires OnTapped correctly via OnAttachHandler.
- [ ] Commit.

**Task 17: Containers (vbox/hbox/scroll).** Their constructors take children: `container.NewVBox(...)`. With the lowered approach, children are AppendChild'd individually. NewVBox() with zero args creates an empty container, then `Add(...)` populates it. Verify.

- [ ] Set vbox/hbox/scroll's ZeroArgs to empty and confirm zero-arg construction works.
- [ ] Verify AppendChild calls correctly populate them.
- [ ] Commit.

**Task 18: Handlers with closures (typical case).** A button's @click handler `{ count = count + 1 }` becomes (post-NoLambda OR pre-NoLambda) a function reference or inline closure. The lowered AttachHandler call passes a handler arg — could be a Func name (NoLambda lifted it) or an inline Closure. fyne's OnAttachHandler needs to handle both.

- [ ] Confirm what shape the handler arg takes in the lowered IR (read passDeclarative.lowerNode's handler emission).
- [ ] Extend OnAttachHandler if needed.
- [ ] Commit.

These three sub-tasks (16-18) are likely to surface concrete issues only when you run the tests. Tackle each empirically: identify a failing test, diagnose, fix, commit. The pattern repeats. Don't try to plan every edge case in advance.

---

## Phase E: Rip the parallel pipeline

### Tasks 19-23: Remove the dead code

Each task deletes a specific cluster. Run tests after each to verify nothing depended on it.

- [ ] **Task 19**: Delete `renderConditional`, `renderFor`, `updateIf<N>` / `updateFor<N>` emission paths in `view_ir.go`. Run `go test ./codegen/platform/fyne/...`.

- [ ] **Task 20**: Delete the `updaters` slice, `addUpdater`, `irWidgetUpdater`, `DepFields`, `emitIRUpdaters` from `view_ir.go` + `compiler_ir.go`.

- [ ] **Task 21**: Delete the two `codegen.FindAffected` call sites in `compiler_ir.go` (timer and bind setter). Update timer/setter emission to drop the `AffectedUpdaters` interpolation.

- [ ] **Task 22**: Delete `lateReactiveAssign`, `recordLateReactive`, `resolveReactiveTokens`, `nodeBindings`, `recordNodeBinding`, `nodeBinding`. Audit `/*SNGLREACT:i*/` token emission — should be gone now.

- [ ] **Task 23**: Delete `localMode`, `withLocalMode`, `snapshotCounters`, `restoreCounters` from `view_ir.go`.

After each task: `go test ./codegen/platform/fyne/...` — expect failures only on tests that explicitly tested the removed paths. Update those tests or delete them.

Commit one task at a time with messages like `fyne: remove updateIf/updateFor (dead after NoDeclarative)`.

### Task 24: Drop scaffold updater fields

**Files:**
- Modify: `codegen/platform/fyne/scaffold.go` — delete `UpdaterNames`, `AffectedUpdaters`, `doRefresh`-related fields.
- Modify: `codegen/platform/fyne/templates/model.go.tmpl` — delete the `doRefresh` method.

- [ ] **Step 1: Audit references**

Run: `grep -rn "UpdaterNames\|AffectedUpdaters\|doRefresh" codegen/platform/fyne/`

- [ ] **Step 2: Remove from struct, template, and consumers**

Delete the struct fields, remove the template arm. Update `newIRTemplateData` and any other consumer.

- [ ] **Step 3: Verify**

Run: `go test ./codegen/platform/fyne/...`

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/fyne/scaffold.go codegen/platform/fyne/templates/model.go.tmpl codegen/platform/fyne/compiler_ir.go
git commit -m "fyne: remove scaffold updater fields (dead after Phase D)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase F: Final verification

### Task 25: Run `go tool verify`; document the resulting state

- [ ] **Step 1: Run verify**

Run: `go tool verify 2>&1 | tail -10`

Capture the failure count. Compare to baseline (33/109 at Plan A close). Expected outcomes:
- Fewer failures (Plan B.2 fixes some pre-existing reactivity issues by rewriting the pipeline).
- Same count (the pre-existing failures are deeper bugs the rewrite didn't touch).
- More failures (Plan B.2 introduced regressions — DEBUG before continuing).

- [ ] **Step 2: Document the state**

Append a note to `docs/superpowers/plans/2026-05-12-fyne-nodeclarative-conversion.md` (this file) at the end:

```markdown
## Verification at Plan B.2 close

(Filled in at close-out.)

- `go tool verify`: <count>/109 failures (baseline: 33/109).
- All fyne integration tests pass.
- The fyne emitted Go compiles end-to-end via the integration-test go-build harness.
```

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/plans/2026-05-12-fyne-nodeclarative-conversion.md
git commit -m "docs: Plan B.2 verification close-out

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 26: Plan handoff annotation

- [ ] **Step 1: Update the spec migration-order note**

In `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md`, find the existing block referring to Plans A/B/B.2 etc. Update to:

```
> **Plan A** lands steps 1–3. **Plan B** lands the fyne intrinsic translator
> and slot-Func consumption. **Plan B.2**
> (`docs/superpowers/plans/2026-05-12-fyne-nodeclarative-conversion.md`)
> enables NoDeclarative on fyne, rewrites BuildUI emission, and rips
> the parallel updater pipeline. Plans C/D cover gtk4 / html. Plan E
> is the final audit.
```

- [ ] **Step 2: Commit + push**

```bash
git add docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md
git commit -m "docs(spec): annotate Plan B.2 scope on reactivity lowering design

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## What's NOT in Plan B.2 (deferred to subsequent plans)

- **gtk4 conversion** — Plan C will mirror Plan B+B.2 for gtk4. Same structural approach (intrinsic translator + WalkLowered + NoDeclarative), different emission (C bridge calls).
- **html conversion** — Plan D handles the static-site-friendly path: tree-walking initial HTML stays, JS update layer goes intrinsic-driven.
- **Final audit / fuzz extension** — Plan E confirms no parallel reactive pipeline survives anywhere, extends `FuzzLoweredDocument` to cover the full caps mix.
- **Two-way binding syntax (`:value=name`)** — separate spec.
- **Keyed for-loop diffing** — separate spec.
- **Nested reactive structures inside reactive slots** — Plan A panics on this; a follow-up spec is needed.
