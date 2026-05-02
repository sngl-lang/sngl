# Codegen Lowering — Phase 3c (NoTimer) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `NoTimer` — replace every `*ir.Timer` decl with a synthesized handler `*ir.Func` plus a `lower.scheduleTimer(id, intervalMs, handler)` call. When `Timer.Enabled` is set, gate the schedule call on the Enabled value and inject schedule/cancel pairs after every Assign that mutates the Enabled Var.

**Architecture:** The pass synthesizes a single `*ir.Func{Receiver: "lower", Name: "scheduleTimer", Intrinsic: "LowerScheduleTimer"}` and a matching `lower.cancelTimer` Func, holding both as state. Calls reference these synthesized Funcs directly — no stdlib changes needed in v1 (`lower.*` is a reserved namespace for pass output; users cannot call these from source today). Goldens render `lower.scheduleTimer(...)` via ir.Convert's bare-call path.

**Tech Stack:** Go (Go 1.24+), existing `ir`/`ast`/`internal/checker`/`internal/parser`/`internal/lower` from prior phases.

**Reference spec:** `docs/superpowers/specs/2026-05-02-phase3-ir-shapes-design.md`

---

## Scope (v1)

In:
- `*ir.Timer` decls at component, window, and package level.
- `Timer.Interval` literal expressions (after Phase 2 NoUnit, these are int literals like `500`).
- `Timer.Enabled` referencing a single reactive Var via Ident.
- Per-Assign schedule/cancel injection when the Assign target is the Enabled Var.

Deferred:
- Compound Enabled expressions (`x && y`).
- Timer at root scope without an owning component.
- Timer.Interval as a non-literal expression (e.g., `someVar`).

## File Structure

**Create:**
- `internal/lower/testdata/timer_basic.txtar`
- `internal/lower/testdata/timer_enabled_gate.txtar`

**Modify:**
- `internal/lower/timer.go` — replace stub.

---

### Task 1: NoTimer implementation

**Files:**
- Modify: `internal/lower/timer.go`

- [ ] **Step 1: Implement the pass**

Replace `internal/lower/timer.go` with:

