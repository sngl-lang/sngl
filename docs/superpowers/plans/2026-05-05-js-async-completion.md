# JS Async Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make async-transparent compilation work end-to-end for the JavaScript target so SNGL programs that transitively call async natives produce correct JS — `async`/`await` placed wherever needed, and async-in-reactive contexts lowered to a settled-state-field pattern.

**Architecture:** One IR helper (`ir.BlockHasAsyncCall`) feeds three consumers — the existing checker pass (extended for lambdas), the JS call-site translator (adds `await` for SNGL→SNGL async calls), and the html codegen (adds `async` to function declarations / handlers / setters / timers). A new lowering pass `NoAsyncReactive` rewrites every reactive context that contains async into a synthetic state field plus a fire-and-forget kicker, so updaters and computeds stay synchronous. Checker gains three rules to reject the cases lowering does not cover.

**Tech Stack:** Go (compiler), SNGL fixtures (txtar), Go test goldens, CDP browser test for end-to-end.

**Spec:** [`docs/superpowers/specs/2026-05-05-js-async-completion-design.md`](../specs/2026-05-05-js-async-completion-design.md)

**Sub-task of:** [#39 Concurrency model](https://git.duckfam.us/jonathan/sngl/-/issues/39). Sibling sub-tasks (out of scope here): #47 closure points-to, #48 monomorphization, #49 parallelization, #50 C# target.

---

## File Structure

**Create:**
- `ir/async.go` — exported `BlockHasAsyncCall`, `StmtHasAsyncCall`, `ExprHasAsyncCall`. Lambda-aware.
- `internal/lower/async_reactive.go` — `passAsyncReactive`, the `NoAsyncReactive` lowering. Two cases: named zero-arg computed; inline reactive subexpr in visual tree (hoisted to `__hoist_N` then folded into case 1).
- `internal/lower/async_reactive_test.go` — golden tests for the pass.
- `cmd/sngl/testdata/async_native_call.txt` — txtar end-to-end.
- `cmd/sngl/testdata/async_handler.txt`
- `cmd/sngl/testdata/async_setter.txt`
- `cmd/sngl/testdata/async_timer.txt`
- `cmd/sngl/testdata/async_computed_lowered.txt`
- `cmd/sngl/testdata/async_inline_reactive_lowered.txt`
- `cmd/sngl/testdata/async_in_computed_with_param.txt`
- `cmd/sngl/testdata/async_lambda_propagates.txt`
- `cmd/sngl/testdata/async_reserved_prefix.txt`

**Modify:**
- `internal/checker/async.go` — replace inline walkers with calls into `ir/async.go`; add lambda body visiting.
- `internal/checker/checker.go` (or wherever post-checks live) — three new rules: parameterized async-reactive, reserved `__async_` / `__hoist_` prefix, async fn into sync fn slot.
- `internal/lower/caps.go` — add `NoAsyncReactive bool`; merge + String.
- `internal/lower/lower.go` — register `passAsyncReactive` before `passComputed`.
- `codegen/platform/html/html.go` — request `NoAsyncReactive` cap; mark `async function` at: `emitJSFunc`, `addEventListener` wrappers, `$set_X`, `$timer_N_tick`, `$timer_N_sync`. Add codegen-side panic guard if any updater body or struct ctor still contains async after lowering.
- `codegen/lang/javascript/translate_ir.go` — `await` for SNGL plain calls and method calls when callee `IsAsync`.

---

## Conventions used in this plan

- Run a single test: `go test -run TestName ./pkg/...`
- Run full suite: `go tool verify`
- Build: `go install ./cmd/sngl`
- Goldens regenerate: `go test -update ./...` (where supported)
- Commit after each task, never amend.

---

### Task 1: Extract async walker into `ir/async.go`

**Files:**
- Create: `ir/async.go`
- Test: `ir/async_test.go`
- Modify: `internal/checker/async.go`

- [ ] **Step 1: Write failing test for `ir.BlockHasAsyncCall`**

```go
// ir/async_test.go
package ir_test

import (
    "testing"

    "git.duckfam.us/jonathan/sngl/ir"
)

func TestBlockHasAsyncCall_DirectCall(t *testing.T) {
    asyncFn := &ir.Func{Name: "fetchHello", IsAsync: true}
    syncFn := &ir.Func{Name: "noop", IsAsync: false}

    block := []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: asyncFn}}}
    if !ir.BlockHasAsyncCall(block) {
        t.Fatalf("expected async detected")
    }

    block2 := []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: syncFn}}}
    if ir.BlockHasAsyncCall(block2) {
        t.Fatalf("expected no async")
    }
}
```

- [ ] **Step 2: Run test**

Run: `go test -run TestBlockHasAsyncCall_DirectCall ./ir/`
Expected: FAIL — `BlockHasAsyncCall` undefined.

- [ ] **Step 3: Implement `ir/async.go`**

```go
// Package-level documentation:
// async.go: shared "does this body transitively contain an async call"
// helpers. Used by the checker's color-propagation pass and by the
// html platform's codegen to decide async-keyword placement.
package ir

import "slices"

func BlockHasAsyncCall(stmts []Stmt) bool {
    return slices.ContainsFunc(stmts, StmtHasAsyncCall)
}

func StmtHasAsyncCall(s Stmt) bool {
    switch x := s.(type) {
    case *CallStmt:
        return ExprHasAsyncCall(x.Call)
    case *Assign:
        return ExprHasAsyncCall(x.Value)
    case *LocalVar:
        return ExprHasAsyncCall(x.Init)
    case *Return:
        return ExprHasAsyncCall(x.Value)
    case *If:
        return ExprHasAsyncCall(x.Cond) || BlockHasAsyncCall(x.Body) || BlockHasAsyncCall(x.Else)
    case *For:
        return ExprHasAsyncCall(x.Iter) || BlockHasAsyncCall(x.Body) || BlockHasAsyncCall(x.Else)
    case *PlatformFilter:
        return BlockHasAsyncCall(x.Body)
    }
    return false
}

func ExprHasAsyncCall(e Expr) bool {
    if e == nil {
        return false
    }
    switch x := e.(type) {
    case *Call:
        if x.Func != nil && x.Func.IsAsync {
            return true
        }
        for _, a := range x.Args {
            if ExprHasAsyncCall(a.Value) {
                return true
            }
        }
        if x.Receiver != nil && ExprHasAsyncCall(x.Receiver) {
            return true
        }
    case *Binary:
        return ExprHasAsyncCall(x.Left) || ExprHasAsyncCall(x.Right)
    case *Unary:
        return ExprHasAsyncCall(x.Operand)
    case *Ternary:
        return ExprHasAsyncCall(x.Cond) || ExprHasAsyncCall(x.Then) || ExprHasAsyncCall(x.Else)
    case *Conversion:
        return ExprHasAsyncCall(x.Operand)
    case *Select:
        return ExprHasAsyncCall(x.Operand)
    case *Index:
        return ExprHasAsyncCall(x.Operand) || ExprHasAsyncCall(x.Idx)
    case *ListLit:
        if slices.ContainsFunc(x.Elems, ExprHasAsyncCall) {
            return true
        }
    case *StructLit:
        for _, f := range x.Fields {
            if ExprHasAsyncCall(f.Value) {
                return true
            }
        }
    case *Spread:
        return ExprHasAsyncCall(x.Operand)
    case *Lambda:
        return BlockHasAsyncCall(x.Func.Block)
    case *Closure:
        if x.Func != nil {
            return BlockHasAsyncCall(x.Func.Block)
        }
    }
    return false
}
```

- [ ] **Step 4: Run test**

Run: `go test -run TestBlockHasAsyncCall_DirectCall ./ir/`
Expected: PASS.

- [ ] **Step 5: Replace checker walker with shared helper**

In `internal/checker/async.go`, replace the inline `blockHasAsyncCall`/`stmtHasAsyncCall`/`exprHasAsyncCall` (lines 38–105) with delegations:

```go
package checker

import "git.duckfam.us/jonathan/sngl/ir"

func (c *checker) analyzeAsync() {
    pkg := c.pkg
    if pkg == nil {
        return
    }
    funcs := allFuncs(pkg)
    for {
        changed := false
        for _, fn := range funcs {
            if fn.IsAsync {
                continue
            }
            if ir.BlockHasAsyncCall(fn.Block) {
                fn.IsAsync = true
                changed = true
            }
        }
        if !changed {
            break
        }
    }
}
```

Delete the now-unused private walkers from this file.

- [ ] **Step 6: Run full suite to confirm no regressions**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add ir/async.go ir/async_test.go internal/checker/async.go
git commit -m "ir: extract BlockHasAsyncCall as shared helper; checker delegates"
```

---

### Task 2: Lambda-body propagation

**Goal:** A function whose body contains a lambda whose body calls async should itself become async (lambdas are not separate funcs from a coloring perspective today; they capture the enclosing). Confirm via test, fix if needed.

**Files:**
- Test: `internal/checker/async_test.go` (create if absent)
- Modify (if needed): `ir/async.go` (already covered in Task 1 — `case *Lambda`).

- [ ] **Step 1: Write failing test**

```go
// internal/checker/async_test.go
package checker_test

import (
    "testing"

    "git.duckfam.us/jonathan/sngl/sngl"
)

func TestAsyncPropagatesThroughLambda(t *testing.T) {
    src := `
import { fetchHello } from "js://app/api"  // declared async
fn outer() {
    let f = () => { fetchHello() }
    f()
}
`
    pkg := mustCheck(t, src)
    outer := mustFindFunc(t, pkg, "outer")
    if !outer.IsAsync {
        t.Fatalf("outer should be async (calls f, lambda containing async)")
    }
}
```

(Use existing test helpers; if `mustCheck`/`mustFindFunc` do not exist, add minimal helpers in `internal/checker/testutil_test.go` mirroring patterns in `internal/checker/checker_test.go`.)

- [ ] **Step 2: Run test**

Run: `go test -run TestAsyncPropagatesThroughLambda ./internal/checker/`
Expected: PASS if Task 1's `case *Lambda:` already covers it. FAIL means either the lambda is wrapped in a `Closure` post-NoLambda (it shouldn't be — checker runs before lower) or a different IR node is used. If fail, inspect `ir/expr.go:Lambda` and update `ir/async.go` accordingly.

- [ ] **Step 3: If fail, fix `ir/async.go`**

Add the missing case in `ExprHasAsyncCall` and re-run.

- [ ] **Step 4: Commit (only if changes made)**

```bash
git add internal/checker/async_test.go ir/async.go
git commit -m "checker: cover lambda bodies in async propagation"
```

If no fix needed, still commit the test:

```bash
git add internal/checker/async_test.go
git commit -m "checker: regression test for async propagation through lambda"
```

---

### Task 3: Emit `await` for SNGL→SNGL async plain calls

**Files:**
- Modify: `codegen/lang/javascript/translate_ir.go`
- Test: `codegen/lang/javascript/javascript_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestTranslateIRPlainCall_AwaitsAsyncSNGLCallee(t *testing.T) {
    asyncFn := &ir.Func{Name: "loadUser", IsAsync: true}
    call := &ir.Call{Func: asyncFn, Args: nil}
    scope := &codegen.ExprScope{}

    got := javascript.TranslateIRPlainCallForTest(call, scope) // see step 2

    want := "await loadUser()"
    if got != want {
        t.Fatalf("got %q want %q", got, want)
    }
}
```

- [ ] **Step 2: Expose translator for tests**

In `codegen/lang/javascript/translate_ir.go`, ensure the plain-call translator is named `translateIRPlainCall` and add a test export:

```go
// translate_ir_export_test.go
package javascript

import (
    "git.duckfam.us/jonathan/sngl/codegen"
    "git.duckfam.us/jonathan/sngl/ir"
)

func TranslateIRPlainCallForTest(c *ir.Call, scope *codegen.ExprScope) string {
    return translateIRPlainCall(c, scope)
}
```

- [ ] **Step 3: Run test**

Run: `go test -run TestTranslateIRPlainCall_AwaitsAsyncSNGLCallee ./codegen/lang/javascript/`
Expected: FAIL — current output is `"loadUser()"`.

- [ ] **Step 4: Implement**

In `codegen/lang/javascript/translate_ir.go`, find `translateIRPlainCall` and add an `await` wrap mirroring `translateIRNativeCall` (line 214):

```go
// inside translateIRPlainCall, just before `return ...`
call := fn + "(" + strings.Join(argStrs, ", ") + ")"
if n.Func != nil && n.Func.IsAsync {
    call = "await " + call
}
return call
```

(If the function is currently structured as a single-line return, refactor to use the `call` local variable.)

- [ ] **Step 5: Run test**

Run: `go test -run TestTranslateIRPlainCall_AwaitsAsyncSNGLCallee ./codegen/lang/javascript/`
Expected: PASS.

- [ ] **Step 6: Apply same to namespace/method call**

In `translateIRNamespaceCall`, after building the call string, apply the same `IsAsync` check and `await` prefix.

- [ ] **Step 7: Add a test for the method-call path**

```go
func TestTranslateIRMethodCall_AwaitsAsync(t *testing.T) {
    asyncFn := &ir.Func{Name: "save", Receiver: "User", IsAsync: true}
    call := &ir.Call{Func: asyncFn, Receiver: &ir.Ident{Name: "u"}}
    got := javascript.TranslateIRNamespaceCallForTest(call, &codegen.ExprScope{})
    want := "await u.save()"
    if got != want {
        t.Fatalf("got %q want %q", got, want)
    }
}
```

Add a corresponding `TranslateIRNamespaceCallForTest` export in `translate_ir_export_test.go`.

- [ ] **Step 8: Run + commit**

```bash
go test ./codegen/lang/javascript/...
git add codegen/lang/javascript/
git commit -m "js: await SNGL->SNGL async calls in plain and namespace forms"
```

---

### Task 4: `async function` for user-defined SNGL funcs

**Files:**
- Modify: `codegen/platform/html/html.go` (function `emitJSFunc` at line 3155)
- Test: golden via `cmd/sngl/testdata/async_native_call.txt` (Task 11)
- Unit test: `codegen/platform/html/html_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestEmitJSFunc_AsyncKeyword(t *testing.T) {
    g := newTestHtmlGen(t)
    fn := &ir.Func{
        Name: "loadAll",
        IsAsync: true,
        Block: []ir.Stmt{&ir.Return{Value: &ir.LiteralExpr{...}}}, // any body
    }
    var b strings.Builder
    g.emitJSFuncForTest(&b, fn)
    if !strings.HasPrefix(b.String(), "async function loadAll") {
        t.Fatalf("missing async keyword: %q", b.String())
    }
}
```

(Add `emitJSFuncForTest` export in a `*_export_test.go` file: `func (g *htmlGen) emitJSFuncForTest(b *strings.Builder, fn *ir.Func) { g.emitJSFunc(b, fn) }`. Reuse `newTestHtmlGen` if present; otherwise add a minimal constructor in test file.)

- [ ] **Step 2: Run test**

Run: `go test -run TestEmitJSFunc_AsyncKeyword ./codegen/platform/html/`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `codegen/platform/html/html.go:emitJSFunc`, change every `Fprintf(b, "function %s(...) ...")` to use `keyword`:

```go
keyword := "function"
if fn.IsAsync {
    keyword = "async function"
}
// expression body:
fmt.Fprintf(b, "%s %s(%s) { return %s; }\n", keyword, jsName, paramStr, body)
// statement body:
fmt.Fprintf(b, "%s %s(%s) {\n", keyword, jsName, paramStr)
```

- [ ] **Step 4: Run test**

Run: `go test -run TestEmitJSFunc_AsyncKeyword ./codegen/platform/html/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/html/
git commit -m "html: emit async function for IsAsync user funcs"
```

---

### Task 5: `async function` for event handler wrappers

**Files:**
- Modify: `codegen/platform/html/html.go` lines 2421/2423 (handler wrappers).
- Test: `codegen/platform/html/html_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestEventHandler_AsyncWrapper(t *testing.T) {
    g := newTestHtmlGen(t)
    g.handlers = []handler{{
        elemID: "btn1",
        event:  "click",
        body:   "await loadAll();",
        mutated: nil,
    }}
    var b strings.Builder
    g.emitHandlersForTest(&b)
    out := b.String()
    if !strings.Contains(out, `addEventListener("click", async function`) {
        t.Fatalf("expected async wrapper, got: %s", out)
    }
}
```

(Body containing `await ` is a sufficient signal during this unit test. In production code we use `ir.BlockHasAsyncCall` over the IR statements that produced this body; see step 3.)

- [ ] **Step 2: Inspect the handler structure**

Read `codegen/platform/html/html.go` around the `g.handlers` declaration to find the field that retains the IR statements for this handler. If the IR has been flattened to `body string` only, add a sibling field `bodyStmts []ir.Stmt` (preferred) or a precomputed `isAsync bool` set when the handler is collected. Pick one; use it consistently.

- [ ] **Step 3: Implement at the collection site**

Wherever a `handler` is appended (search for `g.handlers = append`), set `isAsync` from `ir.BlockHasAsyncCall(handlerStmts)`.

At the emit site (lines 2420–2424), select the keyword:

```go
keyword := "function"
if h.isAsync {
    keyword = "async function"
}
if h.event == "input" {
    fmt.Fprintf(b, "%s.addEventListener(\"%s\", %s(e) {\n  %s\n});\n",
        h.elemID, h.event, keyword, body)
} else {
    fmt.Fprintf(b, "%s.addEventListener(\"%s\", %s() {\n  %s\n});\n",
        h.elemID, h.event, keyword, body)
}
```

- [ ] **Step 4: Run test + commit**

```bash
go test -run TestEventHandler ./codegen/platform/html/
git add codegen/platform/html/
git commit -m "html: async wrapper for event handlers with async bodies"
```

---

### Task 6: `async function` for setters `$set_X`

**Files:**
- Modify: `codegen/platform/html/html.go` near line 2325.

- [ ] **Step 1: Write failing test**

```go
func TestSetter_AsyncWhenChangeHandlerAsync(t *testing.T) {
    g := newTestHtmlGen(t)
    asyncFn := &ir.Func{Name: "save", IsAsync: true}
    g.stateVars = []stateVar{{
        Name: "title",
        Type: stringType,
        Handlers: []handler{{
            Name: "change",
            Func: &ir.Func{Block: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: asyncFn}}}},
        }},
    }}
    var b strings.Builder
    g.emitSettersForTest(&b)
    if !strings.Contains(b.String(), "async function $set_title(") {
        t.Fatalf("setter not async: %s", b.String())
    }
}
```

- [ ] **Step 2: Run test (FAIL)**

Run: `go test -run TestSetter_AsyncWhenChangeHandlerAsync ./codegen/platform/html/`

- [ ] **Step 3: Implement**

At the `function $set_X(v) {` emission, compute async-ness across everything inlined into the setter:

```go
setterAsync := false
for _, h := range dv.Handlers {
    if h.Name == "change" && h.Func != nil && ir.BlockHasAsyncCall(h.Func.Block) {
        setterAsync = true
    }
}
// (Updaters and timer-sync hooks are sync after Task 8; no need to re-check.)
keyword := "function"
if setterAsync {
    keyword = "async function"
}
fmt.Fprintf(b, "%s $set_%s(v) {\n", keyword, dv.Name)
```

- [ ] **Step 4: Run test + commit**

```bash
go test -run TestSetter_ ./codegen/platform/html/
git add codegen/platform/html/
git commit -m "html: async setter when change handler transitively async"
```

---

### Task 7: `async function` for timer ticks

**Files:**
- Modify: `codegen/platform/html/html.go` near lines 2440–2441.

- [ ] **Step 1: Write failing test**

```go
func TestTimerTick_AsyncWhenBodyAsync(t *testing.T) {
    g := newTestHtmlGen(t)
    asyncFn := &ir.Func{Name: "poll", IsAsync: true}
    g.timers = []timer{{
        index: 0,
        bodyStmts: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: asyncFn}}},
        body: "await poll();",
    }}
    var b strings.Builder
    g.emitTimersForTest(&b)
    out := b.String()
    if !strings.Contains(out, "async function $timer_0_tick") {
        t.Fatalf("tick not async: %s", out)
    }
}
```

- [ ] **Step 2: Add `bodyStmts` to `timer` struct (if absent)**

Look at how `timer.body` is currently constructed; capture the original IR statements alongside.

- [ ] **Step 3: Run test (FAIL)**

- [ ] **Step 4: Implement**

```go
tickKw := "function"
if ir.BlockHasAsyncCall(t.bodyStmts) {
    tickKw = "async function"
}
fmt.Fprintf(b, "%s $timer_%d_tick() {\n  %s\n}\n", tickKw, t.index, tickBody)
// $timer_N_sync stays synchronous (it just calls the tick + setTimeout)
```

- [ ] **Step 5: Run test + commit**

```bash
git add codegen/platform/html/
git commit -m "html: async timer tick when body transitively async"
```

---

### Task 8: New `NoAsyncReactive` cap + lowering for named computeds

**Files:**
- Modify: `internal/lower/caps.go`
- Modify: `internal/lower/lower.go`
- Create: `internal/lower/async_reactive.go`
- Create: `internal/lower/async_reactive_test.go`
- Modify: `codegen/platform/html/html.go` (request the cap)

- [ ] **Step 1: Add cap field**

In `internal/lower/caps.go`:

```go
type Caps struct {
    // ... existing ...
    NoAsyncReactive bool // async in reactive contexts → settled state-field + kicker
}
```

Update `Merge` and `String` to include it (insert in `String` between `NoComputed` and `NoLambda` — early, runs before NoComputed).

- [ ] **Step 2: Register pass**

In `internal/lower/lower.go`, declare `passAsyncReactive` and insert it **before** `passComputed` in the `passes` slice (since after this pass, async computeds are sync).

```go
var passAsyncReactive = pass{
    name:    "NoAsyncReactive",
    enabled: func(c Caps) bool { return c.NoAsyncReactive },
    apply:   lowerAsyncReactive,
}
```

Update the rationale comment block.

- [ ] **Step 3: Write the failing pass test (named computed only)**

```go
// internal/lower/async_reactive_test.go
package lower

