# Unified Named/Positional Arguments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow named and positional arguments interchangeably at every call site — function calls, component props, window, timer — with Pythonic ordering rules and `_`-prefix positional-only params.

**Architecture:** Four-layer change: (1) AST and grammar gain named params in `func(x T, y T)` type expressions; (2) `checkCallArgs` and `checkAndSplitArgs` in the checker are replaced with a shared `bindArgs` algorithm that resolves names to param-declaration-order slots; (3) `buildWindow`/`buildTimer` get positional-arg support via the same algorithm; (4) the Go scheme importer exposes real param names (drops the `argN` synthetic fallback).

**Tech Stack:** Go, `modernc.org/egg` PEG/LL parser generator, testdata `.sngl` fixture files.

---

## File Map

| File                                | Change                                                                                                             |
|-------------------------------------|--------------------------------------------------------------------------------------------------------------------|
| `ast/expr.go`                       | Add `FuncTypeParam` struct; change `FuncType.Params []TypeExpr` → `[]FuncTypeParam`                                |
| `internal/parser/sngl.ebnf`         | Add `FuncTypeParamList`, `FuncTypeParam`, `FuncTypeParamTail` productions; update func-type rule                   |
| `internal/parser/zparser.go`        | Regenerated — do not edit manually                                                                                 |
| `internal/parser/build.go`          | Add `buildFuncTypeParamList`, `buildFuncTypeParam` builder functions                                               |
| `internal/parser/format.go`         | Update `writeType` for `*ast.FuncType` to emit names when present                                                  |
| `internal/parser/format_test.go`    | Add round-trip tests for named func-type params                                                                    |
| `internal/checker/resolve.go`       | Update `resolveFuncType` to populate `ir.Param.Name` from `FuncTypeParam.Name`                                     |
| `internal/checker/expr.go`          | Add `bindArgs`; replace `checkCallArgs` and `checkAndSplitArgs` with unified bind; update `checkComponentCallArgs` |
| `internal/checker/checker.go`       | Update `buildWindow` and `buildTimer` to handle positional args                                                    |
| `codegen/scheme/golang/importer.go` | Remove `argN` synthetic name fallback; leave `Name = ""` for anonymous Go params                                   |

---

## Task 1: AST — `FuncTypeParam` struct + update `FuncType`

**Files:**
- Modify: `ast/expr.go:119-124`

- [ ] **Step 1: Add `FuncTypeParam` struct and update `FuncType.Params`**

```go
// FuncTypeParam is one parameter in a func-type expression.
// Name is empty when the parameter has no declared name (positional-only when called).
type FuncTypeParam struct {
	Name string
	Type TypeExpr
}

// FuncType is a function type: func(int, string) -> bool.
type FuncType struct {
	Pos    Pos
	Params []FuncTypeParam // parameter types (Name="" means anonymous/positional-only)
	Return TypeExpr        // nil for void
}
```

- [ ] **Step 2: Fix all call sites that read `FuncType.Params` as `[]TypeExpr`**

Run: `go build ./...`

The build will fail listing every site that iterates `FuncType.Params`. For each:
- `internal/checker/resolve.go` `resolveFuncType`: change `p` from `TypeExpr` to `FuncTypeParam`, use `p.Type`
- `internal/parser/build.go` `buildTypeList` → replaced in Task 3
- `internal/parser/format.go` `writeType` → updated in Task 4

Fix each one minimally (just use `.Type`) so the build passes; Task 4 adds name emission.

- [ ] **Step 3: Verify build**

```
go build ./...
```

Expected: compiles cleanly.

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(ast): add FuncTypeParam; FuncType.Params carries optional names"
```

---

## Task 2: Grammar — named params in func-type expressions

**Files:**
- Modify: `internal/parser/sngl.ebnf:598,604`

The current `Type` rule for func types is:

```
| kw_func lparen [ TypeList ] rparen [ Type ]
```

where `TypeList = Type { comma Type }`. This cannot parse `func(x int)` — `x int` is not a comma-separated list of types.

The new grammar replaces `TypeList` with `FuncTypeParamList` in the func-type rule and adds three new productions. The key disambiguation: after consuming the leading `ident` of a parameter, a `FuncTypeParamTail` inspects the next token:

- `dot` → the ident is part of a qualified type `pkg.Foo`
- `lt` → the ident is the base of a generic type `list<int>`
- starts a `Type` (another `ident`, `kw_func`, etc.) → the ident is a **param name**; the tail is the type
- `,` or `)` → the ident is a standalone anonymous type name

- [ ] **Step 1: Add the new grammar productions**

In `sngl.ebnf`, replace line `| kw_func lparen [ TypeList ] rparen [ Type ]` with `| kw_func lparen [ FuncTypeParamList ] rparen [ Type ]` and add the three new rules after `TypeList = Type { comma Type } .`:

```ebnf
FuncTypeParamList = FuncTypeParam { comma FuncTypeParam } .