```go
package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passTimer = pass{
	name:    "NoTimer",
	enabled: func(c Caps) bool { return c.NoTimer },
	apply:   lowerTimer,
}

// lowerTimer walks every *ir.Timer decl, replaces it with a synthesized
// handler *ir.Func plus a lower.scheduleTimer call, and injects
// schedule/cancel pairs after Assigns that mutate the Enabled Var.
func lowerTimer(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	st := newTimerState()
	st.processOwner(&pkg.Funcs, &pkg.Timers, nil, nil)
	for _, comp := range pkg.Components {
		st.processOwner(&comp.Funcs, &comp.Timers, &comp.Body, nil)
	}
	for _, w := range pkg.Windows {
		// Windows don't have their own Timers slice; only the component
		// hierarchy carries timers. Walk window body to inject Enabled
		// updaters (handled by injectEnabledUpdaters).
		_ = w
	}
	st.injectEnabledUpdaters(pkg)
	return nil
}

type timerState struct {
	// schedule/cancel are the synthesized intrinsic Funcs reused across
	// every Call site. Allocated lazily.
	schedule *ir.Func
	cancel   *ir.Func
	// timersByEnabledVar maps each Var that gates a timer to its info.
	timersByEnabledVar map[*ir.Var][]gatedTimer
	// nextID is the next int Timer ID.
	nextID int
}

type gatedTimer struct {
	id           int
	intervalMs   int
	handlerName  string
	handlerFunc  *ir.Func
	enabledIdent *ir.Ident
}

func newTimerState() *timerState {
	return &timerState{
		timersByEnabledVar: make(map[*ir.Var][]gatedTimer),
	}
}

func (st *timerState) scheduleFunc() *ir.Func {
	if st.schedule == nil {
		st.schedule = &ir.Func{
			Name:      "lower.scheduleTimer",
			Intrinsic: "LowerScheduleTimer",
			Params: []*ir.Param{
				{Name: "id", Type: ir.TypInt},
				{Name: "intervalMs", Type: ir.TypInt},
				{Name: "handler", Type: ir.TypDyn},
			},
			Return: ir.TypVoid,
		}
	}
	return st.schedule
}

func (st *timerState) cancelFunc() *ir.Func {
	if st.cancel == nil {
		st.cancel = &ir.Func{
			Name:      "lower.cancelTimer",
			Intrinsic: "LowerCancelTimer",
			Params: []*ir.Param{
				{Name: "id", Type: ir.TypInt},
			},
			Return: ir.TypVoid,
		}
	}
	return st.cancel
}

// processOwner walks one owner (package, component, window) replacing its
// Timer decls with handler funcs and prepending scheduleTimer calls into the
// body.
func (st *timerState) processOwner(funcs *[]*ir.Func, timers *[]*ir.Timer, body *[]ir.Stmt, _ *ir.Window) {
	if len(*timers) == 0 {
		return
	}
	var newCalls []ir.Stmt
	for _, t := range *timers {
		id := st.nextID
		st.nextID++

		handlerName := "__timer" + strconv.Itoa(id) + "_handler"
		var handlerBlock []ir.Stmt
		if t.Handler != nil {
			handlerBlock = t.Handler.Block
		}
		handlerFunc := &ir.Func{
			Name:   handlerName,
			Block:  handlerBlock,
			Return: ir.TypVoid,
		}
		*funcs = append(*funcs, handlerFunc)

		intervalMs := extractIntervalMs(t.Interval)

		scheduleCall := &ir.CallStmt{
			Call: &ir.Call{
				Type: ir.TypVoid,
				Func: st.scheduleFunc(),
				Args: []ir.CallArg{
					{Value: intLiteralLit(id)},
					{Value: intLiteralLit(intervalMs)},
					{Value: &ir.Ident{Name: handlerName, Type: ir.TypDyn, Sym: handlerFunc}},
				},
			},
		}

		var stmt ir.Stmt = scheduleCall
		if t.Enabled != nil {
			stmt = &ir.If{
				Cond: t.Enabled,
				Body: []ir.Stmt{scheduleCall},
			}
			// Record so injectEnabledUpdaters can emit schedule/cancel pairs.
			if id, ok := t.Enabled.(*ir.Ident); ok {
				if v, ok := id.Sym.(*ir.Var); ok {
					st.timersByEnabledVar[v] = append(st.timersByEnabledVar[v], gatedTimer{
						id:           st.nextID - 1,
						intervalMs:   intervalMs,
						handlerName:  handlerName,
						handlerFunc:  handlerFunc,
						enabledIdent: id,
					})
				}
			}
		}
		newCalls = append(newCalls, stmt)
	}
	*timers = nil
	if body != nil {
		*body = append(newCalls, *body...)
	}
}

// extractIntervalMs reads the interval literal from a Timer.Interval expr.
// After Phase 2 NoUnit, interval literals are plain int Literals.
func extractIntervalMs(e ir.Expr) int {
	if lit, ok := e.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeInt {
		if n, err := strconv.Atoi(lit.Raw); err == nil {
			return n
		}
	}
	return 0
}

// injectEnabledUpdaters walks every Stmt slice, splicing schedule/cancel
// pairs after Assigns that mutate a gating Var.
func (st *timerState) injectEnabledUpdaters(pkg *ir.Package) {
	if len(st.timersByEnabledVar) == 0 {
		return
	}
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt {
			return st.injectIntoStmts(stmts)
		},
	})
}

func (st *timerState) injectIntoStmts(stmts []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		out = append(out, s)
		switch n := s.(type) {
		case *ir.If:
			n.Body = st.injectIntoStmts(n.Body)
			n.Else = st.injectIntoStmts(n.Else)
		case *ir.For:
			n.Body = st.injectIntoStmts(n.Body)
			n.Else = st.injectIntoStmts(n.Else)
		case *ir.PlatformFilter:
			n.Body = st.injectIntoStmts(n.Body)
		case *ir.NodeInst:
			n.Children = st.injectIntoStmts(n.Children)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = st.injectIntoStmts(n.Handlers[i].Func.Block)
				}
			}
		case *ir.SlotInst:
			n.Children = st.injectIntoStmts(n.Children)
		case *ir.ErrorBoundary:
			n.Children = st.injectIntoStmts(n.Children)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = st.injectIntoStmts(n.Handler.Func.Block)
			}
		}
		out = append(out, st.gatedUpdatersFor(s)...)
	}
	return out
}

func (st *timerState) gatedUpdatersFor(s ir.Stmt) []ir.Stmt {
	a, ok := s.(*ir.Assign)
	if !ok {
		return nil
	}
	id, ok := a.Target.(*ir.Ident)
	if !ok {
		return nil
	}
	v, ok := id.Sym.(*ir.Var)
	if !ok {
		return nil
	}
	gated, ok := st.timersByEnabledVar[v]
	if !ok {
		return nil
	}
	var out []ir.Stmt
	for _, gt := range gated {
		// if newValue { schedule(...) } else { cancel(id) }
		out = append(out, &ir.If{
			Cond: &ir.Ident{Name: id.Name, Type: ir.TypBool, Sym: v},
			Body: []ir.Stmt{
				&ir.CallStmt{
					Call: &ir.Call{
						Type: ir.TypVoid,
						Func: st.scheduleFunc(),
						Args: []ir.CallArg{
							{Value: intLiteralLit(gt.id)},
							{Value: intLiteralLit(gt.intervalMs)},
							{Value: &ir.Ident{Name: gt.handlerName, Type: ir.TypDyn, Sym: gt.handlerFunc}},
						},
					},
				},
			},
			Else: []ir.Stmt{
				&ir.CallStmt{
					Call: &ir.Call{
						Type: ir.TypVoid,
						Func: st.cancelFunc(),
						Args: []ir.CallArg{
							{Value: intLiteralLit(gt.id)},
						},
					},
				},
			},
		})
	}
	return out
}

func intLiteralLit(n int) *ir.Literal {
	return &ir.Literal{
		Type: ir.TypInt,
		Raw:  strconv.Itoa(n),
	}
}

// Suppress "declared but not used" warning on assign-only ast import. The
// import is used indirectly through ir types; keep it explicit so future
// edits don't drift.
var _ = ast.AssignSet
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/lower/...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/lower/timer.go
git commit -m "$(cat <<'EOF'
NoTimer Task 1: pass implementation

Replaces every *ir.Timer with a synthesized __timerN_handler Func plus a
lower.scheduleTimer(id, intervalMs, handler) CallStmt prepended to the
owning component body. When Timer.Enabled is set, the schedule call is
wrapped in an If on Enabled, and the gating Var is recorded for the
mutation-time injection pass.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Goldens

**Files:**
- Create: `internal/lower/testdata/timer_basic.txtar`
- Create: `internal/lower/testdata/timer_enabled_gate.txtar`

- [ ] **Step 1: Write basic timer fixture**

Create `internal/lower/testdata/timer_basic.txtar`:

```
caps: NoTimer, NoUnit
-- input.sngl --
component main {
    var n int = 0

    timer(interval=1s) {
        n = n + 1
    }

    text(value=string(n))
}
-- expected.sngl --
```

`NoUnit` is needed alongside so `1s` collapses to a plain int (Phase 2 contract). The pass ordering ensures NoUnit runs before NoTimer.

- [ ] **Step 2: Write Enabled-gate fixture**

Create `internal/lower/testdata/timer_enabled_gate.txtar`:

```
caps: NoTimer, NoUnit
-- input.sngl --
component main {
    var running bool = false
    var n int = 0

    timer(interval=500ms, enabled=running) {
        n = n + 1
    }

    button(@click { running = true })
}
-- expected.sngl --
```

- [ ] **Step 3: Update goldens**

Run: `go test ./internal/lower/ -run TestLower -update`
Expected: PASS.

- [ ] **Step 4: Inspect**

```bash
cat internal/lower/testdata/timer_basic.txtar
cat internal/lower/testdata/timer_enabled_gate.txtar
```

Expected for `timer_basic`:

```
component main {
    var n int = 0
    func __timer0_handler() {
        n = n + 1
    }
    lower.scheduleTimer(0, 1000, __timer0_handler)
    text(value=string(n))
}
```

Expected for `timer_enabled_gate`: schedule call wrapped in `if running { ... }`, plus an `if running { schedule(...) } else { cancelTimer(0) }` after the click handler's `running = true` Assign.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/lower/`
Expected: PASS.