import (
    "testing"

    "git.duckfam.us/jonathan/sngl/ir"
)

func TestLowerAsyncReactive_NamedComputed(t *testing.T) {
    asyncNative := &ir.Func{Name: "fetchHello", IsAsync: true, Native: true}
    greeting := &ir.Func{
        Name: "greeting",
        // Zero-arg expression-body computed.
        Block: []ir.Stmt{&ir.Return{Value: &ir.Call{Func: asyncNative}}},
        Sig: &ir.FuncSig{Return: stringType()},
    }
    pkg := &ir.Package{Funcs: []*ir.Func{greeting, asyncNative}}

    if err := lowerAsyncReactive(pkg, Caps{NoAsyncReactive: true}); err != nil {
        t.Fatalf("pass error: %v", err)
    }

    // After lowering:
    // - a synthetic state field "__async_greeting" exists with zero value
    // - a kicker func "$compute_greeting" exists, IsAsync, body assigns the result
    // - greeting() now returns __async_greeting (sync)
    if !findStateField(pkg, "__async_greeting") {
        t.Fatalf("expected synthetic state field __async_greeting")
    }
    kicker := findFunc(pkg, "$compute_greeting")
    if kicker == nil || !kicker.IsAsync {
        t.Fatalf("expected async kicker $compute_greeting")
    }
    if ir.BlockHasAsyncCall(greeting.Block) {
        t.Fatalf("greeting still has async after lowering")
    }
}
```

(Helpers `findStateField`, `findFunc`, `stringType` go in a small `testutil_test.go` in the same package.)

- [ ] **Step 4: Run test (FAIL — pass not implemented)**

Run: `go test -run TestLowerAsyncReactive_NamedComputed ./internal/lower/`

- [ ] **Step 5: Implement the pass — named computed only**

```go
// internal/lower/async_reactive.go
package lower