FuncTypeParam = ident FuncTypeParamTail | kw_func lparen [ FuncTypeParamList ] rparen [ Type ] | StructDecl | EnumDecl | UnitDecl .

FuncTypeParamTail = dot ident [ lt TypeList gt ] | lt TypeList gt | Type | .
```

Verify the FIRST/FOLLOW sets mentally:
- `FuncTypeParam` branches: `ident`, `kw_func`, `kw_struct`, `kw_enum`, `kw_unit` — all distinct.
- `FuncTypeParamTail` branches: `dot`, `lt`, FIRST(Type)={ident,kw_func,kw_struct,kw_enum,kw_unit}, epsilon. The epsilon branch is taken on `comma` or `)`. None overlap.

- [ ] **Step 2: Regenerate `zparser.go`**

```
go generate ./internal/parser/
```

Expected: `zparser.go` regenerated, no errors from `egg`.

- [ ] **Step 3: Verify the package still compiles (build.go still uses old API)**

```
go build ./internal/parser/
```

Expected: may have compile errors in `build.go` if `buildTypeList` is called in the func-type build path — fix by wiring up the new builder in Task 3.

---

## Task 3: Parser builder — `buildFuncTypeParamList`

**Files:**
- Modify: `internal/parser/build.go` (near `buildTypeList` at ~line 2528)

The grammar now produces `FuncTypeParamList` / `FuncTypeParam` / `FuncTypeParamTail` nodes in the parse tree. The builder needs functions to turn those into `[]ast.FuncTypeParam`.

- [ ] **Step 1: Add `buildFuncTypeParamList` and `buildFuncTypeParam`**

Add after `buildTypeList`:

```go
// buildFuncTypeParamList builds []ast.FuncTypeParam from a FuncTypeParamList node.
func (b *builder) buildFuncTypeParamList(it nodeIter) []ast.FuncTypeParam {
	var params []ast.FuncTypeParam
	for !it.done() {
		if it.isNonTerminal() && it.symbol() == FuncTypeParam {
			params = append(params, b.buildFuncTypeParam(it.enter()))
		} else {
			it.skip() // comma
		}
	}
	return params
}

// buildFuncTypeParam builds one ast.FuncTypeParam from a FuncTypeParam node.
//
// Grammar: FuncTypeParam = ident FuncTypeParamTail | kw_func ... | StructDecl | EnumDecl | UnitDecl .
//
// When the leading token is ident, the tail determines whether it is a param name
// (tail = Type) or part of an anonymous type (tail = dot/lt/epsilon).
func (b *builder) buildFuncTypeParam(it nodeIter) ast.FuncTypeParam {
	if it.done() {
		return ast.FuncTypeParam{}
	}
	// Non-ident leading tokens: anonymous compound type (kw_func, struct, enum, unit).
	if it.isNonTerminal() {
		return ast.FuncTypeParam{Type: b.buildType(it)} // re-enter into a Type-shaped subtree
	}
	switch it.tokenType() {
	case KW_FUNC, KW_STRUCT, KW_ENUM, KW_UNIT:
		return ast.FuncTypeParam{Type: b.buildType(it)}
	}
	// Leading ident — consume it, then inspect the tail.
	identTok := it.shift() // ident
	name := b.textFromToken(identTok)

	if it.done() {
		// No tail: the ident is an anonymous simple type name.
		return ast.FuncTypeParam{Type: &ast.NamedType{Pos: b.posFromToken(identTok), Name: name}}
	}
	if !it.isNonTerminal() {
		it.skip() // unexpected token, skip
		return ast.FuncTypeParam{Type: &ast.NamedType{Pos: b.posFromToken(identTok), Name: name}}
	}
	// FuncTypeParamTail node.
	tail := it.enter()
	if tail.done() {
		// epsilon: ident is anonymous type name.
		return ast.FuncTypeParam{Type: &ast.NamedType{Pos: b.posFromToken(identTok), Name: name}}
	}
	switch tail.tokenType() {
	case DOT:
		// dot ident [lt TypeList gt]: anonymous qualified type.
		tail.skip() // dot
		qualName := ""
		if !tail.done() && !tail.isNonTerminal() {
			qualTok := tail.shift()
			qualName = b.textFromToken(qualTok)
		}
		nt := &ast.NamedType{Pos: b.posFromToken(identTok), Package: name, Name: qualName}
		// Optional [lt TypeList gt].
		if !tail.done() && !tail.isNonTerminal() && tail.tokenType() == LT {
			tail.skip() // lt
			if !tail.done() && tail.isNonTerminal() && tail.symbol() == TypeList {
				nt.TypeArgs = b.buildTypeList(tail.enter())
			}
			if !tail.done() && !tail.isNonTerminal() && tail.tokenType() == GT {
				tail.skip() // gt
			}
		}
		return ast.FuncTypeParam{Type: nt}
	case LT:
		// lt TypeList gt: anonymous generic type (ident is the base name).
		nt := &ast.NamedType{Pos: b.posFromToken(identTok), Name: name}
		tail.skip() // lt
		if !tail.done() && tail.isNonTerminal() && tail.symbol() == TypeList {
			nt.TypeArgs = b.buildTypeList(tail.enter())
		}
		if !tail.done() && !tail.isNonTerminal() && tail.tokenType() == GT {
			tail.skip() // gt
		}
		return ast.FuncTypeParam{Type: nt}
	default:
		// Tail is a Type node: the leading ident is the param name.
		if !tail.isNonTerminal() {
			break
		}
		typ := b.buildType(tail)
		return ast.FuncTypeParam{Name: name, Type: typ}
	}
	return ast.FuncTypeParam{Type: &ast.NamedType{Pos: b.posFromToken(identTok), Name: name}}
}
```

- [ ] **Step 2: Wire `buildFuncTypeParamList` into the func-type branch of `buildType`**

In `buildType` near line 2509, replace:

```go
if !it.done() && it.isNonTerminal() && it.symbol() == TypeList {
	ft.Params = b.buildTypeList(it.enter())
}
```

with:

```go
if !it.done() && it.isNonTerminal() && it.symbol() == FuncTypeParamList {
	ft.Params = b.buildFuncTypeParamList(it.enter())
}
```

Note: the `FuncType.Params` field is now `[]ast.FuncTypeParam` (from Task 1), so this compiles.

- [ ] **Step 3: Build and run parser tests**

```
go test ./internal/parser/...
```

Expected: all existing tests pass (existing `func(int, string)` parses as `[]FuncTypeParam{{Type: int}, {Type: string}}`).

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(parser): parse named params in func-type expressions"
```

