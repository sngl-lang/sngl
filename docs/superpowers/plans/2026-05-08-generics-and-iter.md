# Phase 2: Generic Types, Generic Methods, and `iter<T>` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tighten SNGL's collection types so `list<T>.filter(...)` returns `list<T>` (not `list<dyn>`), `map<K,V>.keys()` returns `list<K>`, generic methods like `list<T>.map<U>(f)` infer `U` from the lambda, plus add `iter<T>` and `for k, v = m` map iteration.

**Architecture:** SNGL already has generics for *functions* (`FuncDef.TypeParams`, `inferTypeParams`, `bindTypeParams`). Phase 2 extends this to type *declarations* (`struct list<T> {}`) and method *declarations* on parameterized receivers (`func list<T>.length() int`). At call sites, the checker binds the receiver's actual type to the param and substitutes through the return type. Generic *methods* (`func list<T>.map<U>(...)`) reuse the existing function-level type-param infrastructure with the receiver's bindings as a starting environment.

**Tech Stack:** Go 1.22+, `modernc.org/egg` (parser generator).

---

## Spec — driven by failing fixtures

```
testdata/test_list_methods_typed.sngl    — passes today via dyn fallthrough; will pass via real T preservation
testdata/test_map_methods_typed.sngl     — same; keys() returns list<K>, values() returns list<V>
testdata/test_iter_for_loop.sngl         — fails today; needs iter<T>, map iteration, list→iter conversion
```

After Phase 2, all three pass for the right reasons (real type params, not `dyn`).

---

## File Structure

**Files modified:**

- `ast/ast.go` — `StructDef.TypeParams []string` if not already present
- `internal/parser/sngl.ebnf` — generic-receiver method decls (`func ident "<" ident-list ">" "." ident ...`)
- `internal/parser/zparser.go` — regenerated
- `internal/parser/build.go` — populate `StructDef.TypeParams` and `FuncDef.RecvTypeParams`
- `internal/checker/stdlib.go` — register stdlib types with their type params
- `internal/checker/resolve.go` — recognize `iter<T>` in type expressions
- `internal/checker/expr.go` — bind receiver's type to its TypeParams at call sites; substitute through return type; add list→iter assignability
- `internal/checker/checker.go` — for-loop accepts map<K,V> and binds k,v
- `ir/types.go` — `TypeIter` kind, `IterOf` constructor
- `lib/types.sngl` — `struct list<T> {}`, `struct map<K, V> {}`, `struct iter<T> {}`
- `lib/functions.sngl` — generic method decls with proper signatures (no more `m dyn` workaround)
- `codegen/lang/golang/`, `codegen/lang/javascript/`, `codegen/lang/kotlin/` — emit iteration via iter<T> as ordinary range/for-of/for-in (likely no change since the runtime types match)
- `codegen/platform/none/testrunner/eval.go` — for-loop variant on map; iter wrapping (if needed)

**No new files.**

---

## Phase A — Generic struct declarations

### Task A1: Add `TypeParams` to `StructDef`

**Files:**
- Modify: `ast/ast.go`

Check if `StructDef` already has a TypeParams field:

```bash
grep -A 8 "type StructDef struct" ast/ast.go
```

If absent, add it:

```go
type StructDef struct {
    Pos         Pos
    Name        string
    TypeParams  []string  // generic type parameters, e.g., ["T"] for `struct list<T> {}`
    Fields      []*StructField
    IsMultiline bool
}
```

If `StructDef` already implements `TypeExpr`, leave that path alone — we're only adding the parameter list at declaration time.

- [ ] **Step 1: Inspect current shape**

```bash
grep -A 8 "type StructDef struct" ast/ast.go
```

- [ ] **Step 2: Add the field if missing**

