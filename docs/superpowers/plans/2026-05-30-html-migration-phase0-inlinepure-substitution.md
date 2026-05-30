# HTML Migration — Phase 0: InlinePure param substitution

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `InlinePure` bind *every* param of an inlined component — falling back to the param's default, then to a typed zero-value — so no bare param identifiers survive into codegen.

**Architecture:** `internal/lower/inline_pure.go`'s `substitute` currently binds only params the call site passed explicitly. Unbound params (e.g. an omitted `disabled bool`) leak into the inlined body as bare `*ir.Ident`s and later render as `disabled="disabled"`. Mirror the user-component inliner (`inline_components.go:582-598`, arg → `Default`) and extend it with `ir.ZeroExpr(p.Type)` so params with neither arg nor default still resolve. This is Phase 0 of `docs/superpowers/specs/2026-05-30-html-static-renderer-migration-design.md` and unblocks the generic-renderer migration (the leak is currently masked by the vestigial `renderStaticX` helpers, so this phase is verified at the lowering layer, not end-to-end html).

**Tech Stack:** Go, SNGL IR (`git.duckfam.us/jonathan/sngl/ir`), `internal/lower` pass pipeline.

---

## File Structure

- `internal/lower/inline_pure.go` — modify `substitute` (the binding loop, ~lines 368-376).
- `internal/lower/inline_pure_substitution_test.go` — **create**; white-box test (`package lower`) driving `lowerInlinePure` directly.

No new types or exported symbols. Reuses existing `ir.ZeroExpr(t *ir.Type) ir.Expr` (`ir/defaults.go`).

---

### Task 1: InlinePure binds omitted params to a zero-value

**Files:**
- Test: `internal/lower/inline_pure_substitution_test.go` (create)
- Modify: `internal/lower/inline_pure.go` (`substitute`, the `bindings` loop)

- [ ] **Step 1: Write the failing test**

Create `internal/lower/inline_pure_substitution_test.go`:

```go
package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A pure component whose body references a prop the call site omits must
// have that reference substituted with the prop's zero-value, not left as a
// bare param identifier (which leaks to codegen as e.g. disabled="disabled").
func TestInlinePure_OmittedParamGetsZeroValue(t *testing.T) {
	// component wrap(flag bool) { div(data-flag=flag) }   — flag has no default
	flagParam := &ir.Param{Name: "flag", Type: ir.TypBool}
	body := []ir.Stmt{
		&ir.NodeInst{
			Name: "div",
			Props: []ir.Arg{{
				Name:  "data-flag",
				Value: &ir.Ident{Name: "flag", Sym: flagParam, Type: ir.TypBool},
			}},
		},
	}
	wrap := &ir.Component{
		Name:  "wrap",
		Props: []*ir.Prop{{Name: "flag", Type: ir.TypBool}}, // no Default
		Body:  body,
	}
	// component main { wrap() }   — no flag arg passed
	main := &ir.Component{
		Name: "main",
		Body: []ir.Stmt{&ir.NodeInst{Name: "wrap", Component: wrap}},
	}
	pkg := &ir.Package{Components: []*ir.Component{wrap, main}}

	if err := lowerInlinePure(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("lowerInlinePure: %v", err)
	}

	// main.Body[0] is now the inlined <div>; its data-flag prop must be a
	// literal false, not the bare `flag` identifier.
	div, ok := main.Body[0].(*ir.NodeInst)
	if !ok || div.Name != "div" {
		t.Fatalf("expected inlined div at main.Body[0], got %T", main.Body[0])
	}
	var val ir.Expr
	for _, p := range div.Props {
		if p.Name == "data-flag" {
			val = p.Value
		}
	}
	if _, isIdent := val.(*ir.Ident); isIdent {
		t.Fatal("omitted param left as bare identifier — not substituted")
	}
	lit, ok := val.(*ir.Literal)
	if !ok {
		t.Fatalf("data-flag value: got %T, want *ir.Literal", val)
	}
	if lit.Raw != "false" {
		t.Errorf("data-flag zero-value: got %q, want \"false\"", lit.Raw)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

Run: `go test ./internal/lower/ -run TestInlinePure_OmittedParamGetsZeroValue -v`
Expected: FAIL — "omitted param left as bare identifier — not substituted" (the current `substitute` only binds passed props, so `flag` stays an `*ir.Ident`).

- [ ] **Step 3: Implement the fallback in `substitute`**

In `internal/lower/inline_pure.go`, replace the binding loop in `substitute`:

```go
	// Build param-binding map: paramName → user's bound arg expression.
	bindings := map[string]ir.Expr{}
	for _, p := range comp.Props {
		for _, prop := range callsite.Props {
			if prop.Name == p.Name {
				bindings[p.Name] = prop.Value
				break
			}
		}
	}
```

with:

```go
	// Build param-binding map. Bind every prop, falling back from the
	// call-site arg to the prop's default to a typed zero-value, so the
	// body never keeps a bare param identifier (mirrors expandCall in
	// inline_components.go; ZeroExpr covers props with no default, e.g.
	// `disabled bool`).
	bindings := map[string]ir.Expr{}
	for _, p := range comp.Props {
		var val ir.Expr
		for _, prop := range callsite.Props {
			if prop.Name == p.Name {
				val = prop.Value
				break
			}
		}
		if val == nil {
			val = p.Default
		}
		if val == nil {
			val = ir.ZeroExpr(p.Type)
		}
		if val != nil {
			bindings[p.Name] = val
		}
	}
```

- [ ] **Step 4: Run the test, verify it passes**

Run: `go test ./internal/lower/ -run TestInlinePure_OmittedParamGetsZeroValue -v`
Expected: PASS.

- [ ] **Step 5: Run the lowering + optimize + html suites for regressions**

Run: `go test ./internal/lower/ ./internal/optimize/ ./codegen/platform/html/`
Expected: all `ok`. (Watch for golden-file diffs in `internal/lower/golden_test.go` — if a golden now shows a previously-omitted param rendered as its zero-value, that is the intended change; update the golden with `go test ./internal/lower/ -run TestGolden -update` only after confirming the diff is exactly a bare-ident → literal substitution.)

- [ ] **Step 6: Commit**

```bash
git add internal/lower/inline_pure.go internal/lower/inline_pure_substitution_test.go
git commit -m "fix(lower): InlinePure binds omitted params to default/zero-value

substitute() bound only call-site-passed props, leaking omitted params
(e.g. disabled bool) as bare identifiers. Fall back arg -> Default ->
ir.ZeroExpr(type), matching the user-component inliner. Phase 0 of the
html static renderer migration."
```

---

## Self-Review

- **Spec coverage:** Implements spec §"Phase 0 — InlinePure param substitution" exactly (arg → Default → typed zero-value via `ir.ZeroExpr`). No other spec section is in scope for this phase.
- **Placeholder scan:** No TBD/TODO; the only code change and the full test are shown.
- **Type consistency:** `ir.Prop{Name,Type,Default}`, `ir.Param`, `ir.Arg{Name,Value}`, `ir.NodeInst{Name,Props,Component,Body}`, `ir.Literal{Raw}`, `ir.ZeroExpr(*ir.Type) ir.Expr`, `lowerInlinePure(*ir.Package, Caps, Options) error` — all match current signatures (verified in `ir/ir.go`, `ir/stmt.go`, `ir/defaults.go`, `internal/lower/inline_pure.go`).
- **Note for next phase:** the end-to-end manifestation (input no longer empty, no `disabled="disabled"`) only appears once the generic renderer replaces `renderStaticInput` in Phase 1+2; Phase 0's guarantee is purely at the IR/lowering layer, which is what the test asserts.