import (
    "fmt"

    "git.duckfam.us/jonathan/sngl/ir"
)

func lowerAsyncReactive(pkg *ir.Package, _ Caps) error {
    if pkg == nil {
        return nil
    }
    var work []*ir.Func
    for _, fn := range pkg.Funcs {
        if isReactiveAsyncComputed(fn) {
            work = append(work, fn)
        }
    }
    for _, fn := range work {
        if err := lowerNamedComputed(pkg, fn); err != nil {
            return err
        }
    }
    return nil
}

// isReactiveAsyncComputed: zero-param, expression-body, body has async call.
func isReactiveAsyncComputed(fn *ir.Func) bool {
    if fn == nil || fn.IsTest || len(fn.Params) != 0 || fn.AST == nil || fn.AST.Body == nil {
        return false
    }
    return ir.BlockHasAsyncCall(fn.Block)
}

func lowerNamedComputed(pkg *ir.Package, fn *ir.Func) error {
    retType := fn.Sig.Return
    zero, err := zeroValueExpr(retType)
    if err != nil {
        return fmt.Errorf("lowerAsyncReactive: %s: %w", fn.Name, err)
    }
    syntheticVar := "__async_" + fn.Name
    pkg.StateVars = append(pkg.StateVars, &ir.Var{
        Name: syntheticVar,
        Type: retType,
        Init: zero,
    })

    // Kicker function: async, body = `__async_<n> = <orig body expression>`
    origRet := fn.Block[0].(*ir.Return) // guaranteed by isReactiveAsyncComputed
    kicker := &ir.Func{
        Name:    "$compute_" + fn.Name,
        IsAsync: true,
        Block: []ir.Stmt{
            &ir.Assign{
                Target: &ir.Ident{Name: syntheticVar},
                Value:  origRet.Value,
            },
        },
        Sig: &ir.FuncSig{Return: nil}, // void
    }
    pkg.Funcs = append(pkg.Funcs, kicker)

    // Wire kicker into reactivity: register dependency on every reactive var
    // in origRet.Value so the kicker re-runs when those mutate. Mechanism
    // depends on existing reactivity-pass API. See Task 8b.

    // Register one-shot startup: append kicker name to pkg.StartupKickers.
    pkg.StartupKickers = append(pkg.StartupKickers, kicker.Name)

    // Replace the computed body with a synchronous read.
    fn.Block = []ir.Stmt{
        &ir.Return{Value: &ir.Ident{Name: syntheticVar}},
    }
    fn.IsAsync = false
    return nil
}