---

## Task 4: Formatter — emit named func-type params

**Files:**
- Modify: `internal/parser/format.go:1181-1193`
- Modify: `internal/parser/format_test.go`

- [ ] **Step 1: Update `writeType` for `*ast.FuncType`**

Replace the current loop that calls `f.writeType(p)` (where `p` was `TypeExpr`) with:

```go
case *ast.FuncType:
    f.write("func(")
    for i, p := range t.Params {
        if i > 0 {
            f.write(", ")
        }
        if p.Name != "" {
            f.write(p.Name)
            f.write(" ")
        }
        f.writeType(p.Type)
    }
    f.write(")")
    if t.Return != nil {
        f.write(" ")
        f.writeType(t.Return)
    }
```

- [ ] **Step 2: Add format round-trip tests**

In `format_test.go`, add after `TestFormatFuncType`:

```go
func TestFormatFuncTypeNamedParams(t *testing.T) {
	assertFormat(t,
		`var f func(x int, y int) bool`,
		`var f func(x int, y int) bool`)
}

func TestFormatFuncTypeMixedParams(t *testing.T) {
	assertFormat(t,
		`var f func(_ int, y int) bool`,
		`var f func(_ int, y int) bool`)
}

func TestFormatFuncTypeAnonymousGeneric(t *testing.T) {
	assertFormat(t,
		`var f func(list<int>) bool`,
		`var f func(list<int>) bool`)
}

func TestFormatFuncTypeNamedGeneric(t *testing.T) {
	assertFormat(t,
		`var f func(items list<int>) bool`,
		`var f func(items list<int>) bool`)
}

func TestFormatFuncTypeNamedQualified(t *testing.T) {
	assertFormat(t,
		`var f func(x int, cb func(string) bool) int`,
		`var f func(x int, cb func(string) bool) int`)
}
```

- [ ] **Step 3: Run tests**

```
go test ./internal/parser/... -run TestFormatFuncType
```

