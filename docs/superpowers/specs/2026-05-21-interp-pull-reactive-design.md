# Pull-Reactive Interp

Date: 2026-05-21

## Summary

Make the interp test runner support the reactive-style fixtures currently failing in `TestRunFixtures` by giving child component instances **shared state** with their parent env (not snapshots), exposing `c.children[i]` as live `componentValue` wrappers, routing method calls on `componentValue` through its live state, and running tree-shake before evaluation. No subscription system, no effect tracking — reactivity is pull-based via existing `ResolveElementRef` re-evaluation.

## Motivation

Four `TestRunFixtures` failures exist today (`test_reactivity_two_components`, `test_reactivity_extension_method`, `test_reactivity_global_var`, `test_mutation_through_method`). They were tagged "pre-existing interp limitations" but each reduces to one of four narrow gaps in the interp / testrunner:

1. `c.children[i]` returns nil — the testrunner's `componentValue` doesn't enumerate child components.
2. `componentEnv` (in `internal/interp/render.go`) builds a fresh env for each ResolveElementRef call, so state mutations on a child don't persist across reads.
3. Top-level methods on a component type (`func main.double(this main)`) register in `env.Funcs["main.double"]` but `componentValue.InvokeMethod` only searches `cv.Funcs` (seeded from `comp.Funcs`), so extension-method dispatch from outside fails.
4. ~~Tree-shake isn't run in the testrunner pipeline.~~ Dropped from this spec — pull-based evaluation already skips unreferenced code, so shake provides no eval-time benefit for the interp. Codegen targets keep shake in their own pipelines.

A full reactive runtime (push notifications, effect graphs) would duplicate per-target machinery that each codegen already builds for its own runtime. The interp can sidestep that by being **pull-based**: every property access re-evaluates against the live env. Existing `ResolveElementRef` already walks the IR fresh each call; the missing piece is **shared state across child component instances**.

## Design

### Shared state via cached child envs

`internal/interp/render.go`'s `componentEnv` currently builds a fresh `*Env` per call:

```go
func (env *Env) componentEnv(comp *ir.Component, inst *ir.NodeInst) *Env {
	child := NewEnv()
	// per-call init from comp.Vars defaults...
}
```

Persist the child env across calls by caching it on the parent env, keyed by the instantiation `*ir.NodeInst`:

```go
type Env struct {
	// ... existing ...
	childEnvs map[*ir.NodeInst]*Env // memoised per instantiation site
}

func (env *Env) componentEnv(comp *ir.Component, inst *ir.NodeInst) *Env {
	if env.childEnvs == nil {
		env.childEnvs = make(map[*ir.NodeInst]*Env)
	}
	if cached, ok := env.childEnvs[inst]; ok {
		return cached
	}
	child := NewEnv()
	// ... existing init logic ...
	env.childEnvs[inst] = child
	return child
}
```

State mutations on the cached env persist across `ResolveElementRef` calls. Re-evaluation of prop expressions reads the current values. This is the reactive-by-laziness primitive.

### `componentValue.children`

The testrunner's `componentValue` (in `codegen/platform/none/testrunner/runner.go`) gains a `children` slot and a `GetField("children")` branch:

```go
type componentValue struct {
    // ... existing fields ...
    body     []ir.Stmt    // the component's lowered body, captured at construction
    children []any        // memoised; nil until first access
}

// Inside GetField, before the existing field/method lookups:
if field == "children" {
    return cv.collectChildren(), nil
}

func (cv *componentValue) collectChildren() []any {
    if cv.children != nil {
        return cv.children
    }
    cv.children = cv.walkChildren(cv.body)
    return cv.children
}

func (cv *componentValue) walkChildren(stmts []ir.Stmt) []any {
    var out []any
    for _, s := range stmts {
        switch n := s.(type) {
        case *ir.NodeInst:
            if n.Component != nil && len(n.Component.Body) > 0 {
                // User component: wrap as a live componentValue whose
                // Env is the cached child env (shared state with parent).
                childEnv := cv.Env.componentEnv(n.Component, n)
                out = append(out, newChildComponentValue(childEnv, n.Component))
            } else {
                // Native element: render its current props map.
                out = append(out, cv.Env.renderNodeProps(n))
            }
        case *ir.CallStmt:
            if rendered := cv.Env.renderCallStmtNode(n); rendered != nil {
                out = append(out, rendered)
            }
        case *ir.If:
            // Evaluate cond at current env; recurse into matching branch.
            if cond, err := cv.Env.Eval(n.Cond); err == nil {
                if b, _ := cond.(bool); b {
                    out = append(out, cv.walkChildren(n.Body)...)
                } else {
                    out = append(out, cv.walkChildren(n.Else)...)
                }
            }
        case *ir.For:
            // For each iteration, evaluate body at the iteration's env.
            // Cached childEnvs keyed by NodeInst alone aren't unique across
            // iterations — out of scope for this pass; emit a placeholder.
            //
            // (No current fixture exercises components inside for loops in
            // the testrunner path.)
        case *ir.PlatformFilter:
            if n.Platform == "" || n.Platform == "none" {
                out = append(out, cv.walkChildren(n.Body)...)
            }
        }
    }
    return out
}
```