// zeroValueExpr returns the zero literal for t. Errors when t has no
// well-defined zero (e.g. an enum without a zero variant). Conservative
// by design — the user should make the fallback explicit.
func zeroValueExpr(t *ir.Type) (ir.Expr, error) {
    // Implementation: switch on t.Kind. Strings -> "", numerics -> 0,
    // bool -> false, list -> empty list, struct -> nil literal, enum w/
    // zero variant -> that variant, else -> error.
    panic("implement zeroValueExpr")
}
```

(`pkg.StateVars` and `pkg.StartupKickers` are placeholders for whatever shape the package uses — find the existing field for state vars (it may live on a Window or Document); `StartupKickers` may need to be a new field or may map to an existing onLoad-style hook in the html platform. See Task 8b for wiring.)

- [ ] **Step 6: Implement `zeroValueExpr`**

Add a switch over `ir.TypeKind` covering string, int, float, bool, list, struct, ref, optional/nullable, enum-with-zero-variant. For the enum-without-zero case, return:

```go
return nil, fmt.Errorf("type %s has no zero value; provide an explicit fallback", t.String())
```

Cover each branch with a unit test in `async_reactive_test.go`.

- [ ] **Step 7: Run test (PASS for named computed)**

Run: `go test -run TestLowerAsyncReactive_NamedComputed ./internal/lower/`

- [ ] **Step 8: Commit**

```bash
git add internal/lower/ codegen/platform/html/html.go
git commit -m "lower: NoAsyncReactive pass for named zero-arg computed"
```

---

### Task 8b: Wire the kicker to reactivity dependencies

**Files:**
- Modify: `internal/lower/async_reactive.go`
- Modify: `internal/lower/reactivity.go` (extend the reactive registry to include kicker funcs)

The reactivity pass collects "this expression depends on these state vars; when any mutate, re-run these updaters." We extend that to also re-run kickers.

- [ ] **Step 1: Read `internal/lower/reactivity.go`**

Identify the dependency-tracking data structure (search for `exprDeps`, `reactiveVars`). Determine how an updater is keyed and how the html platform discovers them at codegen time.

- [ ] **Step 2: Add a "reactive consumer" abstraction**

If the reactivity registry already supports a generic "rerun this code on dep change," reuse it. Otherwise add a sibling list `pkg.AsyncKickers []AsyncKicker` where `AsyncKicker = {FuncName string, Deps []string}` with `Deps` computed by calling `exprDeps` on the original computed body.

- [ ] **Step 3: Test that mutating a dep re-runs the kicker**

```go
func TestLowerAsyncReactive_KickerWiredToDeps(t *testing.T) {
    // Build a computed `fn greeting() => await fetchUser(state.userId)`.
    // After lowering, mutating state.userId should be registered as a
    // trigger for $compute_greeting.
    // Assert via the appropriate pkg field.
}
```

- [ ] **Step 4: Implement + run + commit**

```bash
git add internal/lower/
git commit -m "lower: wire async kickers to reactive dependencies"
```

---

### Task 8c: html platform consumes kickers

**Files:**
- Modify: `codegen/platform/html/html.go`

- [ ] **Step 1: Request the cap**

```go
func (g *Generator) Capabilities() lower.Caps {
    return lower.Caps{NoReactivity: true, NoAsyncReactive: true}
}
```

- [ ] **Step 2: Emit kickers**

In `html.go`, after emitting user funcs (around line 2300), iterate `pkg.AsyncKickers` (or whatever the registry exposes) and emit `async function $compute_X() { ... }` plus an init call at startup.

In each `$set_<dep>` setter, after the synchronous DOM-updater chain, call the relevant kickers (fire-and-forget — no `await`, since the setter need not block on settle).

- [ ] **Step 3: Test via golden**

A txtar test `cmd/sngl/testdata/async_computed_lowered.txt` (Task 11) exercises this end-to-end. No new unit test needed if the txtar verifies presence of the synthetic field, kicker, and read-rewrite.

- [ ] **Step 4: Commit**

```bash
git commit -am "html: emit kickers and wire to setters"
```

---

### Task 9: Inline reactive expr (case b) — hoist to `__hoist_N`

**Files:**
- Modify: `internal/lower/async_reactive.go`
- Modify: `internal/lower/async_reactive_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestLowerAsyncReactive_InlineHoist(t *testing.T) {
    // Build a Window with a Text node:
    //   Text("Hello, " + await fetchHello())
    // Expect after lowering:
    //   - synthetic computed `__hoist_0` zero-arg expression-body returning fetchHello()
    //   - inline expr rewritten to `"Hello, " + __hoist_0()`
    //   - then case (a) lowering applied, producing `__async___hoist_0` etc.
    ...
    if findFunc(pkg, "__hoist_0") == nil {
        t.Fatalf("expected synthetic hoist computed")
    }
    if findStateField(pkg, "__async___hoist_0") == nil {
        t.Fatalf("expected lowered hoist state var")
    }
}
```

- [ ] **Step 2: Run test (FAIL)**

- [ ] **Step 3: Implement hoist**

In `lowerAsyncReactive`, before the named-computed sweep, walk every reactive context (visual nodes' reactive subexpressions) and hoist any subexpression `e` such that:

1. `ir.ExprHasAsyncCall(e)` is true,
2. `e` is the maximal such subtree whose free identifiers are reactive deps (no enclosing-scope locals; no non-reactive params).

For each hoist site:

- generate a synthetic name `__hoist_N` (counter scoped to the package; must not collide with user names — verified by Task 10 reserved-prefix check).
- create `&ir.Func{Name: "__hoist_N", Block: []ir.Stmt{&ir.Return{Value: e}}, Sig: ...}` and append to `pkg.Funcs`.
- replace `e` in the visual tree with `&ir.Call{Func: thatFunc}`.

After hoisting, the named-computed sweep handles them uniformly.

Reuse the reactivity dependency analyzer to compute "free identifiers" in the candidate subtree. The algorithm: depth-first descent; at each node, ask "does any descendant call async?" — if yes, ask "are all free vars reactive deps?" — if yes, hoist this node and stop descending; if no (locals captured), continue descending into async-bearing children.

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/lower/...
git add internal/lower/
git commit -m "lower: hoist inline reactive async exprs into synthetic computeds"
```

