# AST / IR audit

Concrete findings on `ast/` and `ir/`, ordered roughly by impact within
each section. Every finding is grounded in a specific file:line.

---

## 1. AST issues

### 1.1 `ast.Pos` is line/column-only — no byte offset, no end position

`ast/ast.go:16-33` defines `Pos{File, Line, Column}`. There is no `EndPos`
on any node and `Pos` carries no byte offset. Consequences:

- Range-based diagnostics (`Pos: ast.Pos{...} → End: ast.Pos{...}`) are
  impossible. Every error is a 1:1 single-caret.
- LSP semantic-token / completion code has to re-derive token widths from
  source text (search for `NamePos`-style fields — see `ast/expr.go:340`
  `Arg.NamePos`, `ast/ast.go:147` `StructField.NamePositions`).
- `Format()` cannot use offsets to copy whitespace/comments verbatim — see
  finding 1.7 below.

This is the single biggest structural defect. Adding `EndPos` + byte
offset on `Pos` (and an `End()` method on every node) would unblock
real-range diagnostics, LSP hover regions, and offset-based round-trips.

### 1.2 Heterogeneous storage of `Pos`: pointer-returning `ExprPos`/`StmtPos`

`ast/expr.go:4-6` says `Expr.ExprPos() *Pos`. Every implementation
returns `&x.Pos` (`ast/expr.go:403-428`). Returning a `*Pos` rather than
`Pos` invites callers to mutate the AST through the pointer, and on the
critical path is a needless indirection. There is no place in the
codebase where the pointer is used to mutate the position; `git grep "ExprPos().Line" | wc -l` would be the natural read pattern.

Suggested: change to `Pos()` returning `ast.Pos` by value.

### 1.3 `Stmt` and `Expr` interfaces are not exhaustive — no compile-time enforcement

`Stmt` (`ast/expr.go:289`) and `Expr` (`ast/expr.go:4`) are open
interfaces. There is no `astStmt()`/`astExpr()` marker method that all
variants implement to make exhaustive type switches possible. Compare
with `enumBodyItem()` / `structBodyItem()` / `paramOrEventDecl()` /
`argOrEventHandler()` which *do* use sealed marker methods
(`ast/ast.go:65,103,281; ast/expr.go:347`).

Result: a `switch s := s.(type)` over `ast.Stmt` has no way to enforce
completeness, and several `default: panic(...)` sites exist
(e.g. `ir/convert.go:471`, `:617`, `:817`, `:900`). Adding a sealed
marker would let a `go vet`-style exhaustiveness linter catch missed
variants — and most of those panic defaults could be removed.

### 1.4 `LambdaExpr` and `FuncDef` share a "exactly one of Body or Block" invariant in prose only

`ast/ast.go:227-237` `FuncDef.Body / Block` and `ast/expr.go:264-270`
`LambdaExpr.Body / Block` both say in comments "Exactly one of Body or
Block is set." There's no `IsBlock()` helper, no constructor enforcing
it, and `StmtBlock.IsDefined()` (`ast/expr.go:354`) is the only
indirect check. Make this a sum type (interface) or replace with a
single `Body LambdaBody` where `LambdaBody = ExprBody | StmtBlockBody`.

### 1.5 `IncDecStmt`'s "lowered in the checker" comment is misleading