`newChildComponentValue` constructs a `componentValue` whose `Env` is the cached child env (the same instance returned by `componentEnv(comp, inst)`). The child's GetField / SetField / InvokeMethod all route through that env, so writes persist and reads see the current state.

### Type-namespace method dispatch

In `componentValue.InvokeMethod` and `componentValue.GetField`, extend the lookup chain so top-level extension methods on the component's type are reachable:

```go
// Existing chain:
//	cv.Funcs[method]
//	cv.Funcs[cv.compName + "." + method]
//
// New tail:
if cv.compName != "" {
	if fn, ok := cv.Env.Funcs[cv.compName+"."+method]; ok {
		return cv.invokeOnSelf(fn, args)
	}
}
```

`invokeOnSelf(fn, args)` binds the synthetic `this` param to `cv` itself (passing the componentValue as the first arg), then evaluates the body in a child env that inherits `cv.Env`'s state. Assignments inside the body that target `this.<field>` route through `componentValue.SetField` because `this` IS the componentValue.

### Mutation through methods

`SetField` already mutates `cv.Vars` and propagates to the env. The remaining work is wiring the method body so `this.x = ...` resolves `this` to the componentValue at runtime.

For methods called via event handlers (`c.bump.@click()` → handler body runs `bump()`), the handler executes in `cv.Env`. The `bump()` call resolves to `env.Funcs["main.bump"]` (per Section 3). Then `invokeOnSelf` binds `this = cv` and runs the body. Inside the body, `this.x += 1` becomes `*ir.Assign{Target: Select{this, "x"}}`. The interp's `evalMutTarget` for `Select{ident, field}` needs to recognise an ident whose value is a `componentValue` and route the write through `SetField`.

If `evalMutTarget` already handles this for top-level c.field writes from test functions (it must — `c.p.x = 99` works in `test_structs.sngl`), the same path serves bare-method-body writes.

### Error handling

- Cycle in childEnvs: not possible — each child env is keyed by a distinct `*ir.NodeInst` pointer.
- Component method called on a value that isn't a componentValue (e.g. a struct passed in): fall through to the existing path; behavior unchanged.
- `c.children` accessed before any rendering: returns the slice computed from `comp.Body` at component-value construction time. Empty body → empty slice.

### Testing

The 4 reactive fixtures are the acceptance criteria. No new fixtures.

Sanity checks added to `codegen/platform/none/testrunner/runner_test.go` (or extended) — direct unit tests for:
- `componentValue.GetField("children")` returns N entries for an N-statement body.
- Two distinct calls to `c.children` return the same wrapper instances (live, not regenerated).
- Mutating a child's state via `cv.children[0].SetField("x", 5)` is visible to a later `cv.children[0].GetField("x")`.

These are quick unit tests; the fixtures cover the integration path.

## Constraints

- Pull-based reactivity only. No notification graph, no effect tracking.
- For-loop component instances are out of scope. The cache key `*ir.NodeInst` isn't unique per iteration; supporting per-iter instances needs a `(NodeInst, iter-key)` cache and is a follow-up.
- `c.children` element-map dicts (returned for native elements) are point-in-time snapshots; mutations after the slice is fetched aren't reflected in those maps. componentValue entries ARE live.

## Risks

- **Shared-env mutations leaking across siblings.** Two `counter()` calls in main produce two distinct NodeInst pointers, hence two distinct cached envs. Verified by the `test_reactivity_two_components` fixture which proves they remain isolated.
- **Stale `children` slice after structural changes.** If an `if` toggles and re-toggles, the cached children slice misses the structural change. The fixtures don't exercise this; if they do later, drop the slice cache and recompute on each access.
- **`renderCallStmtNode` may not exist.** If `collectCallStmtByID` currently does the rendering inline, expose a small helper that returns the rendered map without searching for an ID.

## Out of scope

- Reactive subscriptions / effect graph. The interp stays pull-based.
- Components instantiated inside `for` loops (per-iteration distinct envs).
- Aligning the interp's IR-shape expectations with the post-lower IR (would require running the full lower pipeline before interp, a separate undertaking).

## Plan summary (detailed plan to follow via writing-plans)

1. Add `Env.childEnvs map[*ir.NodeInst]*Env` and memoise `componentEnv`.
2. Extend `componentValue` with `body` + `children` + the `walkChildren` helper.
3. Add `GetField("children")` branch.
4. Extend `InvokeMethod` and `GetField` lookup chains to consult `cv.Env.Funcs[type+"."+method]`.
5. Implement `invokeOnSelf(fn, args)`: bind `this` to cv, evaluate body, mutations route through SetField.
6. Verify `evalMutTarget` routes `this.x = ...` through componentValue when `this` is bound to a componentValue.
7. Run the 4 fixtures; iterate until green.
8. Run `go tool verify`; verify no regressions in HTML/Bubbletea/etc.