---

### Task 10: Checker rules

**Files:**
- Modify: `internal/checker/checker.go` (or add `internal/checker/async_rules.go`)
- Test: `internal/checker/async_test.go`

Three rules:

1. **Parameterized async-reactive**: a non-zero-param computed/reactive func whose body transitively contains an async call — error `"async expression not allowed in parameterized reactive context"`.
2. **Reserved synthetic prefix**: any user-declared name starting with `__async_` or `__hoist_` — error `"name '%s' uses reserved prefix"`.
3. **Async fn into sync fn slot**: a value of an `IsAsync` func type (or a lambda containing async) being assigned/passed to a parameter / field whose declared func type is sync — error `"cannot pass async function where sync function expected"`.

- [ ] **Step 1: Write failing tests, one per rule**

```go
func TestCheckAsync_ParameterizedReactiveError(t *testing.T) {
    src := `
import { fetchHello } from "js://app/api"
@reactive fn greet(name: string): string => "hi " + await fetchHello()
`
    _, errs := tryCheck(t, src)
    requireError(t, errs, "async expression not allowed in parameterized reactive context")
}

func TestCheckAsync_ReservedPrefixError(t *testing.T) { /* user defines __async_foo */ }
func TestCheckAsync_AsyncIntoSyncSlotError(t *testing.T) { /* pass async fn to sync param */ }
```

