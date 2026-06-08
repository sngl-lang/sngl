# Struct Spread Arguments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow `...structExpr` in a function-call or component-prop argument list to spread the fields of a statically-typed struct as named arguments/props to the callee.

**Architecture:** Reuses the existing `...` (ELLIPSIS) token and `ast.SpreadExpr` — no new syntax. Three checker sites are updated: `bindArgs` (regular function calls), `checkAndSplitArgs` (visual-node component instantiation), and `checkComponentCallArgs` (component calls from expressions). In each, a detected `SpreadExpr` is type-checked; if the operand is a struct, its fields are expanded into named arg/prop slots via `ir.Select`. Non-struct spread errors. Duplicate param/prop errors. Spread is fully resolved at check time — IR, codegen, and the interpreter need no changes.

**Tech Stack:** Go; SNGL type checker (`internal/checker/expr.go`).

---

## File Structure

| File                                         | Change                                                                 |
|----------------------------------------------|------------------------------------------------------------------------|
| `internal/checker/expr.go`                   | `bindArgs`, `checkAndSplitArgs`, `checkComponentCallArgs`              |
| `testdata/test_spread_struct_args.sngl`      | Success fixture (already created)                                      |
| `testdata/error_spread_arg_wrong_type.sngl`  | Error: spread non-struct into function arg (already created)           |
| `testdata/error_spread_arg_duplicate.sngl`   | Error: spread conflicts with explicit function arg (already created)   |
| `testdata/error_spread_prop_wrong_type.sngl` | Error: spread non-struct into component prop (already created)         |
| `testdata/error_spread_prop_duplicate.sngl`  | Error: spread conflicts with explicit component prop (already created) |

---

### Task 1: Struct Spread in Function Calls (`bindArgs`)

**Files:**
- Modify: `internal/checker/expr.go` — `bindArgs` function (line ~1807)

The success fixture (`test_spread_struct_args.sngl`) tests:
- `greet(...cfg)` — full struct spread where field order differs from param order
- `addInts(...p)` — extra struct field (`z`) silently ignored
- `cm.greetLocal(...cfg)` — spread in a component-method call from a test

The error fixtures (`error_spread_arg_wrong_type.sngl`, `error_spread_arg_duplicate.sngl`) test the checker errors.

- [ ] **Step 1: Run fixtures to confirm they currently fail**

```bash
go test -run "TestRunFixtures/test_spread_struct_args" ./codegen/platform/none/testrunner/... 2>&1
go test -run "TestCheckTestdata/error_spread_arg" ./internal/checker/... 2>&1
```

Expected: FAIL — `SpreadExpr` in arg position falls through unhandled.

- [ ] **Step 2: Implement struct spread in `bindArgs`**

In `internal/checker/expr.go`, in the `bindArgs` function, insert a spread handler before the positional branch. The complete insertion, placed immediately after the `if arg.Value == nil { continue }` check:

```go
// Struct spread: ...expr expands struct fields as named args.
if spread, isSpr := arg.Value.(*ast.SpreadExpr); isSpr {
	operandIR := c.checkExpr(spread.Operand)
	operandType := exprType(operandIR)
	if operandType == nil || operandType.Kind != ir.TypeStruct {
		typStr := "(nil)"
		if operandType != nil {
			typStr = operandType.String()
		}
		c.error(spread.Pos, "spread requires a struct type, got %s", typStr)
		ok = false
		continue
	}
	sd := operandType.Decl.(*ir.StructDef)
	seenNamed = true
	for _, f := range sd.Fields {
		idx := -1
		for i, p := range sig.Params {
			if p.Name == f.Name {
				idx = i
				break
			}
		}
		if idx == -1 {
			continue // no matching param; ignore silently
		}
		if bound[idx] != nil {
			c.error(spread.Pos, "parameter %q already provided", f.Name)
			ok = false
			continue
		}
		var selExpr ir.Expr = &ir.Select{Type: f.Type, Operand: operandIR, Field: f.Name}
		p := sig.Params[idx]
		if p.Type != nil && f.Type.Kind != ir.TypeDyn && p.Type.Kind != ir.TypeDyn &&
			!f.Type.IsAssignableTo(p.Type) {
			c.error(spread.Pos, "cannot use field %q (%s) as parameter %q (%s)",
				f.Name, f.Type, p.Name, p.Type)
			ok = false
			continue
		}
		if p.Type != nil {
			selExpr = wrapIfNeeded(selExpr, p.Type)
		}
		bound[idx] = &selExpr
	}
	continue
}
```