Expected: all new and existing func-type formatter tests pass.

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(parser): format named params in func-type expressions"
```

---

## Task 5: Checker — `resolveFuncType` carries param names

**Files:**
- Modify: `internal/checker/resolve.go:218-234`

- [ ] **Step 1: Update `resolveFuncType`**

```go
func (c *checker) resolveFuncType(t *ast.FuncType) *ir.Type {
	params := make([]*ir.Param, len(t.Params))
	for i, p := range t.Params {
		params[i] = &ir.Param{
			Name: p.Name, // "" for anonymous params — positional-only at call sites
			Type: c.resolveTypeRequired(p.Type, ast.Pos{}, "function-type parameter"),
		}
	}
	var ret *ir.Type
	if t.Return != nil {
		ret = c.resolveType(t.Return)
	}
	return &ir.Type{
		Kind: ir.TypeFunc,
		Sig: &ir.FuncSig{
			Params: params,
			Return: ret,
		},
	}
}
```

- [ ] **Step 2: Verify checker_test and testdata**

```
go test ./internal/checker/...
```

Expected: all existing tests pass.

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(checker): resolveFuncType preserves param names in ir.FuncSig"
```

---

## Task 6: Go scheme importer — expose real param names

**Files:**
- Modify: `codegen/scheme/golang/importer.go:224-232`

Currently anonymous Go params (`_`) fall back to synthetic `"arg0"`, `"arg1"`, etc. These synthetic names are never declared in Go source and should not be callable by name. The fix: leave `Name = ""`, which the checker will treat as positional-only.

- [ ] **Step 1: Remove the `argN` fallback**

```go
// Before:
name := v.Name()
if name == "" {
	name = fmt.Sprintf("arg%d", i)
}

// After:
name := v.Name()
// Empty name means anonymous Go param — positional-only at SNGL call sites.
```

- [ ] **Step 2: Run importer tests**

```
go test ./codegen/scheme/golang/...
```

Expected: all pass.

- [ ] **Step 3: Commit**

```bash
git commit -m "fix(go-importer): anonymous Go params stay unnamed (positional-only)"
```

---

## Task 7: Checker — `bindArgs` unified algorithm

**Files:**
- Modify: `internal/checker/expr.go` (add new helper, update `checkCallArgs`)

This is the core of the feature. The `bindArgs` function implements the full Pythonic binding algorithm against a `[]*ir.Param` signature and returns args in **param-declaration order**.

- [ ] **Step 1: Add `bindArgs` helper function**

Add before `checkCallArgs` (near line 1800):

```go
// paramNameOK reports whether a param can be targeted by name at a call site.
// Params with no name or names starting with "_" must be passed positionally.
func paramNameOK(p *ir.Param) bool {
	return p.Name != "" && !strings.HasPrefix(p.Name, "_")
}

// bindArgs resolves a list of AST args against a signature, returns one
// resolved *ir.Expr per param slot (nil = use default). Errors are reported
// via c.error. args is the raw call-site ArgList. sig is the target signature.
// Returns (exprs, ok); exprs[i] corresponds to sig.Params[i].
func (c *checker) bindArgs(callPos ast.Pos, args []ast.ArgOrEventHandler, sig *ir.FuncSig) ([]*ir.Expr, bool) {
	n := len(sig.Params)
	bound := make([]*ir.Expr, n)
	positional := 0
	seenNamed := false
	ok := true

	for _, a := range args {
		arg, isArg := a.(ast.Arg)
		if !isArg {
			continue // event handlers handled by caller
		}
		if arg.Value == nil {
			continue
		}

		if arg.Name == "" {
			// Positional arg.
			if seenNamed {
				c.error(*arg.Value.ExprPos(), "positional argument after named argument")
				ok = false
				continue
			}
			if positional >= n {
				c.error(*arg.Value.ExprPos(), "too many arguments: expected %d", n)
				ok = false
				positional++
				continue
			}
			expr := c.checkArgExpr(arg.Value, sig.Params[positional])
			bound[positional] = &expr
			positional++
		} else {
			// Named arg.
			seenNamed = true
			idx := -1
			for i, p := range sig.Params {
				if p.Name == arg.Name {
					idx = i
					break
				}
			}
			if idx == -1 {
				c.error(arg.NamePos, "unknown parameter %q", arg.Name)
				ok = false
				continue
			}
			p := sig.Params[idx]
			if strings.HasPrefix(p.Name, "_") {
				c.error(arg.NamePos, "parameter %q must be passed positionally", p.Name)
				ok = false
				continue
			}
			if bound[idx] != nil {
				c.error(arg.NamePos, "parameter %q already provided", p.Name)
				ok = false
				continue
			}
			expr := c.checkArgExpr(arg.Value, p)
			bound[idx] = &expr
		}
	}

	// Arity: every required param must be filled.
	for i, p := range sig.Params {
		if bound[i] == nil && p.Default == nil {
			c.error(callPos, "missing required argument %q", p.Name)
			ok = false
		}
	}

	return bound, ok
}

// checkArgExpr type-checks a single call argument against a target param.
// Applies implicit-call and literal-zero adaptations as needed.
func (c *checker) checkArgExpr(value ast.Expr, p *ir.Param) ir.Expr {
	expr := c.checkExprExpecting(value, p.Type)
	actual := exprType(expr)
	c.requireValueType(actual, *value.ExprPos())
	if p.Type != nil && actual.Kind != ir.TypeDyn && p.Type.Kind != ir.TypeDyn && !actual.IsAssignableTo(p.Type) {
		if adapted, ok := adaptLiteralZero(expr, p.Type); ok {
			expr = adapted
		} else if callExpr, _ := c.implicitCall(value, actual, p.Type); callExpr != nil {
			expr = c.checkExpr(callExpr)
		}
	}
	if p.Type != nil {
		expr = wrapIfNeeded(expr, p.Type)
	}
	return expr
}
```