Edit `ast/ast.go`. If already present, skip to step 4.

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add ast/ast.go
git commit -m "feat(ast): StructDef.TypeParams for generic struct declarations"
```

### Task A2: Grammar accepts type params on struct decls

**Files:**
- Modify: `internal/parser/sngl.ebnf`
- Regenerate: `internal/parser/zparser.go`

The current grammar accepts `struct Name { fields }`. Extend to `struct Name [ "<" type_param_list ">" ] { fields }`.

Find existing function-decl type-param handling for reference:

```bash
grep -B 1 -A 8 "TypeParamList\|FuncDecl\|StructDecl" internal/parser/sngl.ebnf
```

Function decls likely already have something like `[ "<" TypeParamList ">" ]` after the function name. Mirror it on struct decls.

- [ ] **Step 1: Read existing productions**

```bash
grep -n "StructDecl\|TypeParamList\|FuncTail\|FuncName" internal/parser/sngl.ebnf
```

- [ ] **Step 2: Modify StructDecl production**

Add the optional type param list. Example:
```
StructDecl = kw_struct ident [ TypeParamList ] StructLitBody .
```

Reuse `TypeParamList` if it exists; otherwise define it as `lt ident { comma ident } gt` matching whatever `lt`/`gt` terminals are.

- [ ] **Step 3: Regenerate parser**

```bash
cd internal/parser && go run modernc.org/egg -o zparser.go -package parser -start Document sngl.ebnf
```

- [ ] **Step 4: Verify build**

```bash
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add internal/parser/sngl.ebnf internal/parser/zparser.go
git commit -m "feat(grammar): generic type parameters on struct declarations"
```

### Task A3: Builder populates `StructDef.TypeParams`

**Files:**
- Modify: `internal/parser/build.go`

Find the existing `buildStructDef` (or `buildStructDecl`):

```bash
grep -n "buildStructDef\|buildStructDecl" internal/parser/build.go
```

Read how it walks the parse tree. Add a step that, when the optional `TypeParamList` non-terminal is present, collects the idents into a `[]string` and assigns to `def.TypeParams`.

If function decls already populate `FuncDef.TypeParams` from a similar production, mirror that code:

```bash
grep -A 15 "TypeParams\|TypeParamList" internal/parser/build.go | head -40
```

- [ ] **Step 1: Read the existing builder**

- [ ] **Step 2: Mirror the function-decl TypeParam handling on struct decl**

Edit `buildStructDef` to walk the optional `TypeParamList` child and populate `TypeParams`.

- [ ] **Step 3: Add a focused test**

Append to `internal/parser/build_test.go` (or wherever struct-decl parser tests live):

```go
func TestParseGenericStruct(t *testing.T) {
    src := "struct list<T> {}"
    doc, err := Parse("test.sngl", []byte(src))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    s := doc.Stmts[0].(*ast.StructDef)
    if s.Name != "list" {
        t.Errorf("Name = %q, want list", s.Name)
    }
    if len(s.TypeParams) != 1 || s.TypeParams[0] != "T" {
        t.Errorf("TypeParams = %v, want [T]", s.TypeParams)
    }
}