(Helper `tryCheck` returns `(pkg, []error)` without `t.Fatal` on errors. Add if missing.)

- [ ] **Step 2: Run tests (FAIL)**

- [ ] **Step 3: Implement rules**

Each rule is a post-`analyzeAsync` walk:

```go
// internal/checker/async_rules.go
package checker

import (
    "strings"

    "git.duckfam.us/jonathan/sngl/ir"
)

func (c *checker) checkAsyncRules() {
    for _, fn := range allFuncs(c.pkg) {
        if strings.HasPrefix(fn.Name, "__async_") || strings.HasPrefix(fn.Name, "__hoist_") {
            c.errAt(fn.Pos(), "name %q uses reserved prefix", fn.Name)
        }
        if isReactive(fn) && len(fn.Params) > 0 && ir.BlockHasAsyncCall(fn.Block) {
            c.errAt(fn.Pos(), "async expression not allowed in parameterized reactive context")
        }
    }
    // Walk every assignment / call-arg / struct-field that targets a func type.
    // For each, check declared (target) func type's color vs actual expr's
    // async-ness. Actual is async iff: its IR is a *Call whose Func.IsAsync,
    // or a *Lambda/*Closure whose body has async, or an *Ident referring to
    // an async func.
    walkPackage(c.pkg, walkFuncs{...})
}
```