- [ ] **Step 2: Replace `checkCallArgs` with the unified bind**

Replace the body of `checkCallArgs` (lines 1802–1872) with:

```go
func (c *checker) checkCallArgs(args ast.ArgList, sig *ir.FuncSig) []ir.CallArg {
	// Order check: no positional after named.
	seenNamed := false
	for _, a := range args.Args {
		if arg, ok := a.(ast.Arg); ok {
			if arg.Name != "" {
				seenNamed = true
			} else if seenNamed && arg.Value != nil {
				c.error(*arg.Value.ExprPos(), "positional argument after named argument")
				return nil
			}
		}
	}

	// No sig: check exprs, pass through names unchanged (dynamic call).
	if sig == nil {
		var result []ir.CallArg
		for _, a := range args.Args {
			if arg, ok := a.(ast.Arg); ok && arg.Value != nil {
				expr := c.checkExpr(arg.Value)
				c.requireValueType(exprType(expr), *arg.Value.ExprPos())
				result = append(result, ir.CallArg{Name: arg.Name, NamePos: arg.NamePos, Value: expr})
			}
		}
		return result
	}

	bound, _ := c.bindArgs(args.Pos, args.Args, sig)

	// Build result in param order; omit trailing nils (use defaults).
	var result []ir.CallArg
	for i, expr := range bound {
		if expr == nil {
			continue // default param — omit from call args
		}
		result = append(result, ir.CallArg{
			Name:    sig.Params[i].Name,
			NamePos: sig.Params[i].AST.Pos, // best-effort source pos
			Value:   *expr,
		})
	}
	return result
}
```

Note: `ir.Param` may not have an `.AST` field — use `ast.Pos{}` as the NamePos if unavailable; callers that need it will already have original source positions. Check the `ir.Param` struct and adjust accordingly.

- [ ] **Step 3: Run checker tests**

```
go test ./internal/checker/...
```