`ast/expr.go:307-314` says "Lowered in the checker to `target = target + 1`".
Grep shows it survives to `internal/parser/format.go:600` (round-trip) but
nothing in `internal/checker` writes back an `*ir.Assign` form — it is
re-handled inline. The comment is documenting checker behavior, but
the AST node is then preserved in the canonical AST. Either lift the
explanation out of the AST file (where it doesn't belong) or actually
desugar before producing IR.

### 1.6 `EventDecl` / `Param` / `EventHandler` are returned by value, not by pointer

`ast/ast.go:285-286` `func (Param) paramOrEventDecl()` / `func (EventDecl) paramOrEventDecl()`. Other AST nodes are pointer-receiver.
This produces two issues:
1. Inconsistent storage — `PropList.Props []ParamOrEventDecl` holds
   values, while everything else (e.g. `[]Stmt`, `[]Expr`) holds
   pointers.
2. The interface table for a value type can't be `nil`-checked the
   same way; ast-walking code can't normalize `switch x := s.(type)`.

`ast/expr.go:340-352` `Arg` / `EventHandler` do the same thing
(`func (Arg) argOrEventHandler()`).

### 1.7 No comment/whitespace preservation on `Document`; `Comment` is a `Stmt`

`ast/ast.go:36-41` `Comment` exists, and `ast/ast.go:356` makes it a
`Stmt`. But:

- There's no slot for comments attached to specific declarations
  (no leading/trailing comment lists on `FuncDef`, `VarDecl`, etc).
- Whitespace (blank lines) is not represented at all.
- `Format()` calls `convertStmtBlock` in `ir/convert.go:476-487`,
  which materializes `block.Pos = ast.Pos{Line:1}` to fake `IsDefined()` —
  i.e. round-tripping IR→AST does not preserve original positions,
  and any inline comment in the source is lost.

`docs/superpowers/audit/orthogonality.md` likely overlaps; this audit
flags it for AST-shape impact.

### 1.8 `IsMultiline` is layout state baked into the AST

Eight separate `IsMultiline bool` fields exist on `StructDef`,
`EnumDef`, `UnitDef`, `ComponentDecl`, `ParamList`, `PropList`,
`ListExpr`, `StructExpr`, `StmtBlock`, `ArgList`
(`ast/ast.go:76,116,158,...`; `ast/expr.go:225,326,333`). This is
formatter-only state that the checker and IR shouldn't need to see.
Splitting "lexical layout" from "structural AST" would let
`ir.Convert` stop fabricating it (`ir/convert.go:96` sets
`IsMultiline: len(s.Fields) > 1`).

### 1.9 `LiteralExpr.Raw` keeps source text, but `LiteralKind` is also stored — overlap

`ast/expr.go:131-135` keeps both `Kind LiteralKind` and `Raw string`.
`UnitLiteral` (`ast/expr.go:138-142`) embeds `LiteralExpr` AND
duplicates `Pos`, AND adds `Suffix string`. The parser uses
`LiteralExpr` for unit literals (see `internal/parser/build.go`).
`UnitLiteral` looks unused at runtime — grep shows it referenced from
`codegen/lang/golang/helpers.go:157` but that branch may be dead.
Verify and delete or actually use it consistently.

### 1.10 `TypeExpr` includes anonymous `StructDef`/`EnumDef`/`UnitDef`, mixing decl + type

`ast/expr.go:432-436` makes the same node `StructDef` simultaneously a
`Stmt`, an `Expr`, AND a `TypeExpr`. So `var x struct { a int }` and
`struct Foo { a int }` share the node type with `Name = ""`. Callers
must check `Name == ""` to disambiguate. A separate `StructTypeExpr`
that wraps a body would make exhaustive switches honest.

### 1.11 `UnaryExpr` is a `TargetExpr` for `*p` deref — but `&` is also a `UnaryOp`

`ast/expr.go:443` `func (*UnaryExpr) targetExpr() {}`. This means
`&x = ...` and `&x++` parse to legal-looking ASTs that the checker
must then reject. Splitting deref into its own `DerefExpr` (target) and
leaving address-of as a non-target `AddrExpr` would catch this at
type-system level.

---

## 2. IR issues

### 2.1 Synthesized IR nodes have no position (via `AST` backref) — scoped

> **Decision (2026-07-05):** The `AST *ast.X` backref stays as IR's single,
> intentional source of both position and structured syntax — IR will **not**
> grow a first-class `Pos` field, and the IR→AST link is kept long-term (this
> is the deliberate syntax/semantics boundary, not a leak). The earlier
> "add `Pos` to every IR node" recommendation is rejected. The real gap —
> synthesized nodes with `AST == nil` — is closed by:
>   1. **Propagating the origin `AST`** into synthesized nodes: a pass that
>      derives a node from a user node copies that node's `AST` (or the
>      nearest enclosing decl's) so position flows through the existing field.
>   2. **Graceful diagnostic fallback**: when a post-check diagnostic lands on
>      a node with `AST == nil`, resolve position from the enclosing
>      decl/component rather than 1:1.
>   3. **A synthetic `AST` node** where a good error message genuinely needs
>      one for an otherwise origin-less synthesized node.
>
> Not scheduled as a standalone task — applied opportunistically as
> synthesizing passes are touched. Separately, the analyses that *misuse* the
> backref by re-walking the AST post-check (purity — compiler-phases 1.3;
> testlower — lowering-migration #19) and the imports reading `imp.AST.Path`
> instead of the existing `imp.Path` are tracked by their own findings. Docs
> (`docs/lookup/*`) legitimately consume AST structure and keep doing so.

Every IR expr has `AST *ast.X` (`ir/expr.go:14,22,38,...`) and codegen
extracts positions through `e.AST.Pos`. The remaining gap:

- Synthesized IR (lowering, defaults) has `AST == nil` (`ir/expr.go:38`
  comment "nil for synthetic", `ir/expr.go:198` Closure "nil for
  synthesized"). Diagnostics on those nodes have no position at all.
- `ir.Literal` synthesized by `ZeroExpr` (`ir/defaults.go:11-28`) has
  no position info at all.

### 2.2 `TypeColor` exists but is "intentionally never produced"

`ir/types.go:25` declares `TypeColor`, but the comment at
`ir/types.go:425-431` says color is uniformly carried as a `TypeStruct`
with a stdlib decl. `IsColorStruct(t)` is the detector. Meanwhile
`isStringDomain` (`ir/types.go:420`) still includes `TypeColor`, and
`ZeroExpr` (`ir/defaults.go:17`) and `String()` (`ir/types.go:172`)
still handle `TypeColor`. This is a dead variant masquerading as live.
Either remove `TypeColor` or actually produce it.

### 2.3 `Type` is a single struct with optional slots — looks like a tagged union but isn't

`ir/types.go:49-57`:

```go
type Type struct {
	Kind      TypeKind
	Elems     []*Type
	Decl      Symbol
	Sig       *FuncSig
	ParamName string
	Package   string
	Meta      any
}
```

This is fine in C but every Go IR I've seen prefers separate concrete
types (`*ListType`, `*StructType`, `*FuncType`, ...) with a shared
interface. Issues with the current shape:

- `Equal` (`ir/types.go:309`) does case-by-case sets of fields.
- `Substitute` (`ir/types.go:244`) hand-recurses, and missing
  `TypeFunc.Sig.Substitute` is what was added retroactively.
- Adding a new type kind requires updating `String()`, `Equal()`,
  `Substitute()`, `IsAssignableTo()`, `ZeroExpr()`, `IsConst()`,
  `typekind_string.go` (codegen).
- `Meta any` (`ir/types.go:56`) is a typed-state escape hatch — see
  user memory `feedback_typed_state.md`. It's used by platforms
  (e.g. cgo bindings via `NativeTypeRef` at `ir/types.go:498`) — fine
  but typed wrappers would be cleaner.

### 2.4 `Func.Block []Stmt` — but no `*Func.IsBlock()`/no explicit "expr body" representation

`ir/ir.go:125` "Block []Stmt — type-checked statements (expression
bodies become a single Return)". The lowering normalization is good,
but `ZeroExpr`'s `zeroFuncExpr` (`ir/defaults.go:64-90`) constructs
`fn.Block = []Stmt{&Return{Value: z}}` directly — so any pass that
treats "single-Return block" specially needs to be careful (e.g.
inline-pure). Worth a comment or helper.

### 2.5 `Lambda` and `Closure` are two distinct nodes for the same source construct

`ir/expr.go:185-201`. Pre-`NoLambda` the AST emits `Lambda`; post-pass
emits `Closure`. Both expose the same `Func` field. Effects-/lower-
agnostic walkers handle both cases identically every time (see
`ir/async.go:141-149`, `ir/expr.go:309`). Either:

- Merge into one node with a nullable `State *StructLit` (nil = pure
  lambda, non-nil = capturing closure), or
- Ensure `NoLambda` runs unconditionally before any pass that walks
  expressions, and delete `Lambda`.

### 2.6 Stale post-lower IR nodes survive into codegen

`PlatformFilter`, `SlotInst`, `ErrorBoundary` are IR statements
that grep shows every codegen backend has to handle:

- `codegen/treewalk.go:56-61` walks them
- `codegen/lang/golang/golang.go:543-553` switches on them
- `codegen/lang/golang/http.go:216-244` again
- `codegen/lang/golang/helpers_emit.go:258-262`
- `codegen/analysis.go:206-246`

Per CLAUDE.md the pipeline is "Optimize → Lower → Codegen", so by the
time codegen runs these should be gone. They aren't — verify whether
lower's `passPlatformExtensionBody`/`passReactivity` actually strips
them, or whether they leak through. (See also
`docs/superpowers/audit/lowering-migration.md` finding 1.)

### 2.7 `Window.stmtNode()` makes `*Window` both a top-level decl and a `Stmt`

`ir/ir.go:290`: `func (w *Window) stmtNode()`. Comment says "Window can
appear as a statement in for-loop bodies". This is structurally OK but
asymmetric — `pkg.Windows` holds top-level windows AND inner statement
walkers must handle `*Window` (see `codegen/treewalk.go:64`). A nested
window appearing in a for-loop body would need both representation,
and `pkg.Windows` is supposed to be flat after hoist
(`ir/stmt.go:137` `For.HoistedWindowIDs`). Worth confirming whether
`*Window` truly survives in statement position post-optimize.

### 2.8 `LiftedCaptures` and `AddressedVars` are "extra" cap-state living on `Package`

`ir/ir.go:48-58`. These are pass-output side-tables — fine — but they
live on the IR root with no namespacing, and they're populated only by
`NoLambda`/`NoRef`. If `NoLambda` doesn't run (any future codegen that
supports closures natively), `LiftedCaptures` is nil and downstream
consumers panic. Make them required fields populated by a specific
analyzer, or move into a `LoweringMetadata` substruct.

### 2.9 `ContextRead` is a one-off Expr defined in `context.go` instead of `expr.go`

`ir/context.go:37-44`. It's a perfectly normal `Expr` (implements
`exprNode()`, `ExprType()`) but it's split off because Context is a
"feature". Same with `ContextProvider` as a `Stmt`. Two issues:

1. `strip.go` doesn't strip `ContextRead`/`ContextProvider`
   (`ir/strip.go:212-281`). Either inert (no AST pointer to strip,
   safe) or — more likely — a latent bug if they ever carry cross-
   references that should be cleared for DeepEqual.
2. `IsConst` (`ir/expr.go:241`) doesn't recognise `*ContextRead`.
   Calls reading context never const-fold even when the context value
   is itself a const. Minor.

### 2.10 `IsConst` falls through on `*MapLitIR`, `*ContextRead`, `*Conversion` (sort of)

`ir/expr.go:241-313`. `MapLitIR` is missing from the switch entirely
— a const map literal is *never* considered const. `Closure` returns
false (correct). `Conversion` returns `IsConst(Operand)` (`:287`) —
but this is too generous when the conversion has side effects
(parse/validate at runtime for date/email/etc.).

### 2.11 `CallArg` and `Arg` are two near-identical types

`ir/expr.go:112-116` `CallArg{Name, NamePos, Value}` is used for
`Call.Args` and `Emit.Args`. `ir/stmt.go:30-36` `Arg{Name, NamePos, Value}` is the same shape, used for `NodeInst.Props`. There is no
reason to have both; merge to one type.

### 2.12 `Receiver` on `Call` is ambiguous (namespace vs method receiver)