`isReactive`: for now, "computed" (zero-or-more-arg, expression body, no `@test`) approximates the reactive set the checker can see pre-lowering. The lowering pass narrows further.

`errAt` follows existing error-emission patterns in `checker.go`.

Wire `c.checkAsyncRules()` into the checker pipeline immediately after `c.analyzeAsync()`.

- [ ] **Step 4: Run tests + commit**

```bash
go test ./internal/checker/...
git add internal/checker/
git commit -m "checker: forbid parameterized async-reactive, reserved prefixes, async-into-sync-slot"
```

---

### Task 11: End-to-end txtar fixtures

Each fixture is a self-contained `cmd/sngl/testdata/*.txt` archive. Run with `go test ./cmd/sngl/`.

Format reference: existing `.txt` files in the same directory.

- [ ] **Step 1: `async_native_call.txt`**

```
sngl build --lang js --platform html .
exists out/index.html
stdout 'async function loadAll'
stdout 'await fetchHello'

-- main.sngl --
import { fetchHello } from "js://app/api"

fn loadAll() {
    fetchHello()
}

window MainWindow {
    button "Go" {
        @click { loadAll() }
    }
}

-- app/api.ts --
export async function fetchHello(): Promise<string> { return "hi"; }
```

(Adjust to match real txtar conventions in the repo — check an existing test like `cmd/sngl/testdata/build_*.txt` for the exact verb names and import path scheme.)

- [ ] **Step 2: `async_handler.txt`**

Asserts the `addEventListener("click", async function`.

- [ ] **Step 3: `async_setter.txt`**