Expected: all existing tests pass. New testdata error fixtures still fail (they expect errors; those are verified by the test runner picking up the `// ERROR(check)` directives).

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(checker): bindArgs — unified Pythonic named/positional bind algorithm"
```

---

## Task 8: Checker — `checkAndSplitArgs` positional prop support

**Files:**
- Modify: `internal/checker/expr.go:2797-2867`

Component props are stored as `ir.Prop` (with `Name`, `Type`, `Default`). The positional slot order is the `comp.Props` slice order. Event handlers remain name-only; they are not counted for positional index.

- [ ] **Step 1: Replace `checkAndSplitArgs` body**

The key changes:
1. Order check (positional after named → error).
2. Positional args fill `comp.Props[positional]` by index (skipping `EventDecl` entries).
3. Named args work as before.
4. Arity check: all required (no-default) props must be filled.

```go
func (c *checker) checkAndSplitArgs(args ast.ArgList, comp *ir.Component) ([]ir.Arg, []ir.EventHandler) {
	var props []ir.Arg
	var handlers []ir.EventHandler
	seenNames := make(map[string]ast.Pos) // for duplicate-handler detection

	// Build an ordered list of props for positional binding.
	var orderedProps []*ir.Prop
	if comp != nil {
		orderedProps = comp.Props
	}
	boundPropIdx := make(map[string]bool) // track which props were bound

	// Order check.
	seenNamed := false
	for _, a := range args.Args {
		if arg, ok := a.(ast.Arg); ok {
			if arg.Name != "" && !strings.HasPrefix(arg.Name, ":") {
				seenNamed = true
			} else if arg.Name == "" && seenNamed && arg.Value != nil {
				c.error(*arg.Value.ExprPos(), "positional argument after named argument")
				return nil, nil
			}
		}
	}

	positional := 0
	for _, a := range args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Name == "key" {
				continue // handled separately
			}
			var resolvedName string
			var expected *ir.Type

			if arg.Name == "" {
				// Positional: find the next prop slot.
				if positional >= len(orderedProps) {
					if arg.Value != nil {
						c.error(*arg.Value.ExprPos(), "too many positional arguments")
					}
					positional++
					continue
				}
				p := orderedProps[positional]
				resolvedName = p.Name
				expected = p.Type
				positional++
			} else {
				// Named.
				resolvedName = arg.Name
				propName := resolvedName
				if strings.HasPrefix(propName, ":") {
					propName = propName[1:]
				}
				if comp != nil {
					expected = componentPropType(comp, propName)
				}
			}

			var val ir.Expr
			if arg.Value != nil {
				val = c.checkExprExpecting(arg.Value, expected)
				c.requireValueType(exprType(val), *arg.Value.ExprPos())
				if expected != nil {
					actual := exprType(val)
					if actual.Kind != ir.TypeDyn && expected.Kind != ir.TypeDyn && !actual.IsAssignableTo(expected) {
						if adapted, ok := adaptLiteralZero(val, expected); ok {
							val = adapted
						} else if callExpr, _ := c.implicitCall(arg.Value, actual, expected); callExpr != nil {
							val = c.checkExpr(callExpr)
						}
					}
					if expected.Kind != ir.TypeDyn {
						val = wrapIfNeeded(val, expected)
					}
				}
			}
			if comp != nil && resolvedName != "" {
				propName := resolvedName
				if strings.HasPrefix(propName, ":") {
					propName = propName[1:]
				}
				if !componentHasProp(comp, propName) && !componentHasEvent(comp, propName) {
					c.error(args.Pos, "unknown prop %q on component %s", resolvedName, comp.Name)
					continue
				}
				boundPropIdx[propName] = true
			}
			props = append(props, ir.Arg{Name: resolvedName, NamePos: arg.NamePos, Value: val})

		case ast.EventHandler:
			if prevPos, exists := seenNames[arg.Name]; exists {
				c.error(arg.Pos, "duplicate event handler %q (first at %v)", arg.Name, prevPos)
				continue
			}
			seenNames[arg.Name] = arg.Pos
			params := make([]*ir.Param, len(arg.Params.Params))
			for i, p := range arg.Params.Params {
				typ := c.resolveType(p.Type)
				if typ.Kind == ir.TypeDyn && comp != nil {
					if et := componentEventType(comp, arg.Name); et != nil {
						typ = et
					}
				}
				params[i] = &ir.Param{Name: p.Name, Type: typ}
			}
			fn := &ir.Func{Params: params}
			c.pushScope()
			for _, p := range params {
				c.scope.Declare(p)
			}
			fn.Block = c.checkBlockIR(&arg.Body)
			c.popScope()
			handlers = append(handlers, ir.EventHandler{
				AST:  &arg,
				Name: arg.Name,
				Func: fn,
			})
		}
	}

	// Arity: required props must be bound.
	if comp != nil {
		for _, p := range comp.Props {
			if !boundPropIdx[p.Name] && p.Default == nil {
				// Only error if the prop wasn't provided via named arg either.
				found := false
				for _, irArg := range props {
					pname := irArg.Name
					if strings.HasPrefix(pname, ":") {
						pname = pname[1:]
					}
					if pname == p.Name {
						found = true
						break
					}
				}
				if !found {
					c.error(args.Pos, "missing required prop %q on component %s", p.Name, comp.Name)
				}
			}
		}
	}

	return c.desugarBindings(comp, props, handlers)
}
```

- [ ] **Step 2: Run checker tests**

```
go test ./internal/checker/...
```

Expected: all existing tests pass.

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(checker): checkAndSplitArgs supports positional component props"
```

---

## Task 8b: Checker — `checkComponentCallArgs` positional support

**Files:**
- Modify: `internal/checker/expr.go:2869-2930`

`checkComponentCallArgs` handles the component-in-expression form (`text(value="hi")` inside an expression context). It currently ignores positional args (`arg.Name == ""` → silently skipped at line 2900). It needs the same positional bind treatment as `checkAndSplitArgs`.

- [ ] **Step 1: Add order check and positional binding**

Replace the body of `checkComponentCallArgs` with a version that:
1. Checks positional-after-named order (error if violated)
2. Resolves positional args against `comp.Props[positional]` by index
3. Validates unknown props for named args (existing behavior)
4. Checks arity for required props