func TestParseGenericStructTwoParams(t *testing.T) {
    src := "struct map<K, V> {}"
    doc, err := Parse("test.sngl", []byte(src))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    s := doc.Stmts[0].(*ast.StructDef)
    if len(s.TypeParams) != 2 || s.TypeParams[0] != "K" || s.TypeParams[1] != "V" {
        t.Errorf("TypeParams = %v, want [K V]", s.TypeParams)
    }
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/parser/ -run "TestParseGenericStruct" -v
go test ./...
git add internal/parser/build.go internal/parser/build_test.go
git commit -m "feat(parser): build StructDef.TypeParams from generic struct decls"
```

### Task A4: Checker registers parameterized stdlib types

**Files:**
- Modify: `internal/checker/stdlib.go`

The stdlib loader currently registers `list`, `map`, `iter` (after Phase D) as plain unparameterized struct types. Extend it to honor `StructDef.TypeParams`.

```bash
grep -n "RegisterStruct\|stdlibStructs\|loadStdlib" internal/checker/stdlib.go internal/checker/checker.go | head
```

When a stdlib struct decl carries `TypeParams`, register it such that resolving `list<int>` substitutes `T → int` in any reference's `Elems`. The existing `MapOf(k, v)` and `ListOf(elem)` constructors already do this for the built-in versions; the change here is hooking stdlib decls into that same machinery.

- [ ] **Step 1: Read the stdlib loader**

```bash
grep -n "registerStruct\|stdlib\\.Structs\\|reservedStdlibTypes" internal/checker/stdlib.go
```

- [ ] **Step 2: Plumb `TypeParams` through**

Whatever struct registration the loader does, capture `def.TypeParams` and store on the registered type so resolution can substitute.

This may already work for user-defined generic structs (since SNGL already supports them in user code per the existing `StructDef.TypeParams` field — verify with a fixture or grep). If yes, the change is purely "make stdlib decls go through the same path."

- [ ] **Step 3: Run regression tests**

```bash
go test ./internal/checker/
```

- [ ] **Step 4: Commit**

```bash
git add internal/checker/stdlib.go
git commit -m "feat(checker): stdlib structs carry their TypeParams"
```

---

## Phase B — Methods on parameterized receivers

### Task B1: Grammar accepts parameterized receiver in method decls

**Files:**
- Modify: `internal/parser/sngl.ebnf`
- Regenerate: `internal/parser/zparser.go`

Today: `func receiver.method(args) ReturnType { body }` where `receiver` is a bare ident.
After: `func receiver "<" type_param_list ">" "." method(args) ReturnType { body }` to introduce the receiver's type params into the method's scope.

Find the existing method-decl production (it's the receiver-form variant of `FuncDecl`):

```bash
grep -n "FuncDecl\|FuncName\|FuncTail\|receiver" internal/parser/sngl.ebnf
```

The current `FuncName` likely allows `ident "." ident`. Extend with optional `[ TypeParamList ]` between the first ident and the dot:

```
FuncName = ident [ TypeParamList ] dot ident | ident
```

(The bare `ident` alternative is for non-method funcs like `func add(a int, b int) int {}`.)

- [ ] **Step 1: Read FuncName production**

- [ ] **Step 2: Add the optional TypeParamList between receiver ident and dot**

- [ ] **Step 3: Regenerate**

```bash
cd internal/parser && go run modernc.org/egg -o zparser.go -package parser -start Document sngl.ebnf
```

- [ ] **Step 4: Verify build**

```bash
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add internal/parser/sngl.ebnf internal/parser/zparser.go
git commit -m "feat(grammar): generic-receiver method decls (func list<T>.method())"
```

### Task B2: AST + builder populate the receiver's TypeParams on FuncDef

**Files:**
- Modify: `ast/ast.go` (if needed)
- Modify: `internal/parser/build.go`

Add a `RecvTypeParams []string` field to `FuncDef` if the existing `TypeParams` doesn't cover this:

```go
type FuncDef struct {
    Pos             Pos
    Name            string
    TypeParams      []string  // method-level params: func name<U>(...)
    RecvTypeParams  []string  // receiver-level params: func list<T>.name(...)
    Params          ParamList
    ReturnType      TypeExpr
    Body            Expr
    Block           StmtBlock
}
```

Alternatively, store both in a single `TypeParams` field with the receiver params first — match what makes the checker's substitution logic simplest. Read `inferTypeParams` and `bindTypeParams` first to see what shape they expect:

```bash
grep -B 2 -A 20 "func.*inferTypeParams\|func.*bindTypeParams" internal/checker/expr.go
```

If a single `TypeParams` slice works, use that. The grammar production fills it from the receiver's `<...>` list at parse time.

- [ ] **Step 1: Check current `FuncDef.TypeParams` semantics**

- [ ] **Step 2: Decide single-slice vs split fields, document the choice in a comment**

- [ ] **Step 3: Update builder to populate from the new grammar element**

In `buildFuncDef` (or equivalent), when the receiver-form FuncName has a `TypeParamList`, collect its idents into the chosen field.

- [ ] **Step 4: Test**

Append to `internal/parser/build_test.go`:

```go
func TestParseGenericMethodDecl(t *testing.T) {
    src := "struct list<T> {}\nfunc list<T>.length() int {}"
    doc, err := Parse("test.sngl", []byte(src))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    var fn *ast.FuncDef
    for _, s := range doc.Stmts {
        if f, ok := s.(*ast.FuncDef); ok {
            fn = f
            break
        }
    }
    if fn == nil {
        t.Fatal("no FuncDef found")
    }
    if fn.Name != "list.length" && fn.Name != "length" {
        t.Errorf("Name = %q", fn.Name)
    }
    // Whichever field carries the T:
    found := false
    for _, p := range fn.TypeParams {
        if p == "T" {
            found = true
        }
    }
    // also check RecvTypeParams if you split
    if !found {
        for _, p := range fn.RecvTypeParams {
            if p == "T" {
                found = true
            }
        }
    }
    if !found {
        t.Errorf("expected T in type params; got TypeParams=%v RecvTypeParams=%v", fn.TypeParams, fn.RecvTypeParams)
    }
}
```

- [ ] **Step 5: Run + commit**

```bash
go test ./internal/parser/ -run "TestParseGenericMethod" -v
go test ./...
git add ast/ast.go internal/parser/build.go internal/parser/build_test.go
git commit -m "feat(parser): build receiver-level TypeParams on method FuncDefs"
```

### Task B3: Checker substitutes receiver's type at call sites

**Files:**
- Modify: `internal/checker/expr.go`

Today, when `lst.length()` is checked:
1. `lst`'s static type is `list` (or `list<int>` once Phase A lands)
2. The checker resolves `length` as a method on `list`
3. Returns the declared return type as-is

For `func list<T>.length() int`, return type is `int` — no T, no substitution needed. But for `func list<T>.filter(f func(T) bool) list<T>`, the checker must substitute T using the receiver's actual type-arg.

Find the method-call resolution:

```bash
grep -n "inferMethodCall\|LookupMethod\|methodCall" internal/checker/expr.go
```

Add a substitution pass: when the resolved method's `RecvTypeParams` is non-empty, build a `bindings map[string]*ir.Type` from the receiver's `Elems` (e.g., for `list<int>` operand, bind `T → int`), then `bindTypeParams` through the method's signature using those bindings.

If the existing `bindTypeParams` already handles arbitrary substitutions, the new code just builds the bindings map and calls it before computing the return type.

- [ ] **Step 1: Read inferMethodCall (or the checker's method dispatch)**

- [ ] **Step 2: Build receiver-type → param-name bindings**

```go
// At the method-call resolution site, after looking up the FuncSig:
recvType := exprType(operand)
var bindings map[string]*ir.Type
if recvType != nil && recvType.Kind == ir.TypeStruct && len(recvType.Elems) == len(sig.RecvTypeParams) {
    bindings = make(map[string]*ir.Type, len(sig.RecvTypeParams))
    for i, name := range sig.RecvTypeParams {
        bindings[name] = recvType.Elems[i]
    }
}
sig = substituteTypeParams(sig, bindings)
```

If `substituteTypeParams` doesn't exist as a standalone function, factor one out from `inferTypeParams` (which does method-level substitution).

- [ ] **Step 3: Test**

Add to a new file `internal/checker/generic_methods_test.go`:

```go
package checker_test

import (
    "testing"

    "git.duckfam.us/jonathan/sngl/internal/checker"
    "git.duckfam.us/jonathan/sngl/internal/parser"
    "git.duckfam.us/jonathan/sngl/ir"
)

func TestListFilterPreservesElementType(t *testing.T) {
    src := `var xs list<int> = [1, 2, 3]
var ys list<int> = xs.filter(func(x int) => x > 0)`
    doc, err := parser.Parse("test.sngl", []byte(src))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    _, diags := checker.Check(doc, &checker.Config{IsMain: true})
    for _, d := range diags {
        if d.Severity == ir.Error {
            t.Errorf("unexpected: %s", d.Error())
        }
    }
}

func TestListFilterTypeMismatchErrors(t *testing.T) {
    src := `var xs list<int> = [1, 2, 3]
var ys list<string> = xs.filter(func(x int) => x > 0)`
    doc, err := parser.Parse("test.sngl", []byte(src))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    _, diags := checker.Check(doc, &checker.Config{IsMain: true})
    found := false
    for _, d := range diags {
        if d.Severity == ir.Error {
            found = true
        }
    }
    if !found {
        t.Error("expected error: list<int> not assignable to list<string>")
    }
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/checker/ -run "TestListFilter" -v
go test ./...
git add internal/checker/expr.go internal/checker/generic_methods_test.go
git commit -m "feat(checker): substitute receiver type params in method return types"
```

### Task B4: Re-declare list / map / string stdlib methods with proper types

**Files:**
- Modify: `lib/types.sngl`, `lib/functions.sngl`

The existing decls use `func list.length(m dyn) int` (dyn-receiver workaround). Replace with proper generic forms:

```sngl
// In lib/types.sngl
struct list<T> {}
struct map<K, V> {}
```

```sngl
// In lib/functions.sngl

// list<T>
func list<T>.length() int {}
func list<T>.filter(f func(T) bool) list<T> {}
func list<T>.push(item T) list<T> {}
func list<T>.remove(idx int) list<T> {}
func list<T>.contains(item T) bool {}

// map<K, V>
func map<K, V>.length() int {}
func map<K, V>.keys() list<K> {}
func map<K, V>.values() list<V> {}
func map<K, V>.contains(key K) bool {}
func map<K, V>.get(key K, default V) V {}
```

(Audit existing list/map decls and replace each in place.)

Crucially: do NOT add `m dyn` or `(receiver dyn, ...)` — methods with parameterized receivers don't take an explicit receiver param; the implicit receiver is bound at call time.

- [ ] **Step 1: Find current decls**

```bash
grep -n "func list\\.\\|func map\\.\\|func string\\." lib/functions.sngl
```

- [ ] **Step 2: Replace each in place**

- [ ] **Step 3: Run all check-phase tests**

```bash
go test ./internal/checker/
```

Many tests may exercise list/map methods; if they fail because of type mismatches the existing dyn-permissiveness hid, fix the test source (the type errors are correct; the tests just used loose types).

- [ ] **Step 4: Run the test_list/test_map fixtures**

```bash
go test ./codegen/platform/none/testrunner/ -run "TestRunFixtures/test_list_methods_typed|TestRunFixtures/test_map_methods_typed" -v
```

Should still pass — assertions remain valid; types are now strict.

- [ ] **Step 5: Commit**

```bash
git add lib/types.sngl lib/functions.sngl
git commit -m "refactor(stdlib): list/map methods use parameterized receivers (drop dyn workaround)"
```

---

## Phase C — Method-level type parameters

### Task C1: Grammar + AST for `func list<T>.map<U>(f func(T) U) list<U>`

**Files:**
- Modify: `internal/parser/sngl.ebnf`
- Regenerate: `internal/parser/zparser.go`
- Modify: `internal/parser/build.go`

Currently the grammar's `FuncDecl` allows `[ TypeParamList ]` after the function name (the method name). With the receiver-form gaining its own param list (B1), the shape becomes:

```
func ident [ TypeParamList ] dot ident [ TypeParamList ] (...) ReturnType { body }
```

Both lists are optional and independent. The first is the receiver's; the second is the method's own.

- [ ] **Step 1: Update FuncDecl production**

If B1 already added the receiver-form TypeParamList, the second TypeParamList may already be there from the existing grammar. Verify with a parse test.

- [ ] **Step 2: Builder populates both `RecvTypeParams` and `TypeParams`**

If they're separate fields, both get populated. If they're a single combined slice, append in order so substitution can distinguish via known-receiver-param-count.

- [ ] **Step 3: Test**

```go
func TestParseGenericMethodWithMethodTypeParam(t *testing.T) {
    src := "struct list<T> {}\nfunc list<T>.map<U>(f func(T) U) list<U> {}"
    doc, err := Parse("test.sngl", []byte(src))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    var fn *ast.FuncDef
    for _, s := range doc.Stmts {
        if f, ok := s.(*ast.FuncDef); ok {
            fn = f
            break
        }
    }
    if fn == nil {
        t.Fatal("no FuncDef")
    }
    // Receiver should have T; method should have U.
    // (Adapt assertion to whichever field layout was chosen.)
    seen := append(append([]string{}, fn.RecvTypeParams...), fn.TypeParams...)
    if !contains(seen, "T") || !contains(seen, "U") {
        t.Errorf("type params: got %v, want T+U", seen)
    }
}

func contains(xs []string, s string) bool {
    for _, x := range xs {
        if x == s {
            return true
        }
    }
    return false
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/parser/ -run "TestParseGenericMethodWith" -v
git add internal/parser/sngl.ebnf internal/parser/zparser.go internal/parser/build.go internal/parser/build_test.go
git commit -m "feat(parser): method-level type params on generic-receiver methods"
```

### Task C2: Checker infers method-level U from lambda return type

**Files:**
- Modify: `internal/checker/expr.go`

The existing `inferTypeParams` already handles function-level type params by matching arg types against param types. For a method with both receiver-level T (bound from receiver type) and method-level U (inferred from args), the flow is:

1. Bind T from receiver type → `bindings["T"] = int`
2. Substitute T through the method's param types → `f func(int) U`
3. Run `inferTypeParams` on the substituted signature with the actual args (a lambda `func(x int) => "{x}"`) — the lambda's return type is `string`, so `U → string` from `bindTypeParams(funcType{Param: int, Return: U}, funcType{Param: int, Return: string})`
4. Substitute U through the return type → `list<string>`

If the existing `inferTypeParams` only knows about method-level (or function-level) params and doesn't accept a pre-built bindings map, extend it. Add a variant:

```go
// inferTypeParamsWithBindings infers concrete types for a signature's type
// params, starting with `seed` bindings (e.g., from the receiver).
func (c *checker) inferTypeParamsWithBindings(sig *ir.FuncSig, args ast.ArgList, seed map[string]*ir.Type) *ir.FuncSig {
    bindings := make(map[string]*ir.Type, len(seed)+len(sig.TypeParams))
    for k, v := range seed {
        bindings[k] = v
    }
    // (existing inferTypeParams body, but using `bindings` instead of starting empty)
}
```

At the method-call site, build `seed` from the receiver's `Elems` and call this variant.

- [ ] **Step 1: Read inferTypeParams**

- [ ] **Step 2: Refactor to accept seed bindings**

- [ ] **Step 3: Wire into method-call dispatch (after Task B3's substitution)**

- [ ] **Step 4: Test**

Append to `internal/checker/generic_methods_test.go`:

```go
func TestListMapSameType(t *testing.T) {
    src := `var xs list<int> = [1, 2, 3]
var ys list<int> = xs.map(func(x int) => x * 2)`
    doc, err := parser.Parse("test.sngl", []byte(src))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    _, diags := checker.Check(doc, &checker.Config{IsMain: true})
    for _, d := range diags {
        if d.Severity == ir.Error {
            t.Errorf("unexpected: %s", d.Error())
        }
    }
}

func TestListMapDifferentType(t *testing.T) {
    src := `var xs list<int> = [1, 2, 3]
var ys list<string> = xs.map(func(x int) => "{x}")`
    doc, err := parser.Parse("test.sngl", []byte(src))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    _, diags := checker.Check(doc, &checker.Config{IsMain: true})
    for _, d := range diags {
        if d.Severity == ir.Error {
            t.Errorf("unexpected: %s", d.Error())
        }
    }
}

func TestListMapTypeMismatch(t *testing.T) {
    src := `var xs list<int> = [1, 2, 3]
var ys list<int> = xs.map(func(x int) => "{x}")`  // int → string mapping; assigning to list<int> is wrong
    doc, err := parser.Parse("test.sngl", []byte(src))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    _, diags := checker.Check(doc, &checker.Config{IsMain: true})
    found := false
    for _, d := range diags {
        if d.Severity == ir.Error {
            found = true
        }
    }
    if !found {
        t.Error("expected error: list<string> not assignable to list<int>")
    }
}
```

- [ ] **Step 5: Run + commit**

```bash
go test ./internal/checker/ -run "TestListMap" -v
go test ./...
git add internal/checker/expr.go internal/checker/generic_methods_test.go
git commit -m "feat(checker): infer method type params from lambda args (after receiver substitution)"
```

### Task C3: Add `list<T>.map<U>` to lib/functions.sngl

**Files:**
- Modify: `lib/functions.sngl`

Replace the existing `map` decl with the parameterized form. If the prior phase landed `func list<T>.filter(f func(T) bool) list<T>`, the analogous declaration for map is:

```sngl
func list<T>.map<U>(f func(T) U) list<U> {}
```

- [ ] **Step 1: Replace the decl**

- [ ] **Step 2: Run the test_list_methods_typed fixture, verify it still passes**

```bash
go test ./codegen/platform/none/testrunner/ -run "TestRunFixtures/test_list_methods_typed" -v
```

The fixture asserts both same-type (`xs.map(func(x int) => x * 2)` returning `list<int>`) and different-type (`xs.map(func(x int) => "{x}")` returning `list<string>`).

- [ ] **Step 3: Commit**

```bash
git add lib/functions.sngl
git commit -m "feat(stdlib): list<T>.map<U> with method-level type param"
```

---

## Phase D — `iter<T>` type

### Task D1: Add `TypeIter` kind to IR

**Files:**
- Modify: `ir/types.go`
- Test: `ir/types_test.go`

```go
const (
    // existing kinds...
    TypeIter // Elems = [T] for iter<T>
)

func IterOf(elem *Type) *Type {
    return &Type{Kind: TypeIter, Elems: []*Type{elem}}
}
```

Update `String()` to emit `iter<T>`. Update `Substitute`/`Equal` if they pattern-match on Kind.

- [ ] **Step 1: Add the kind, constructor, String case**

- [ ] **Step 2: Regenerate the stringer**

```bash
go generate ./ir/
```

- [ ] **Step 3: Test**

```go
func TestIterOfString(t *testing.T) {
    it := IterOf(TypString)
    if got := it.String(); got != "iter<string>" {
        t.Errorf("got %q, want iter<string>", got)
    }
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./ir/
git add ir/types.go ir/typekind_string.go ir/types_test.go
git commit -m "feat(ir): TypeIter kind and IterOf constructor"
```

### Task D2: Resolve `iter<T>` type expressions in the checker

**Files:**
- Modify: `internal/checker/resolve.go`

Find the existing `case "list":` / `case "map":` arm:

```bash
grep -n "case \"list\"\|case \"map\"" internal/checker/resolve.go
```

Add a peer:

```go
case "iter":
    if len(typeArgs) != 1 {
        c.errorf(pos, "iter requires exactly 1 type argument, got %d", len(typeArgs))
        return TypDyn
    }
    return IterOf(c.resolveType(typeArgs[0]))
```

(`IterOf` re-export — add `func IterOf(elem *ir.Type) *ir.Type { return ir.IterOf(elem) }` to `internal/checker/types.go` if needed.)

- [ ] **Step 1: Add the case + helper**

- [ ] **Step 2: Test**

Append to `internal/checker/map_test.go` or a new `iter_test.go`:

```go
func TestIterTypeResolves(t *testing.T) {
    src := `var x iter<int>`
    doc, _ := parser.Parse("t.sngl", []byte(src))
    _, diags := checker.Check(doc, &checker.Config{IsMain: true})
    for _, d := range diags {
        if d.Severity == ir.Error {
            t.Errorf("unexpected: %s", d.Error())
        }
    }
}
```

- [ ] **Step 3: Run + commit**

```bash
go test ./internal/checker/ -run "TestIter" -v
git add internal/checker/resolve.go internal/checker/types.go internal/checker/iter_test.go
git commit -m "feat(checker): resolve iter<T> type expressions"
```

### Task D3: Add `struct iter<T> {}` to lib/types.sngl

**Files:**
- Modify: `lib/types.sngl`

```sngl
struct iter<T> {}
```

Iter is opaque — no visible methods. for-loops bind to it; lists implicitly convert (Task D4).

- [ ] **Step 1: Add the decl**

- [ ] **Step 2: Run regression tests**

```bash
go test ./...
```

- [ ] **Step 3: Commit**

```bash
git add lib/types.sngl
git commit -m "feat(stdlib): iter<T> opaque generic type"
```

### Task D4: List → iter<T> implicit conversion

**Files:**
- Modify: `internal/checker/expr.go` (or wherever assignability is checked)

Find the assignability function:

```bash
grep -n "typeAssignable\|isAssignable\|assignableTo" internal/checker/
```

Add a rule: `list<T>` is assignable to `iter<T>`. This typically goes alongside other kind-based conversions (e.g., int → float if SNGL has that).

```go
// In typeAssignable(want, got):
if want.Kind == ir.TypeIter && got.Kind == ir.TypeList && len(want.Elems) == 1 && len(got.Elems) == 1 {
    return typeAssignable(want.Elems[0], got.Elems[0])
}
```

- [ ] **Step 1: Find and modify the assignability check**

- [ ] **Step 2: Test**

```go
func TestListAssignableToIter(t *testing.T) {
    src := `func count(xs iter<int>) int {
    var n = 0
    for _ = xs { n = n + 1 }
    return n
}
var lst list<int> = [1, 2, 3]
var n = count(lst)`
    doc, _ := parser.Parse("t.sngl", []byte(src))
    _, diags := checker.Check(doc, &checker.Config{IsMain: true})
    for _, d := range diags {
        if d.Severity == ir.Error {
            t.Errorf("unexpected: %s", d.Error())
        }
    }
}
```

(Note: this test will only pass once Task E1 also lands, since the for-loop body uses iter binding. Either land them together or put a `t.Skip(...)` until E1 lands.)

- [ ] **Step 3: Run + commit**

```bash
go test ./internal/checker/ -run "TestListAssignableToIter" -v
git add internal/checker/expr.go internal/checker/iter_test.go
git commit -m "feat(checker): list<T> implicitly converts to iter<T>"
```

---

## Phase E — for-loop on iter<T> and on map<K, V>

### Task E1: For-loop accepts iter<T>

**Files:**
- Modify: `internal/checker/checker.go` (or wherever `*ast.ForStmt` is checked)

Find the for-loop checker:

```bash
grep -n "checkForStmt\|ForStmt\|\"for iterator\"" internal/checker/*.go
```

Today the check probably says "for iterator must be list, got X". Extend to accept iter<T> AND list<T> (which converts), AND map<K, V> (Task E2).

```go
switch iter.Kind {
case ir.TypeList:
    elemT := iter.Elems[0]
    // Bind ForStmt.Key as elemT (single-var form) or as int + elemT (two-var form)
case ir.TypeIter:
    elemT := iter.Elems[0]
    // Single-var form: bind ForStmt.Key as elemT
case ir.TypeMap:
    keyT, valT := iter.Elems[0], iter.Elems[1]
    // Two-var form: bind ForStmt.Key as keyT, ForStmt.Value as valT
default:
    c.errorf(pos, "for iterator must be list, iter, or map; got %s", iter)
}
```

- [ ] **Step 1: Read the existing for-loop checker**

- [ ] **Step 2: Add iter case (single-var form only)**

- [ ] **Step 3: Test**

```go
func TestForLoopOnIter(t *testing.T) {
    src := `func count(xs iter<int>) int {
    var n = 0
    for x = xs { n = n + x }
    return n
}`
    doc, _ := parser.Parse("t.sngl", []byte(src))
    _, diags := checker.Check(doc, &checker.Config{IsMain: true})
    for _, d := range diags {
        if d.Severity == ir.Error {
            t.Errorf("unexpected: %s", d.Error())
        }
    }
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/checker/ -run "TestForLoopOnIter" -v
git add internal/checker/checker.go internal/checker/iter_test.go
git commit -m "feat(checker): for-loop accepts iter<T>"
```

### Task E2: For-loop accepts map<K, V> with two iterator vars

**Files:**
- Modify: `internal/checker/checker.go`

Two-var for-loop binds K to the first var, V to the second.

```go
case ir.TypeMap:
    if len(forStmt.Vars) != 2 {
        c.errorf(pos, "iterating over map<K, V> requires two variables: for k, v = m")
    } else {
        keyT, valT := iter.Elems[0], iter.Elems[1]
        scope.declare(forStmt.Vars[0], keyT)
        scope.declare(forStmt.Vars[1], valT)
    }
```

- [ ] **Step 1: Add the map case + bind two vars**

- [ ] **Step 2: Test**

```go
func TestForLoopOnMap(t *testing.T) {
    src := `var m map<string, int> = {a = 1, b = 2}
func test() int {
    var total = 0
    for k, v = m {
        total = total + v
    }
    return total
}`
    doc, _ := parser.Parse("t.sngl", []byte(src))
    _, diags := checker.Check(doc, &checker.Config{IsMain: true})
    for _, d := range diags {
        if d.Severity == ir.Error {
            t.Errorf("unexpected: %s", d.Error())
        }
    }
}

func TestForLoopOnMapSingleVarErrors(t *testing.T) {
    src := `var m map<string, int> = {a = 1}
func test() {
    for k = m {}
}`
    doc, _ := parser.Parse("t.sngl", []byte(src))
    _, diags := checker.Check(doc, &checker.Config{IsMain: true})
    found := false
    for _, d := range diags {
        if d.Severity == ir.Error {
            found = true
        }
    }
    if !found {
        t.Error("expected error: map iteration requires two vars")
    }
}
```

- [ ] **Step 3: Run + commit**

```bash
go test ./internal/checker/ -run "TestForLoopOnMap" -v
git add internal/checker/checker.go internal/checker/iter_test.go
git commit -m "feat(checker): for-loop accepts map<K, V> with two iterator variables"
```

---

## Phase F — Codegen for iter and map iteration

### Task F1: Go codegen — for-loop on map

**Files:**
- Modify: `codegen/lang/golang/golang.go` (or wherever for-loops are emitted)

Today Go codegen probably emits `for _, v := range list { ... }` for list iteration. For maps, emit `for k, v := range m { ... }`. The IR-level for-loop already carries operand type — dispatch on it.

```bash
grep -n "for.*range\|ForStmt\|emitFor" codegen/lang/golang/
```

Add a TypeMap branch.

- [ ] **Step 1: Find for-loop emission**
- [ ] **Step 2: Add map branch**
- [ ] **Step 3: Add a golden txtar for map iteration**
- [ ] **Step 4: Run + commit**

### Task F2: Go codegen — iter<T> as `iter.Seq[T]` or simple range

For `iter<T>` operands, emit Go's iter.Seq[T] range form:
```go
for x := range it {
    // body
}
```

Where `it` is of type `iter.Seq[T]` (Go 1.23+). Or, simpler: model `iter<T>` at runtime as `[]T` in Go (since list→iter conversion lets a list be passed where iter is expected, the runtime can just be a list). This avoids needing iter.Seq.

Pick the simpler approach: `iter<T>` runtime type is `[]T`. The Go codegen emits `for _, x := range xs { ... }` regardless.

- [ ] **Step 1: Decide runtime representation (recommend []T)**
- [ ] **Step 2: Emit for-loop accordingly**
- [ ] **Step 3: Test**
- [ ] **Step 4: Commit**

### Task F3: JS codegen — for-loop on map + iter

Similar treatment for JS:
- Map: `for (const [k, v] of m.entries()) { ... }` (since JS Map uses .entries())
- iter<T>: backed by a JS array; `for (const x of xs)` works for both

- [ ] **Steps mirror F1/F2 for JS codegen**

### Task F4: Kotlin codegen — for-loop on map + iter

- Map: `for ((k, v) in m) { ... }`
- iter<T>: backed by `List<T>`

- [ ] **Steps mirror F1/F2 for Kotlin codegen**

### Task F5: testrunner — iter binding in for-loop, map iteration

**Files:**
- Modify: `codegen/platform/none/testrunner/eval.go`

Find for-loop execution:

```bash
grep -n "ForStmt\|case \\*ir\\.For\|execFor" codegen/platform/none/testrunner/
```

Add:
- iter operand: treat as a slice (interpreter representation of list)
- map operand with two vars: walk `range m` style, binding both names

```go
if m, ok := iterVal.(map[string]any); ok {
    if len(forStmt.Vars) == 2 {
        for k, v := range m {
            env.vars[forStmt.Vars[0].Name] = k
            env.vars[forStmt.Vars[1].Name] = v
            // execute body
        }
    }
}
```

- [ ] **Step 1: Add map iteration**
- [ ] **Step 2: Run test_iter_for_loop fixture**

```bash
go test ./codegen/platform/none/testrunner/ -run "TestRunFixtures/test_iter_for_loop" -v
```

Expected: PASS (all assertions in the fixture).

- [ ] **Step 3: Commit**

```bash
git add codegen/platform/none/testrunner/eval.go
git commit -m "feat(testrunner): for-loop on map + iter; test_iter_for_loop passes"
```

---

## Phase G — Final sweep

### Task G1: Verify all fixtures pass for the right reasons

**Files:**
- Run fixtures.

```bash
go test ./codegen/platform/none/testrunner/ -run "TestRunFixtures/test_(map|list|iter|i18n)_" -v
```

All map/list/iter/i18n fixtures should pass.

If any fixture passes today via dyn-fallthrough but tighter types break it, fix the fixture's type annotations (the assertion behavior should remain correct).

- [ ] **Step 1: Run + investigate any failures**

- [ ] **Step 2: If fixtures need stricter types, update them**

- [ ] **Step 3: Run go tool verify**

```bash
go tool verify
```

Expected: PASS.

- [ ] **Step 4: Update CLAUDE.md**

Add a brief mention of the iter<T> type and the generic-method support to the existing "Built-in Generic Types" section:

```markdown
- **`iter<T>`** — opaque iterator type. `list<T>` and `map<K,V>` implicitly convert; `for x = it` binds elements.
```

And note the generic-method capability:

```markdown
- Stdlib types support generic methods: `list<T>.map<U>(f func(T) U) list<U>` etc. The checker substitutes T from the receiver's actual type-arg, then infers method-level type params from the call's actual args.
```

- [ ] **Step 5: Final commit**

```bash
git add CLAUDE.md
git commit -m "docs: iter<T> + generic methods on stdlib types"
```

---

## Self-review

### Spec coverage

- `test_iter_for_loop.sngl` (currently failing) — Tasks D1-D4 (iter<T>), E1-E2 (for-loop binding), F5 (testrunner)
- `test_list_methods_typed.sngl` (passes via dyn) — Tasks A1-A4 (generic struct decls), B1-B4 (parameterized methods), C1-C3 (method-level params)
- `test_map_methods_typed.sngl` (passes via dyn) — same as above

### Placeholder scan

No `TBD`/`TODO`/`implement later` in step bodies. Tasks F1-F4 (codegen for map/iter) carry brief outlines because the precise emission depends on each codegen's existing for-loop pattern — that's read-the-existing-code work, not a placeholder.

### Type consistency

- `RecvTypeParams []string` introduced in B2 and consistently used in B3 and C2.
- If the chosen design uses a single `TypeParams` slice with receiver params first, that variant must be used consistently. Tasks B2, B3, C1, C2 all reference the same convention.
- `IterOf` constructor in D1 used in D2 + D4 + downstream codegen.

### Risk notes

- **Task A3** depends on the existing user-defined-generic-struct path working (since stdlib decls go through the same code). If user-defined `struct foo<T> {}` doesn't already work, Task A3 grows to add that support.
- **Task B3** is the lynchpin — receiver-type substitution is what makes the rest work. If `bindTypeParams` can't be reused cleanly, the substitution logic gets more invasive.
- **Task F1-F4** (codegen) is largely "no behavior change" if the existing codegen already iterates by IR type. If the codegen has hardcoded "list iteration" assumptions, more work is needed.
- **Phase D-E ordering** — Task D4 (list→iter conversion) is testable only after E1 (for-loop binding). Either fold them together or accept that D4's test stays skipped until E1 lands.

### Out of scope

- General-purpose `match` expression syntax (separate plan tracked in glab issue #54)
- Hybrid match-based i18n syntax (issue #54)
- HTML/Android i18n runtime overrides (deferred from cleanup plan)
- iter<T> for user-defined types that want to opt in (this plan only handles list and map)