`exprType`, `wrapIfNeeded` are package-level helpers used throughout `expr.go`. `*ir.Select` implements `ir.Expr`; `AST` field is nil for synthetic projections.

- [ ] **Step 3: Guard spread in the `sig==nil` dynamic-call path**

In `checkCallArgs`, the `sig == nil` branch (line ~1923) doesn't call `bindArgs`. Add a guard before `checkExpr` so spread in a dynamic call fails clearly:

```go
if arg.Value != nil {
    if spread, isSpr := arg.Value.(*ast.SpreadExpr); isSpr {
        c.error(spread.Pos, "... struct spread cannot be used in a dynamic function call")
        continue
    }
    expr := c.checkExpr(arg.Value)
```

- [ ] **Step 4: Build and run function-call fixtures**

```bash
go build ./... 2>&1
go test -run "TestRunFixtures/test_spread_struct_args" ./codegen/platform/none/testrunner/... 2>&1
go test -run "TestCheckTestdata/error_spread_arg" ./internal/checker/... 2>&1
```

Expected: all pass. (The `testStructSpreadComponentProps` test will still fail — that's Task 2.)

- [ ] **Step 5: Commit**

```bash
git add internal/checker/expr.go
git commit -m "feat(checker): expand ...struct in function call arg lists into named params"
```

---

### Task 2: Struct Spread in Component Props (`checkAndSplitArgs` + `checkComponentCallArgs`)

**Files:**
- Modify: `internal/checker/expr.go` — `checkAndSplitArgs` (line ~2910) and `checkComponentCallArgs` (line ~3060)

The success fixture tests `Item #it(...opts)` in a component body, then reads `item.label` and `item.count` from the test. The error fixtures test wrong type and duplicate prop.

- [ ] **Step 1: Run component-prop fixtures to confirm they currently fail**

```bash
go test -run "TestRunFixtures/test_spread_struct_args" ./codegen/platform/none/testrunner/... 2>&1
go test -run "TestCheckTestdata/error_spread_prop" ./internal/checker/... 2>&1
```

Expected: FAIL.

- [ ] **Step 2: Add spread handling to `checkAndSplitArgs`**

`checkAndSplitArgs` handles visual-node component instantiation (`Item(...opts)` in a component body). Find the `for _, a := range args.Args` loop and the `case ast.Arg:` branch. Currently it checks `arg.Name == ""` (positional) and `arg.Name != ""` (named). Add a spread case before the positional check — insert after `if arg.Name == "key" { continue }`:

```go
// Struct spread: ...expr expands struct fields as named props.
if arg.Value != nil {
	if spread, isSpr := arg.Value.(*ast.SpreadExpr); isSpr {
		operandIR := c.checkExpr(spread.Operand)
		operandType := exprType(operandIR)
		if operandType == nil || operandType.Kind != ir.TypeStruct {
			typStr := "(nil)"
			if operandType != nil {
				typStr = operandType.String()
			}
			c.error(spread.Pos, "spread requires a struct type, got %s", typStr)
			continue
		}
		sd := operandType.Decl.(*ir.StructDef)
		seenNamed = true
		for _, f := range sd.Fields {
			if comp == nil {
				continue // no component context; can't match prop names
			}
			propType := componentPropType(comp, f.Name)
			if propType == nil {
				continue // no matching prop; ignore silently
			}
			if boundProps[f.Name] {
				c.error(spread.Pos, "prop %q already provided on component %s", f.Name, comp.Name)
				continue
			}
			var selExpr ir.Expr = &ir.Select{Type: f.Type, Operand: operandIR, Field: f.Name}
			if f.Type.Kind != ir.TypeDyn && propType.Kind != ir.TypeDyn {
				selExpr = wrapIfNeeded(selExpr, propType)
			}
			boundProps[f.Name] = true
			props = append(props, ir.Arg{Name: f.Name, Value: selExpr})
		}
		continue
	}
}
```

`componentPropType` is already used in this function (line ~2973). `boundProps` is the existing duplicate-tracking map.

- [ ] **Step 3: Add spread handling to `checkComponentCallArgs`**

`checkComponentCallArgs` handles component calls from expression context (e.g., in test functions). Same pattern — find the `for _, a := range call.Args.Args` loop, `case ast.Arg:` branch, insert after `if arg.Name == "key" { continue }`:

```go
// Struct spread: ...expr expands struct fields as named props.
if arg.Value != nil {
	if spread, isSpr := arg.Value.(*ast.SpreadExpr); isSpr {
		operandIR := c.checkExpr(spread.Operand)
		operandType := exprType(operandIR)
		if operandType == nil || operandType.Kind != ir.TypeStruct {
			typStr := "(nil)"
			if operandType != nil {
				typStr = operandType.String()
			}
			c.error(spread.Pos, "spread requires a struct type, got %s", typStr)
			continue
		}
		sd := operandType.Decl.(*ir.StructDef)
		for _, f := range sd.Fields {
			if !componentHasProp(comp, f.Name) {
				continue // no matching prop; ignore silently
			}
			if boundProps[f.Name] {
				c.error(spread.Pos, "prop %q already provided on component %s", f.Name, comp.Name)
				continue
			}
			propType := componentPropType(comp, f.Name)
			var selExpr ir.Expr = &ir.Select{Type: f.Type, Operand: operandIR, Field: f.Name}
			if propType != nil && f.Type.Kind != ir.TypeDyn && propType.Kind != ir.TypeDyn {
				selExpr = wrapIfNeeded(selExpr, propType)
			}
			result = append(result, ir.CallArg{Name: f.Name, Value: selExpr})
			boundProps[f.Name] = true
		}
		continue
	}
}
```

`componentHasProp` and `componentPropType` are already used in this function.

- [ ] **Step 4: Build and run all spread fixtures**

```bash
go build ./... 2>&1
go test -run "TestRunFixtures/test_spread_struct_args" ./codegen/platform/none/testrunner/... 2>&1
go test -run "TestCheckTestdata/error_spread_prop" ./internal/checker/... 2>&1
```

Expected: all pass.

- [ ] **Step 5: Run full suite**

```bash
go tool verify 2>&1 | tail -20
```

Expected: all packages pass.

- [ ] **Step 6: Commit**

```bash
git add internal/checker/expr.go testdata/test_spread_struct_args.sngl testdata/error_spread_arg_wrong_type.sngl testdata/error_spread_arg_duplicate.sngl testdata/error_spread_prop_wrong_type.sngl testdata/error_spread_prop_duplicate.sngl
git commit -m "feat(checker): expand ...struct in component prop lists into named props"
```

---

## Self-Review

**Spec coverage:**
- `...expr` on struct → named function args ✓ Task 1, `test_spread_struct_args.sngl`
- `...expr` on struct → named component props ✓ Task 2, `test_spread_struct_args.sngl`
- Struct fields not matching any param/prop → silently ignored ✓ both tasks
- Non-struct operand → `"spread requires a struct type, got %s"` ✓ `error_spread_arg_wrong_type.sngl`, `error_spread_prop_wrong_type.sngl`
- Field conflicts with explicit arg → `"parameter/prop %q already provided"` ✓ `error_spread_arg_duplicate.sngl`, `error_spread_prop_duplicate.sngl`
- Spread in dynamic function call → error ✓ Task 1 Step 3
- No new tokens, grammar, AST, IR, codegen, or interpreter changes ✓

**Placeholder scan:** Clean.

**Type consistency:** `ir.Select`, `exprType`, `wrapIfNeeded`, `componentPropType`, `componentHasProp` — all existing helpers used consistently.