- [ ] **Step 6: Run full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/lower/testdata/timer_basic.txtar internal/lower/testdata/timer_enabled_gate.txtar
git commit -m "$(cat <<'EOF'
NoTimer goldens

Two fixtures: bare timer (replaced by handler Func + scheduleTimer call)
and Enabled-gated timer (schedule wrapped in if-on-Enabled, plus
schedule/cancel injection at Enabled mutation sites).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Final verification

- [ ] **Step 1: Run full project verify**

Run: `go tool verify`
Expected: PASS.

---

## Self-Review Notes

Spec coverage (`docs/superpowers/specs/2026-05-02-phase3-ir-shapes-design.md`):
- §Per-pass behavior / NoTimer steps 1–4 → Tasks 1+2.

Risks:

1. **`lower.scheduleTimer` is a synthesized Func, not stdlib-resolvable.** v1 lowers without stdlib changes. If a future user wants to call `lower.scheduleTimer` from source, the checker would reject it (no such namespace). Phase 3d or a follow-up adds `lib/lower.sngl` registration if needed.

2. **Func.Name = "lower.scheduleTimer"`** with a literal dot causes ir.Convert to emit an IdentExpr whose Name contains a dot. The formatter prints it verbatim — readable in goldens, but parser would re-tokenize as Select on roundtrip. Goldens never re-parse the lowered output, so this is fine for now.

3. **Window timers ignored.** The processOwner skip for windows matches the current IR shape (windows don't have a Timers slice). If that changes, walkOwner extends.

4. **Enabled-gating Assign placement.** Emitted after the Assign rather than wrapping it; matches the order semantics ("when this var changes, do that") and pairs naturally with NoReactivity's prop-update injection (which also runs after).