`ir/expr.go:69-86` comment block tries to explain: "Plain function:
Func set, Receiver nil. Type-attached method: Args[0] is the
receiver. Namespace call: Receiver set to the namespace ident."

This overloads one field. A `Call` for `string.upper(x)` has
`Args[0] = x` and `Receiver = nil`, while `pkg.foo()` has
`Receiver = <Ident pkg>` and Func may or may not be set. Code
inspecting `Call` (e.g. async analysis at `ir/async.go:110`) has to
know this convention. Cleaner: separate `Callee Expr` for funcvar
calls (already present at `:74`), an explicit `Namespace *Ident`, and
drop `Receiver`.

### 2.13 `ErrorMode` constants partly redundant with `ResolvedHandler != nil`

`ir/expr.go:88-107` defines five `ErrorMode` values, but the checker
sets `ResolvedHandler` alongside `ErrorMode`. The relationship between
the two is implicit (`ErrorPerCall ↔ ErrorHandler != nil`,
`ErrorInvokeAndTerminate ↔ ResolvedHandler != nil and != ErrorHandler`).
A single enum or a sum type would avoid invariant drift.

### 2.14 `Type.Equal` returns `true` from a no-op default case for any unknown kind

`ir/types.go:309-355`. The default case (after the type switch) is
`return true` — meaning two `Type{Kind: TypeColor}` instances are
"equal" by virtue of falling out. That's correct for singleton-style
primitives, but it also means a future kind added without a case
arm will silently match. A `default: return t == other` would be
safer.

