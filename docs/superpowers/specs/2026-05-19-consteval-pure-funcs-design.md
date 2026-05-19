# Generic Constant Folding via Pure-Function Interpreter

Date: 2026-05-19
Status: Draft

## Summary

Replace per-type cases in the constant evaluator with a generic interpreter that runs any pure SNGL function body when its inputs are constant. As prerequisite, normalize `#hex` color literals into `*ir.StructLit` during type checking so downstream code sees a single shape per value type. The combined effect closes the spec gap currently tracked at [issue #76](https://git.duckfam.us/jonathan/sngl/-/issues/76) — `color.lighten(#ff0000, 0.5)`, user-defined pure color helpers, and arbitrary compositions all fold without any color-aware code in the optimizer.

Drive-by: fix the checker bug `cannot initialize color with color` that rejects `const X color = color{r=255,g=255,b=255,a=255}`.

## Goals

- An expression is constant iff it is pure and all of its inputs are constant. (`isConstExpr` already enforces this — keep it.)
- The optimizer evaluates a constant call by interpreting the callee's body, not by consulting a type-specific table.
- Hex and `color{r=,g=,b=,a=}` reach the optimizer as the same IR shape.
- LSP Layer-2 color detection automatically sees folded color values without source-form awareness.
- No special cases for color, measurement, enum, option, or any future named type at the optimizer level.

## Non-goals

- A general-purpose runtime for non-pure code. Pure functions only.
- Folding through closures or first-class function values. Recursion is fine; lambdas captured into pure helpers are out of scope.
- Constant-folding optimizations in compilation phases other than `internal/optimize`.
- Reworking unit literals into structs. Units (`12em`, `1000ms`) stay as `*ir.Literal{Type:unit, Raw, Suffix}`; the interpreter sees them as primitive values.
- Renaming or restructuring `evalQualifiedMethod` — the entries for `int.min`, `list.length`, etc. remain because those funcs are intrinsics with no SNGL body to interpret.

## Architecture

Four pieces:

### Piece A — Checker normalizes hex literals to struct lit

Today the checker emits two distinct IR shapes for color values:

- `#ff8040` → `*ir.Literal{Type:color, Raw:"#ff8040"}`
- `color{r=255, g=128, b=64, a=255}` → `*ir.StructLit{Name:"color", Fields:[r=255, g=128, b=64, a=255]}`

After this change: both lower to the StructLit form. The `*ast.LiteralExpr` for the original hex token is preserved as `StructLit.AST` so source positions and original text survive for diagnostics, hover, and LSP `documentColor`.

The change happens in the checker's literal-to-IR translation for `ast.LiteralColor`. Rough shape:

```go
case ast.LiteralColor:
    r, g, b, a := parseHexChannels(lit.Raw)
    return &ir.StructLit{
        AST:  lit,            // preserve source position + original raw text
        Type: c.resolve("color"),
        Name: "color",
        Fields: []ir.StructFieldLit{
            {Name: "r", Value: intLit(r)},
            {Name: "g", Value: intLit(g)},
            {Name: "b", Value: intLit(b)},
            {Name: "a", Value: intLit(a)},
        },
    }
```

After this lands, `*ir.Literal{Type:color}` no longer appears in IR. Code that pattern-matches on it (LSP color walker, any codegen path that special-cased the hex string) gets updated.

### Piece B — Extract interpreter to `internal/interp`

The testrunner at `codegen/platform/none/testrunner/` already has a full IR interpreter (`eval.go` + `exec.go` + `builtins.go`, ~2700 LOC). Moving it from a codegen-platform subdirectory into a neutral package serves three consumers — the existing test runner, the optimizer (Piece D), and the planned REPL.

Steps:

- Create `internal/interp/`.
- Move `eval.go`, `exec.go`, `builtins.go` (and any helpers exclusive to them) verbatim. Package name becomes `interp`.
- Public surface that survives the move: `Env`, `NewEnv`, `BuildEnv`, `Eval`, `Exec`, `SetFunc`, `SetVar`, plus the error types `AssertError`, `RaisedError`.
- Testrunner-specific bits (`runner.go`, `render.go`, `assert_msg.go`, `testing_t.go`) stay in `codegen/platform/none/testrunner/` and import `internal/interp` for the eval/exec layer.
- Update import paths across the moved files; rename test files alongside their subjects.
- No behavior change; existing testrunner tests must pass unchanged.

Risk: 2700 LOC moves with import churn. Mitigation: do the move as one focused commit before any optimizer-side wiring lands.

### Piece C — Checker bug fix

`const X color = color{r=255, g=255, b=255, a=255}` errors `cannot initialize color with color` due to a type-equality miscompare for named structs. Likely a pointer-equality check where structural equality is needed, or a missing case where `*ir.StructLit{Name:"color"}` produces a synthesized type that doesn't `==` the canonical color type pointer.

Find the site, fix the comparison, add a regression test using the literal form above plus a `var Y color = #ffffff` baseline.

### Piece D — Wire interpreter into optimizer

The const evaluator's `evalCall` currently tries:

1. Hardcoded `evalQualifiedMethod` table (string/int/list/color manual entries)
2. Hardcoded `evalCallFunc` (int/float/string casts)
3. `evalNativeCall` for pure native imports

Add a fourth path that handles the common case:

4. **`Func.Block` interpretation**: if the resolved callee has a SNGL body and `Purity == PurityPure`, build an `interp.Env`, bind args to params, run `Exec` over `Func.Block` until a return is observed.

The optimizer-side helper is small — it adapts the existing interp API to the optimizer's "fail silently, preserve original" contract:

- Build `interp.Env` populated with the same package the optimizer is currently folding.
- Deep-copy composite args (`map[string]any`, `[]any`) before binding to params so callee mutation can't corrupt `ctx.values` (the optimizer's per-call memoization). The interp package itself doesn't need to change — the deep-copy lives in the optimizer-side adapter.
- Bound recursion depth: 256. The interpreter doesn't have a built-in limit (it ran whole test suites to completion); the optimizer adapter tracks depth and short-circuits before the Go stack blows up.
- Any error from `Eval` or `Exec` (assertion failures, raised errors, type mismatches, etc.) → fold fails, original `ir.Call` preserved. The optimizer must never propagate these to the user; they're optimization-time signals, not program errors.