State var with `@change` handler that calls async. Asserts `async function $set_X(`.

- [ ] **Step 4: `async_timer.txt`**

Timer ticking an async body. Asserts `async function $timer_0_tick`.

- [ ] **Step 5: `async_computed_lowered.txt`**

```
sngl dump optimized --lang js --platform html .
stdout 'var __async_greeting'
stdout '\$compute_greeting'
! stdout 'fn greeting.* => await'

sngl build --lang js --platform html .
stdout 'state.__async_greeting'
stdout 'async function \$compute_greeting'

-- main.sngl --
import { fetchHello } from "js://app/api"

fn greeting(): string => await fetchHello()

window MainWindow {
    text "{greeting()}"
}

-- app/api.ts --
export async function fetchHello(): Promise<string> { return "hi"; }
```

- [ ] **Step 6: `async_inline_reactive_lowered.txt`**

Async inline inside Text node. Asserts `__hoist_0` synthesized.

- [ ] **Step 7: `async_in_computed_with_param.txt`**

```
! sngl build --lang js --platform html .
stderr 'async expression not allowed in parameterized reactive context'

-- main.sngl --
import { fetchHello } from "js://app/api"

fn greet(name: string): string => name + " " + await fetchHello()

window MainWindow {
    text "{greet(\"world\")}"
}

-- app/api.ts --
export async function fetchHello(): Promise<string> { return "hi"; }
```

- [ ] **Step 8: `async_lambda_propagates.txt`**

Asserts that a func enclosing a lambda that calls async becomes async (i.e. `async function outer`).

- [ ] **Step 9: `async_reserved_prefix.txt`**

User defines `fn __async_foo() {}`. Expect checker error `name "__async_foo" uses reserved prefix`.

- [ ] **Step 10: Run all and commit**

```bash
go test ./cmd/sngl/
git add cmd/sngl/testdata/
git commit -m "test: txtar fixtures for js async completion"
```

---

### Task 12: CDP browser end-to-end

**Files:**
- Create: `codegen/platform/html/async_browser_test.go`

(Existing CDP runner pattern: see `codegen/platform/html/cdprunner.go` and any `*_browser_test.go`.)

- [ ] **Step 1: Write the test**

Build a SNGL program with a button whose click handler calls an async native that resolves to "hello", and a Text node bound to a state var the handler mutates after the await. Run via the CDP runner; assert the text DOM updates after the promise settles.

```go
//go:build !js

package html_test

import (
    "testing"
)

func TestBrowser_AsyncHandlerUpdatesDOM(t *testing.T) {
    src := `
import { fetchHello } from "js://app/api"

state greeting: string = "before"

window MainWindow {
    button "Go" { @click { greeting = await fetchHello() } }
    text "{greeting}"
}
`
    // mock api.ts with fetchHello returning "after"
    // build, serve, click, wait, assert text == "after"
    runBrowserCase(t, src, browserExpect{
        clickSelector: "button",
        finalText:     "after",
    })
}
```

(`runBrowserCase` modeled on existing CDP test helpers in the repo.)

- [ ] **Step 2: Run + commit**

```bash
go test -run TestBrowser_AsyncHandlerUpdatesDOM ./codegen/platform/html/
git add codegen/platform/html/
git commit -m "test: CDP coverage for async handler settling DOM"
```

---

### Task 13: Final verification

- [ ] **Step 1: Run full suite**

Run: `go tool verify`
Expected: all green.

- [ ] **Step 2: Build**

Run: `go install ./cmd/sngl`
Expected: clean build.

- [ ] **Step 3: WASM build (used by docsgen playground)**

Run: `GOOS=js GOARCH=wasm go build ./internal/playground`
Expected: clean build.

- [ ] **Step 4: Docs site**

Run: `go tool docsgen`
Expected: clean.

- [ ] **Step 5: Push (only if user asks)**

Do not push; surface to user.

---

## Self-review notes

**Spec coverage:**
- §1 async analysis → Tasks 1, 2.
- §2 lowering (named) → Task 8/8b/8c.
- §2 lowering (inline reactive hoist) → Task 9.
- §3 codegen wiring → Tasks 4–7.
- §4 SNGL→SNGL await → Task 3.
- §5 checker rules → Task 10.
- §6 test plan → Tasks 11, 12.
- §7 rollout/risk → Task 13 verifies.

**Open items from spec deferred to implementation:**

- Lambda IR-node confirmation in async walker → Task 1 step 3 (handles `*Lambda` and `*Closure`); Task 2 verifies via test.
- Method-call await wiring → Task 3 step 6.
- Kicker firing at module init vs first read → spec recommendation is module init; implemented via `pkg.StartupKickers` (or equivalent existing hook) in Task 8/8c.

**Type / name consistency:**
- Synthetic prefix `__async_` (for state field) and `__hoist_` (for hoisted computed) — consistent in Tasks 8, 9, 10.
- Kicker name pattern `$compute_<orig>` — consistent in Tasks 8, 8c.
- New cap `NoAsyncReactive` — consistent in Tasks 8, 8c, html `Capabilities`.

**Placeholder scan:**
- `zeroValueExpr` body in Task 8 step 5 is a `panic("implement zeroValueExpr")` stub but Task 8 step 6 implements it explicitly with required test coverage.
- "see Task 8b" / "see Task 11" cross-references — each linked task contains the deferred work.

No "TBD" / "fill in details" left.