### 2.15 `IsAssignableTo` materializes implicit conversions, but rules are checker-internal

`ir/types.go:360-416` lists implicit conversion rules (null → option,
int → float, string ↔ string-domain, list ↔ iter, map K-erasure to
dyn, etc.). But the user-memory rule says these all must materialize
as `ir.Conversion`. The checker uses `wrapIfNeeded`
(`internal/checker/coerce.go:41-56`) after each call — but several
`IsAssignableTo` callsites in the checker do **not** call
`wrapIfNeeded`:

- `internal/checker/expr.go:942` `recvParamStyle` method-call dispatch
- `internal/checker/expr.go:2412` event-handler param check
- `internal/checker/expr.go:2627` `implicitCall` (no wrap, but the
  call is being synthesized — possible bug if the synthesized call's
  return type later flows somewhere expecting conversion).
- `internal/checker/context.go:35` context-value default check

Some of these are pure validity checks (no expression in flight), but
each should be audited to confirm no actual coercion site is missing
the wrap. (Per user feedback `feedback_explicit_conversions.md`.)

### 2.16 Centralised `ir.Walk` exists; consumers not yet migrated — PARTIAL

> **Status (2026-07-05):** The centralised walker now exists —
> `ir.Walk(pkg, VisitorFuncs{Stmt, Expr})` plus the single-callback
> `ir.WalkExprs`/`ir.WalkStmts` conveniences (`ir/walkexprs.go`): one
> traversal that panics on an unknown node kind, so a new IR shape extends
> exactly one site. **Remaining:** migrate the bespoke walkers below onto it.

Each consumer still reimplements stmt+expr traversal:

- `ir/async.go:21-150` walks for async detection
- `ir/strip.go:217-362` walks for AST-strip
- `internal/lower/walk.go:7-118` walks for lowering (but only top-level
  iteration over decls — not deep)
- `codegen/treewalk.go:35` `TreeWalker` (visual-tree only)
- `codegen/iterate.go:84+` collectWindows / collectReachableComponents
- `codegen/deps.go:350` ternary walk
- Many platform-side walkers — see `docs/superpowers/audit/lowering-migration.md`
  finding 2.

Each one switches over `ir.Stmt`/`ir.Expr` and panics on unknown
variants — so adding a new IR node breaks all of them at once. Migrating
them onto `ir.Walk` removes that fan-out.

### 2.17 IR is mutated in place across every pass — PARTIAL

> **Status (2026-07-05):** The missing clone helper now exists:
> `ir.ClonePackage` (`ir/clone.go`) is a reflection deep-copy with a
> pointer-identity map that re-points every cross-reference (Ident.Sym,
> Call.Func, StructLit.Def, Type.Decl, the Symbols table, the pointer-keyed
> side tables) to the cloned nodes while sharing AST nodes and the immutable
> global Type singletons. This fixed the multi-target build bug — the
> pipeline now lowers a per-target clone (see compiler-phases 2.1). The
> broader observation below still holds: individual passes mutate in place,
> so pass output still can't be cached and snapshot diffing still needs
> `StripForCompare`; `ClonePackage` unblocks that work but hasn't been
> applied to it yet.

`ir/strip.go` is the most extreme example — it mutates the Package
for `reflect.DeepEqual` test comparison (`ir/strip.go:7-11`). But
also `internal/lower/walk.go` callbacks return rewritten slices and
the package is mutated in place (`walkVar` reassigns `v.Init`). This
means no pass can run twice safely and no pass output can be cached.

### 2.18 `Symbol` interface is anaemic; resolution requires type-asserting

`ir/scope.go:6-9`: `Symbol{SymName, SymType}`. Every consumer does a
`switch sym.(type)` to dispatch. `ir/expr.go:31` `Ident.Sym Symbol`
holds the result. Adding e.g. a "PropSym" required no interface
change but every switch site must be updated. Acceptable but worth
documenting the exhaustive set of `Symbol` implementations
(`*Var, *Func, *Param, *Component, *StructDef, *EnumDef, *UnitDef, *Import, *Namespace, *LoopVar, *TypeSym, *Window, *Context`).

### 2.19 `Var.AST ast.Stmt` is typed as the interface, not the concrete decl