#### Result round-trip

When the interpreter returns a value, the optimizer needs to convert it back to IR. The existing `irLiteral` handles strings, ints, floats, bools, nil. Add a small helper `irFromValue(val any, typ *ir.Type) ir.Expr` that handles `map[string]any` → `*ir.StructLit`, `[]any` → `*ir.ListLit`, primitives → existing `irLiteral`. The struct/list cases recursively call `irFromValue` on their fields/elements.

#### Cleanup once it works

After Piece D is verified for `color.lighten`, `color.darken`, `color.opacity`, `color.rgb`, `color.rgba`, `color.hex`:

- Delete the `color.*` cases from `evalQualifiedMethod`.
- Delete the `case ir.TypeColor` arm in `parseLiteral` (no color literals reach the optimizer post-Piece-A).
- Skip deleting `evalQualifiedMethod` entirely — `int.min`, `list.length`, etc. remain intrinsics with no SNGL bodies.

## Components

| File | Status | Responsibility |
|---|---|---|
| `internal/checker/expr.go` (or wherever color lit is built) | modify | Emit `*ir.StructLit{Name:"color"}` for `ast.LiteralColor` |
| `internal/checker/...` (type-eq site) | modify | Fix `cannot initialize color with color` |
| `internal/interp/` | create (move) | `eval.go`, `exec.go`, `builtins.go` moved from testrunner. Public `Env`, `Eval`, `Exec`, etc. |
| `codegen/platform/none/testrunner/` | modify | Import `internal/interp` for the moved code; keep testrunner-specific orchestration (`runner.go`, `render.go`, `assert_msg.go`, `testing_t.go`) here |
| `internal/optimize/interpret.go` | create | Optimizer-side adapter: depth limit, deep-copy, error→bail, `irFromValue` |
| `internal/optimize/consteval.go` | modify | Plumb adapter into `evalCall`; remove `color.*` from `evalQualifiedMethod`; remove `case ir.TypeColor` from `parseLiteral` |
| `internal/optimize/interpret_test.go` | create | Unit tests per statement kind + E2E fold tests |
| `internal/lsp/color_irwalk.go` | modify | Walk for `*ir.StructLit{Name:"color"}` instead of `*ir.Literal{Type:color}` |
| `internal/lsp/color.go` | modify | `colorFromIRLiteral` / `rangeForIRLiteral` consume StructLit; channel values from field literals |
| Existing tests asserting `*ir.Literal{Type:color}` | adjust | Update to StructLit shape |

## Data flow

`const C = color.lighten(#ff0000, 0.5)` end-to-end:

```
parser → AST
  ConstDecl{Specs:[{Names:["C"], Default:
    CallExpr{Func: SelectExpr(IdentExpr("color"), "lighten"),
             Args: [LiteralExpr{kind:Color, raw:"#ff0000"},
                    LiteralExpr{kind:Float, raw:"0.5"}]}}]}

checker → IR  (Piece A normalizes the hex)
  ir.Var{Name:"C", IsConst:true, Init:
    ir.Call{Func:<ColorLightenFunc(Block)>, Args:[
      ir.StructLit{Name:"color", Fields:[r=255, g=0, b=0, a=255]},
      ir.Literal{Type:float, Raw:"0.5"},
    ]}}

optimizer.foldExpr → folded IR  (Piece B interprets)
  1. isConstExpr(Init) → true (pure func + all const args).
  2. evalCall:
     a. Eval args: c = map{r:255,g:0,b:0,a:255}, pct = 0.5.
     b. call.Func.Block != nil && Func.Purity == Pure → interpretFunc.
  3. interpretFunc:
     a. Fresh env. Deep-copy c. Bind c, pct.
     b. Walk Block:
        - LocalVar ret = color{...c.r,...} → env["ret"] = map{r:255,g:0,b:0,a:255}
        - Assign ret.r = int.min(255, c.r + int(float(255-c.r)*pct))
          → eval RHS via evalExpr (folds binary + the pure intrinsic) → 127
          → walk LHS selector chain; mutate env["ret"]["r"] = 127.
        - Same for g, b.
        - Return ret → result = env["ret"]; halt.
     c. Return map{r:255,g:127,b:127,a:255}.
  4. irFromValue(map, colorType) → *ir.StructLit{Name:"color", Fields:[r=255,g=127,b=127,a=255]}.
  5. foldExpr replaces the original ir.Call with the StructLit.

LSP color walker (post-folding)
  6. Sees *ir.StructLit{Name:"color"} in package var Init.
  7. Reads channel literals from Fields; emits Layer-2 ColorInformation.
```