```go
func (c *checker) checkComponentCallArgs(call *ast.CallExpr, comp *ir.Component) []ir.CallArg {
	var result []ir.CallArg

	// Order check.
	seenNamed := false
	for _, a := range call.Args.Args {
		if arg, ok := a.(ast.Arg); ok {
			if arg.Name != "" && arg.Name != "key" {
				seenNamed = true
			} else if arg.Name == "" && seenNamed && arg.Value != nil {
				c.error(*arg.Value.ExprPos(), "positional argument after named argument")
				return nil
			}
		}
	}

	boundProps := make(map[string]bool)
	positional := 0
	for _, a := range call.Args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			if arg.Name == "key" {
				continue
			}
			var resolvedName string
			var expected *ir.Type

			if arg.Name == "" {
				// Positional: bind to next prop slot.
				if positional >= len(comp.Props) {
					if arg.Value != nil {
						c.error(*arg.Value.ExprPos(), "too many positional arguments")
					}
					positional++
					continue
				}
				p := comp.Props[positional]
				resolvedName = p.Name
				expected = p.Type
				positional++
			} else {
				resolvedName = arg.Name
				propName := resolvedName
				if strings.HasPrefix(propName, ":") {
					propName = propName[1:]
				}
				if !componentHasProp(comp, propName) && !componentHasEvent(comp, propName) {
					c.error(*call.Func.ExprPos(), "unknown prop %q on component %s", arg.Name, comp.Name)
					continue
				}
				expected = componentPropType(comp, propName)
			}

			if arg.Value != nil {
				argExpr := c.checkExprExpecting(arg.Value, expected)
				actual := exprType(argExpr)
				c.requireValueType(actual, *arg.Value.ExprPos())
				if expected != nil && actual.Kind != ir.TypeDyn && expected.Kind != ir.TypeDyn && !actual.IsAssignableTo(expected) {
					if adapted, ok := adaptLiteralZero(argExpr, expected); ok {
						argExpr = adapted
					} else if callExpr, _ := c.implicitCall(arg.Value, actual, expected); callExpr != nil {
						argExpr = c.checkExpr(callExpr)
					} else {
						c.error(*arg.Value.ExprPos(), "cannot pass %s as %s", actual, expected)
					}
				}
				if expected != nil && expected.Kind != ir.TypeDyn {
					argExpr = wrapIfNeeded(argExpr, expected)
				}
				result = append(result, ir.CallArg{Name: resolvedName, NamePos: arg.NamePos, Value: argExpr})
				boundProps[resolvedName] = true
			}

		case ast.EventHandler:
			c.pushScope()
			for _, p := range arg.Params.Params {
				typ := c.resolveType(p.Type)
				if typ.Kind == ir.TypeDyn {
					if et := componentEventType(comp, arg.Name); et != nil {
						typ = et
					}
				}
				c.scope.Declare(&ir.Param{Name: p.Name, Type: typ})
			}
			c.checkBlock(&arg.Body)
			c.popScope()
			if !componentHasEvent(comp, arg.Name) {
				c.error(*call.Func.ExprPos(), "unknown event %q on component %s", arg.Name, comp.Name)
			}
		}
	}

	// Arity: required props must be bound.
	for _, p := range comp.Props {
		if !boundProps[p.Name] && p.Default == nil {
			c.error(call.Args.Pos, "missing required prop %q on component %s", p.Name, comp.Name)
		}
	}

	return result
}
```

- [ ] **Step 2: Run checker tests**

```
go test ./internal/checker/...
```