`ir/ir.go:178` `AST ast.Stmt`. Comment: "original ConstDecl or
VarDecl". Callers do `v.AST.(*ast.VarDecl)` to recover. Two issues:

- The same IR `*Var` corresponds to one **spec** inside a multi-spec
  `VarDecl` (`ast/ast.go:174-194`), so `v.AST` doesn't pinpoint
  which name — `NamePositions` lookup loses the connection.
- Type-asserting at every position is unsafe; a typed slot
  `*ast.VarSpec` (or pointer plus index) would be cleaner.

### 2.20 `RecvTypeParams` lifecycle: "consumed (set to nil) after substitution"

`ir/types.go:445` comment. This is mutable state on a shared
`*FuncSig` (since `Func.FuncSig()` at `ir/ir.go:163-171` constructs a
fresh sig from the Func, but the Func still has the field). Mutating
during substitution risks aliasing issues if `FuncSig` is ever shared
between two calls to the same generic.

---

## 3. AST ↔ IR boundary issues

### 3.1 `ir.Convert(pkg) → ast.Document` is a two-way bridge that drops information

`ir/convert.go:13-81` reconstructs an AST from IR. But:

- It fabricates `IsMultiline = len(...) > 1` (`:96`), losing original
  formatting.
- It sets `block.Pos = ast.Pos{Line: 1}` (`:485`) so `IsDefined()`
  returns true — i.e. all positions are bogus.
- It cannot represent `ContextRead` / `ContextProvider` cleanly —
  there's a `convertContextProvider` but bare `ContextRead`
  identifiers need to roundtrip back to plain `ast.IdentExpr`
  (`ir/convert.go:640` is the ElementRef branch; ContextRead handling
  needs verification).

Either commit to "IR is the canonical form, AST is parse-only" and
delete `Convert`, or make `Convert` lossless (preserve positions /
comments via backrefs).

### 3.2 Native imports use `NativeImport` in IR but reach into AST

`ir/ir.go:97-107` `NativeImport` holds `Structs, Funcs, Vars` — all
IR types. Good. But `Func.AST *ast.FuncDef` is `nil` for these
(`ir/ir.go:118` comment). Codegen accessing positions on a native
func crashes. Either define a synthetic Pos, or formalise a "no
source" sentinel.

### 3.3 `ir.Conversion` for explicit casts uses `*ast.CallExpr`, conflating user cast with implicit coercion

`ir/expr.go:120-123` `Conversion{AST *ast.CallExpr, Type, Operand}`.
Both `int(x)` (user-written cast) and the checker's implicit
wrapping (`internal/checker/coerce.go:55`) produce the same node.
There's no way to tell them apart for diagnostics. If an implicit
conversion fails at runtime, the error has no source span (the
synthesized form has `AST = nil`). Splitting into `ExplicitCast` and
`ImplicitConv` makes the IR more self-explanatory.

### 3.4 `Lambda.Func.AST` is nil — lambdas have no AST backref via Func