## Errors & edge cases

- **Recursion depth**: bound at 256. Excess → fold fails silently; original call preserved.
- **Invalid LHS** (e.g. `nil.field = x`): bail; preserve original.
- **NodeInst / Emit / SlotInst in pure func body**: `panic("optimize: <kind> in pure func body — checker bug")`. Per agreement.
- **Native intrinsics in pure body** (e.g. `=> stdlib.ColorHex(c)`): the Return expr evaluates via `evalExpr` → `evalNativeCall` — no special handling needed.
- **Mutation isolation**: deep-copy composite args at bind time to prevent ctx.values cache corruption when the same const is folded twice.
- **Unknown stmt kind**: fold fails, preserve. Don't panic — keeps the optimizer robust to future IR shapes.
- **Non-foldable nested call**: `isConstExpr` already rejects upstream — outer fold doesn't start.
- **`break`/`continue` in For**: if SNGL has them, support; if not, the interpreter doesn't need them.

## Testing

### Checker (Pieces A + C)

- Unit: parsing `#ff8040` produces `*ir.StructLit{Name:"color", Fields:[r=255,g=128,b=64,a=255]}`; not `*ir.Literal{Type:color}`.
- Unit: `#abc` expands to `*ir.StructLit{Fields:[r=170,g=187,b=204,a=255]}`.
- Unit: `#11223344` produces `Fields:[r=17, g=34, b=51, a=68]`.
- Regression: `const X color = color{r=255,g=255,b=255,a=255}` checks clean.
- Existing tests touching color literals — adjusted to the new shape.

### Interpreter (Piece B)

- One table-test per stmt kind in `interpret_test.go`: LocalVar, Return, Assign(ident), Assign(selector), Assign(index), If(true), If(false), For, Toggle, CallStmt-with-pure-call.
- Mutation isolation: fold the same expression twice; assert no cross-talk between calls.
- Recursion: `factorial(5)` user-defined helper folds to 120. `factorial(1000)` bails cleanly (returns `false` ok, fold preserved).
- Result round-trip through `irFromValue`:
  - Primitive map field → ir.Literal.
  - Nested struct → nested StructLit.
  - List of maps → ListLit of StructLits.

### E2E

- `const A = color.lighten(#ff0000, 0.5)` after Optimize → IR Var.Init is `*ir.StructLit{Name:"color", Fields:[r=255,g=127,b=127,a=255]}`.
- `const B = color.opacity(color.lighten(#ff0000, 0.5), 128)` → folded struct with alpha=128.
- `func tint(c color, n float) color => color.lighten(c, n)` + `const C = tint(#ff0000, 0.5)` → folded.
- LSP fixture exercising Layer-2 detection finds the folded swatches.

### Regression

- Existing optimizer tests (`consteval_test.go`, etc.) still pass.
- LSP color fixtures (`testdata/lsp/colors.sngl`) still detect the same swatches.
- The Layer-2 parity test still asserts the expected number of color entries.
- All `internal/lsp` fixture type-check tests still pass.

## Rollout

Four pieces, one coherent change. Recommended implementation order:

1. **Piece B** (extract interpreter to `internal/interp`). Pure refactor, zero behavior change. Testrunner tests must remain green. Sets up the import path for Piece D.
2. **Piece C** (checker bug fix). Small, independent. Lets later tests use `color{...}` literal form.
3. **Piece A** (hex → StructLit). Update existing tests that asserted the literal shape.
4. **LSP walker update** (`color_irwalk.go`, `color.go`). Matches the new shape so color fixtures don't regress.
5. **Piece D** (wire interpreter into optimizer). Tests for: end-to-end fold of composed expressions, recursion depth, mutation isolation, result round-trip.
6. **Cleanup**: remove `color.*` from `evalQualifiedMethod` and `parseLiteral`'s `TypeColor` arm.

Each step ships with tests; intermediate states are still green.

## Open items

- Verify SNGL has no `break`/`continue` before deciding whether to support them in `For` interpretation.
- Confirm `Assign` LHS in IR is one of `*ir.Ident`, `*ir.Select`, `*ir.Index`. If there's a fourth, document it in the plan.
- Decide what `irFromValue` does with a `map[string]any` when the declared return type isn't a named struct — likely impossible by purity rules, but a defensive default (bail) is safer than asserting.