Expected: all existing tests pass.

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(checker): checkComponentCallArgs supports positional props"
```

---

## Task 9: Checker — `buildWindow` and `buildTimer` positional support

**Files:**
- Modify: `internal/checker/checker.go:1491-1567`

Positional order for window: `title`, `href`, `favicon` (matching current named-arg convention). Positional order for timer: `interval`, `enabled`. Event handlers (@tick, @error) remain name-only.

- [ ] **Step 1: Extract positional args to a name→expr map**

Add a helper before `buildWindow`:

```go
// resolvePositionalArgs returns a map of prop-name→ast.Expr by applying the
// given positional order to any unnamed args in the ArgList. Named args are
// returned as-is. Caller must handle EventHandler entries separately.
func resolvePositionalArgs(args ast.ArgList, order []string) map[string]ast.Expr {
	result := make(map[string]ast.Expr)
	positional := 0
	for _, a := range args.Args {
		arg, ok := a.(ast.Arg)
		if !ok {
			continue
		}
		if arg.Name == "" {
			if positional < len(order) && arg.Value != nil {
				result[order[positional]] = arg.Value
			}
			positional++
		} else {
			if arg.Value != nil {
				result[arg.Name] = arg.Value
			}
		}
	}
	return result
}
```

- [ ] **Step 2: Update `buildWindow`**

```go
func (c *checker) buildWindow(vn *ast.VisualNode) *ir.Window {
	w := &ir.Window{AST: vn, Name: vn.ID, Typ: c.windowType}
	for _, name := range hrefPathParams(vn) {
		w.Vars = append(w.Vars, &ir.Var{Name: name, Type: TypString})
	}
	c.pushScope()
	defer c.popScope()
	for _, v := range w.Vars {
		c.scope.Declare(v)
	}
	namedArgs := resolvePositionalArgs(vn.Args, []string{"title", "href", "favicon"})
	if e, ok := namedArgs["href"]; ok {
		w.Href = c.checkExpr(e)
	}
	if e, ok := namedArgs["title"]; ok {
		w.Title = c.checkExpr(e)
	}
	if e, ok := namedArgs["favicon"]; ok {
		w.Favicon = c.checkExpr(e)
	}
	for _, a := range vn.Args.Args {
		if eh, ok := a.(ast.EventHandler); ok && eh.Name == "error" {
			w.ErrorHandler = c.buildErrorHandler(&eh)
		}
	}
	return w
}
```

- [ ] **Step 3: Update `buildTimer`**

```go
func (c *checker) buildTimer(vn *ast.VisualNode) *ir.Timer {
	t := &ir.Timer{AST: vn, Handler: &ir.Func{}}
	namedArgs := resolvePositionalArgs(vn.Args, []string{"interval", "enabled"})
	if e, ok := namedArgs["interval"]; ok {
		t.Interval = c.checkExpr(e)
	}
	if e, ok := namedArgs["enabled"]; ok {
		t.Enabled = c.checkExpr(e)
	}
	for _, a := range vn.Args.Args {
		if eh, ok := a.(ast.EventHandler); ok && eh.Name == "tick" {
			t.Handler = &ir.Func{Params: c.buildParams(eh.Params)}
			vn.Block = eh.Body
		}
	}
	return t
}
```

- [ ] **Step 4: Run checker tests**

```
go test ./internal/checker/...
```

Expected: all pass. `testdata/window_positional.sngl` and `testdata/test_timer_positional.sngl` should now check cleanly.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(checker): window and timer accept positional args"
```

---

## Task 10: Verify all testdata fixtures pass

- [ ] **Step 1: Run full test suite**

```
go tool verify
```

Expected: all tests pass, including:
- `test_named_args.sngl` — named func calls
- `test_named_defaults.sngl` — skip intermediate defaults
- `test_named_component_props.sngl` — positional + out-of-order props
- `test_named_func_type.sngl` — named call through typed var
- `test_underscore_params.sngl` — `_`-prefix params
- `window_positional.sngl` — positional window props
- `test_timer_positional.sngl` — positional timer props
- `error_named_after_positional.sngl` — ERROR directive matched
- `error_duplicate_named_arg.sngl` — ERROR directive matched
- `error_unknown_named_arg.sngl` — ERROR directive matched
- `error_missing_required_named.sngl` — ERROR directive matched
- `error_positional_only_named.sngl` — ERROR directive matched
- `error_named_anon_func_type.sngl` — ERROR directive matched
- `error_named_prop_after_positional.sngl` — ERROR directive matched
- `error_duplicate_named_prop.sngl` — ERROR directive matched
- `error_missing_required_prop.sngl` — ERROR directive matched

- [ ] **Step 2: Fix any failures**

If a testdata fixture fails with an unexpected error or the expected error message doesn't match, update the fixture's error string to match what the checker actually emits — do not change the checker to match a wrong message. The fixture is the spec; the message wording is flexible as long as it's clear.

- [ ] **Step 3: Final commit**

```bash
git commit -m "test: verify all named/positional arg fixtures pass"
```

---

## Self-Review

**Spec coverage:**
- Named args on func calls ✓ Task 7
- Positional args on component props ✓ Task 8
- Out-of-order named args ✓ Task 7 (`bindArgs` fills by name, returns in param order)
- Skip intermediate defaults via named args ✓ Task 7 (`bound[i] == nil` → omit from result)
- `_`-prefix positional-only params ✓ Task 7 (`strings.HasPrefix(p.Name, "_")` check)
- Anonymous func-type params positional-only ✓ Task 5 (`Name=""` from `resolveFuncType`)
- Named call through named func-type variable ✓ Tasks 5+7 (sig carries names → `bindArgs` uses them)
- Go imports expose param names ✓ Task 6
- Window positional args ✓ Task 9
- Timer positional args ✓ Task 9
- All error cases from fixtures ✓ Tasks 7, 8, 9

**Known edge cases handled:**
- Dynamic calls (no sig): Task 7 handles `sig == nil` path by passing through unchanged
- `checkComponentCallArgs` (component-in-expression form): not updated in this plan — it's a separate code path at expr.go:2869. It should get the same treatment as `checkAndSplitArgs`. Add a Task 8b if needed after reviewing.
- `desugarBindings`: the bidirectional `:prop` binding desugaring is preserved in Task 8; it fires after the positional resolution.