`ir/expr.go:186-189`. The Lambda itself has `AST *ast.LambdaExpr`,
but the inner `Func` has `AST: nil` (`ir/ir.go:118` "nil for lambdas
and event handlers"). So `func` walkers that go through `pkg.Funcs`
won't find lambdas, but walkers that visit `*Lambda` expr nodes will.
Two parallel namespaces.

### 3.5 `EmitStmt` keeps `Name string` but no resolved `EventDecl` pointer

`ir/stmt.go:87-91` `Emit{AST, Name, Args}`. Code at codegen has to
re-look-up which event by string name — the EventDecl is already
resolved at checker time. Add `EventDecl *EventDecl` to `Emit`.

### 3.6 Round-trip `Format` for the AST loses comments because checker→IR drops them

`internal/checker/checker.go:295` and `:943` skip `*ast.Comment` in
all phases. Comments never enter IR. So `parser.Format(parser.Parse(src))`
preserves comments (AST→AST), but anything routed through IR loses
them. The doc-browser, formatter dump commands, and IR→AST `Convert`
all silently strip user-authored comments.

---

## 4. Walker / mutation issues

### 4.1 `StripForCompare` mutates input — only safe in tests, but exported in `ir/`

`ir/strip.go:7` is exported. Any non-test caller will permanently
trash an IR Package. Move into a `ir/strippkg_test` build, or make
it return a stripped copy.

### 4.2 `internal/lower/walk.go` only top-level — passes have to recurse manually inside Stmt/Expr

`internal/lower/walk.go:7-17`: `walkFuncs.stmts func([]ir.Stmt) []ir.Stmt` is given the whole slice; the pass must then walk into
each `Stmt` (e.g. `If.Body`, `For.Body`, `NodeInst.Children`) itself.
This is why every pass under `internal/lower/` reimplements its own
recursion (`lambda.go`, `reactivity.go`, `noref.go`, etc.). A single
recursive `ir.Walk` would prevent missing nested cases.

### 4.3 `walkPackage` doesn't visit `pkg.Outputs.Options` or `pkg.Contexts.Default` exprs

`internal/lower/walk.go:21-54`. Outputs / Contexts top-level
declarations are not traversed. If a future lowering needs to
rewrite a context default expression, it must duplicate the
traversal. `strip.go:90-95` does walk Outputs.Options — divergent
coverage.

### 4.4 IR cross-pointers stripped only by `StripForCompare`, not by lowering

`ir/strip.go:298,316,337` — `Ident.Sym`, `Call.Func`, `StructLit.Def`
are cleared for DeepEqual. But normal lowering does NOT clear these
when rewriting a `*Call` from a method dispatch to an intrinsic;
stale cross-pointers can survive. No real-bug-found but worth
auditing every lower pass that produces a `*Call`.

---

## 5. Other / cross-cutting

### 5.1 Naming inconsistency: `Decl` / `Def` / `Stmt`

- AST: `VarDecl`, `ConstDecl`, `EventDecl`, `Import`, but
  `FuncDef`, `StructDef`, `EnumDef`, `UnitDef`, `ComponentDecl`.
  Mixed `-Decl` and `-Def`.
- IR: `Func`, `Var`, `Component`, `Window`, `Timer`, `Import`,
  `Param` are bare. `StructDef`, `EnumDef`, `UnitDef`, `EventDecl`
  keep their AST suffix. `Prop` is bare. Mixed naming for the
  "same role" (named declaration).

Pick one (`Def` for "definition with a body / fields" / `Decl` for
"declaration only") and align.

### 5.2 `Color` enum lives in `ir/color.go` next to `Color` *type* literal usage in stdlib

`ir/color.go:1-26` defines `Color {Sync, Async, Param}` for function
sync/async tracking. `TypeColor` (`ir/types.go:25`) is the SNGL value
type for hex colors. Two unrelated concepts share the name "color".
Rename to `FuncColor` / `AsyncColor` / `Effect`.

### 5.3 `Purity` vs `Color` vs `IsAsync` vs `CanError` are four parallel effect dimensions on `Func`

`ir/ir.go:127-136`: `Purity, IsAsync, CanError`, plus `FuncSig.Color`
(`ir/types.go:447`). Four dimensions, four times to mis-set. They are
populated by different passes (`checker/purity.go`, `checker/effects.go`,
`checker/async.go`). A single `Effects` struct on `Func` would
consolidate.

### 5.4 `ZeroExpr` returns synthesized literals with no type-string conversion for `dyn`/`option`/`null`

`ir/defaults.go:5-60` returns `nil` for `TypeDyn` and constructs
literals for primitive types, but `TypeOption` returns
`&Literal{Type: TypNull, ...}` — typed-as-null even though the
target is option<T>. Codegen receiving this must remember to widen
or it'll emit `null` where it needs e.g. `Optional.empty()`.

### 5.5 `Intrinsics` defined as a slice, not a map

`ir/intrinsics.go:18-75`. Every backend does a linear scan to find
an intrinsic by name. Make it a `map[string]IntrinsicDef`.

### 5.7 `Window.Checked bool` — a transient flag in IR

`ir/ir.go:284` `Checked bool` — "true if body was already checked in
context (e.g., inside a for-loop)". This is checker-internal state
leaking into the IR data model. Move to a checker-side side-table.

### 5.8 `Synthesized bool` markers proliferate

`ir/expr.go:33` `Ident.Synthesized`, `ir/ir.go:139` `Func.Synthesized`,
`ir/ir.go:188` `Var.Synthesized`. Each says "set by lowering pass".
These are pass-tracking metadata that should live in pass output, not
on the IR itself. Codegen consumes them via JSON-tagged `-` so they
don't appear in dumps. Consider a `LoweredOrigin` enum
(`UserCode | SlotPass | ReactivityPass | ...`).
