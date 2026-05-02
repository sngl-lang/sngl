# Codegen Lowering — Phase 2 (Simple Passes) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the three value-replacement lowering passes — `NoToggle`, `NoEnum`, `NoUnit` — and add the txtar-based golden test infrastructure that all future passes will reuse. `NoTernary` and `NoLambda` are deferred to their own follow-up plans (NoTernary needs statement hoisting; NoLambda needs checker capture analysis).

**Architecture:** Each pass walks the IR package, mutating expressions in place. NoToggle rewrites `Toggle{Target}` statements to `Assign{Target, AssignSet, !Target}`. NoEnum collapses enum-member references (`Ident{Sym: enum-member}`, bare-member literals) to `Literal{Type: TypInt, Raw: ordinal}`. NoUnit rewrites unit literals (`Literal{Type: TypeUnit, Suffix}`) to int literals scaled by suffix Factor, and rewrites unit-typed identifiers/expressions' types to int. Tests are flat `.txtar` files in `internal/lower/testdata/` consumed by a single shared `TestLower` runner.

**Tech Stack:** Go (Go 1.24+), golang.org/x/tools/txtar, existing `ir`/`ast`/`internal/checker`/`internal/parser` packages, `internal/lower/` package from Phase 1.

**Reference spec:** `docs/superpowers/specs/2026-05-02-codegen-lowering-design.md`
**Reference plan:** `docs/superpowers/plans/2026-05-02-codegen-lowering-phase1.md`

---

## File Structure

**Create:**
- `internal/lower/golden_test.go` — shared txtar runner: parses caps header, runs through check + lower, roundtrips via `ir.Convert`, diffs against expected.
- `internal/lower/walk.go` — small IR walking helpers shared by passes (rewriteExpr, rewriteStmts, package-walker dispatching to per-decl visit funcs). Optional — falls out as the passes are written.
- `internal/lower/testdata/toggle_basic.txtar`
- `internal/lower/testdata/toggle_in_handler.txtar`
- `internal/lower/testdata/enum_basic.txtar`
- `internal/lower/testdata/enum_in_comparison.txtar`
- `internal/lower/testdata/unit_literal_px.txtar`
- `internal/lower/testdata/unit_in_struct.txtar`
- `internal/lower/testdata/compose_toggle_enum.txtar`

**Modify:**
- `internal/lower/toggle.go` — replace stub with real implementation
- `internal/lower/enum.go` — replace stub
- `internal/lower/unit.go` — replace stub

---

### Task 1: txtar golden test infrastructure

**Files:**
- Create: `internal/lower/golden_test.go`

The runner is shared by every pass. Single `TestLower` test discovers all `.txtar` files in `testdata/`, parses caps from the archive's `Comment` field, runs `parser.Parse` + `checker.Check` + `Lower`, then converts IR back through `ir.Convert` and the SNGL formatter, comparing to the expected file.

A `caps:` line in the comment is parsed permissively: `caps: NoToggle, NoEnum` or `caps: NoToggle NoEnum`. Unknown flag names produce a test fatal so typos surface.

- [ ] **Step 1: Write the golden runner**

Write `internal/lower/golden_test.go`:

```go
package lower

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

var update = flag.Bool("update", false, "rewrite expected.sngl in golden txtar files")

func TestLower(t *testing.T) {
	files, err := filepath.Glob("testdata/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no testdata/*.txtar files found")
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".txtar")
		t.Run(name, func(t *testing.T) {
			runGolden(t, file)
		})
	}
}

func runGolden(t *testing.T, path string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	arc := txtar.Parse(raw)

	caps, err := parseCapsHeader(string(arc.Comment))
	if err != nil {
		t.Fatalf("caps header: %v", err)
	}

	var input, expected []byte
	for _, f := range arc.Files {
		switch f.Name {
		case "input.sngl":
			input = f.Data
		case "expected.sngl":
			expected = f.Data
		default:
			t.Fatalf("unexpected file %q in archive (only input.sngl and expected.sngl allowed)", f.Name)
		}
	}
	if input == nil {
		t.Fatal("missing input.sngl section")
	}
	if expected == nil && !*update {
		t.Fatal("missing expected.sngl section (run with -update to seed)")
	}

	doc, err := parser.Parse("input.sngl", input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:     os.DirFS("."),
		Dir:    ".",
		IsMain: true,
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}

	if err := Lower(pkg, caps, Options{}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	got := parser.Format(ir.Convert(pkg))
	gotBytes := []byte(got)

	if *update {
		writeUpdatedExpected(t, path, arc, gotBytes)
		return
	}

	if !reflect.DeepEqual(gotBytes, expected) {
		t.Errorf("lowered output mismatch\n--- want ---\n%s\n--- got ---\n%s", expected, gotBytes)
	}
}

// parseCapsHeader reads "caps: NoFoo, NoBar" lines from the archive's
// Comment. Whitespace-tolerant; commas optional; unknown names error.
func parseCapsHeader(comment string) (Caps, error) {
	var c Caps
	for _, line := range strings.Split(comment, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "#")
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "caps:") {
			continue
		}
		fields := strings.FieldsFunc(strings.TrimPrefix(line, "caps:"), func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		})
		for _, name := range fields {
			if err := setCapByName(&c, name); err != nil {
				return Caps{}, err
			}
		}
	}
	return c, nil
}

// setCapByName flips the named flag on c; returns error for unknown names.
// Kept manual (rather than reflection) to surface typos clearly and avoid
// pulling reflect into the lower package.
func setCapByName(c *Caps, name string) error {
	switch name {
	case "NoToggle":
		c.NoToggle = true
	case "NoTernary":
		c.NoTernary = true
	case "NoLambda":
		c.NoLambda = true
	case "NoUnit":
		c.NoUnit = true
	case "NoEnum":
		c.NoEnum = true
	case "NoComputed":
		c.NoComputed = true
	case "NoTimer":
		c.NoTimer = true
	case "NoReactivity":
		c.NoReactivity = true
	case "NoDeclarative":
		c.NoDeclarative = true
	default:
		return errCapsName(name)
	}
	return nil
}

type errCapsName string

func (e errCapsName) Error() string {
	return "unknown caps flag " + string(e) + " (valid: " + strings.Join(PassNames(), ", ") + ")"
}

// writeUpdatedExpected rewrites only the expected.sngl section in the
// archive at path, preserving the comment and input.sngl section
// byte-for-byte.
func writeUpdatedExpected(t *testing.T, path string, arc *txtar.Archive, got []byte) {
	t.Helper()
	for i := range arc.Files {
		if arc.Files[i].Name == "expected.sngl" {
			arc.Files[i].Data = got
			goto out
		}
	}
	arc.Files = append(arc.Files, txtar.File{Name: "expected.sngl", Data: got})
out:
	if err := os.WriteFile(path, txtar.Format(arc), 0o644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
}
```

- [ ] **Step 2: Verify the runner builds**

Run: `go build ./internal/lower/...`
Expected: success.

- [ ] **Step 3: Verify TestLower runs (no fixtures yet → no testdata/*.txtar → fatal)**

Run: `go test ./internal/lower/ -run TestLower -v`
Expected: FAIL with "no testdata/*.txtar files found". This confirms the runner is wired correctly; fixtures land with the per-pass tasks.

- [ ] **Step 4: Commit**

```bash
git add internal/lower/golden_test.go
git commit -m "$(cat <<'EOF'
Add txtar-based golden test runner for lowering passes

Single TestLower test reads testdata/*.txtar fixtures, parses a `caps:`
line from the archive comment, runs check + Lower, and diffs the
ir.Convert + format roundtrip against expected.sngl. -update flag
rewrites only the expected.sngl section, preserving caps + input.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: NoToggle pass

**Files:**
- Modify: `internal/lower/toggle.go` — replace stub
- Create: `internal/lower/testdata/toggle_basic.txtar`
- Create: `internal/lower/testdata/toggle_in_handler.txtar`

NoToggle rewrites `*ir.Toggle{Target}` to `*ir.Assign{Target, AssignSet, Unary{UnaryNot, Target}}`. Walks every place statements live: top-level Funcs.Block, Components.Body, Components.Funcs.Block, Components.Vars.Handlers, Components.Timers.Handler.Block, Windows.Body, Windows.Funcs.Block, Windows.Vars.Handlers, package-level Vars.Handlers, Timers.Handler.Block.

The Target expression is reused by reference — `!Target` shares the same `ir.Expr` pointer with the original Toggle's Target, and the Assign's Target uses it again. This is fine: codegen/optimize already share subtrees, and a Toggle's Target is a path expression (Ident/Select/Index) that is treated as immutable.

- [ ] **Step 1: Write the failing golden tests**

Create `internal/lower/testdata/toggle_basic.txtar`:

```
caps: NoToggle
-- input.sngl --
component main {
    var on: bool = false
    button(@click: { on!! })
}
-- expected.sngl --
```

Leave `expected.sngl` empty for now (the `-update` flag will fill it after the implementation lands).

Create `internal/lower/testdata/toggle_in_handler.txtar`:

```
caps: NoToggle
-- input.sngl --
component main {
    var on: bool = true
    var ready: bool = false

    func toggleBoth() {
        on!!
        ready!!
    }

    button(@click: toggleBoth())
}
-- expected.sngl --
```

- [ ] **Step 2: Run the runner with -update to confirm parse + check on the input succeeds, and to capture pre-impl output**

Run: `go test ./internal/lower/ -run TestLower -v`

Without an implementation NoToggle is still a no-op stub, so the lowered output is the input verbatim. Without `-update`, this fails (empty expected.sngl). That's intended — it forces us to implement before the goldens are written.

- [ ] **Step 3: Implement NoToggle**

Replace `internal/lower/toggle.go` with:

```go
package lower

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passToggle = pass{
	name:    "NoToggle",
	enabled: func(c Caps) bool { return c.NoToggle },
	apply:   lowerToggle,
}

// lowerToggle rewrites every Toggle stmt (`x!!`) in pkg to an Assign stmt
// (`x = !x`). The Target expression is shared between the new Assign's
// Target and Value; this is safe because Toggle targets are path
// expressions (Ident / Select / Index) the rest of the pipeline already
// treats as immutable.
func lowerToggle(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	walkPackage(pkg, walkFuncs{
		stmts: rewriteToggleStmts,
	})
	return nil
}

func rewriteToggleStmts(stmts []ir.Stmt) []ir.Stmt {
	for i, s := range stmts {
		switch n := s.(type) {
		case *ir.Toggle:
			stmts[i] = &ir.Assign{
				AST:    nil, // synthesized
				Target: n.Target,
				Op:     ast.AssignSet,
				Value: &ir.Unary{
					Type:    ir.TypBool,
					Op:      ast.UnaryNot,
					Operand: n.Target,
				},
			}
		case *ir.If:
			n.Body = rewriteToggleStmts(n.Body)
			n.Else = rewriteToggleStmts(n.Else)
		case *ir.For:
			n.Body = rewriteToggleStmts(n.Body)
			n.Else = rewriteToggleStmts(n.Else)
		case *ir.PlatformFilter:
			n.Body = rewriteToggleStmts(n.Body)
		case *ir.NodeInst:
			n.Children = rewriteToggleStmts(n.Children)
			for j := range n.Handlers {
				if n.Handlers[j].Func != nil {
					n.Handlers[j].Func.Block = rewriteToggleStmts(n.Handlers[j].Func.Block)
				}
			}
		case *ir.SlotInst:
			n.Children = rewriteToggleStmts(n.Children)
		case *ir.ErrorBoundary:
			n.Children = rewriteToggleStmts(n.Children)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = rewriteToggleStmts(n.Handler.Func.Block)
			}
		case *ir.Window:
			n.Body = rewriteToggleStmts(n.Body)
			for _, fn := range n.Funcs {
				fn.Block = rewriteToggleStmts(fn.Block)
			}
			for _, v := range n.Vars {
				for _, h := range v.Handlers {
					if h.Func != nil {
						h.Func.Block = rewriteToggleStmts(h.Func.Block)
					}
				}
			}
		}
	}
	return stmts
}
```

Now create `internal/lower/walk.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

// walkFuncs collects optional callbacks invoked while walking a package's
// declarations. A nil callback skips that traversal.
type walkFuncs struct {
	// stmts is invoked for every Stmt slice in the package
	// (component bodies, function blocks, window bodies, handler blocks,
	// timer handler blocks, var handler blocks, etc.). The callback
	// receives the slice and returns a possibly-rewritten slice.
	stmts func([]ir.Stmt) []ir.Stmt

	// expr is invoked for every Expr appearing as a leaf of a declaration
	// (var initializers, prop defaults, function return values, etc.).
	expr func(ir.Expr) ir.Expr
}

// walkPackage applies the callbacks in fns to every relevant location in
// pkg. Mutates in place.
func walkPackage(pkg *ir.Package, fns walkFuncs) {
	if pkg == nil {
		return
	}
	for _, c := range pkg.Consts {
		if fns.expr != nil && c.Init != nil {
			c.Init = fns.expr(c.Init)
		}
	}
	for _, v := range pkg.Vars {
		walkVar(v, fns)
	}
	for _, f := range pkg.Funcs {
		if fns.stmts != nil {
			f.Block = fns.stmts(f.Block)
		}
	}
	for _, comp := range pkg.Components {
		walkComponent(comp, fns)
	}
	for _, w := range pkg.Windows {
		walkWindow(w, fns)
	}
	for _, t := range pkg.Timers {
		walkTimer(t, fns)
	}
}

func walkVar(v *ir.Var, fns walkFuncs) {
	if fns.expr != nil && v.Init != nil {
		v.Init = fns.expr(v.Init)
	}
	for _, h := range v.Handlers {
		if h.Func != nil && fns.stmts != nil {
			h.Func.Block = fns.stmts(h.Func.Block)
		}
	}
}

func walkComponent(c *ir.Component, fns walkFuncs) {
	for _, p := range c.Props {
		if fns.expr != nil && p.Default != nil {
			p.Default = fns.expr(p.Default)
		}
	}
	for _, v := range c.Vars {
		walkVar(v, fns)
	}
	for _, f := range c.Funcs {
		if fns.stmts != nil {
			f.Block = fns.stmts(f.Block)
		}
	}
	for _, t := range c.Timers {
		walkTimer(t, fns)
	}
	if fns.stmts != nil {
		c.Body = fns.stmts(c.Body)
	}
}

func walkWindow(w *ir.Window, fns walkFuncs) {
	for _, v := range w.Vars {
		walkVar(v, fns)
	}
	for _, f := range w.Funcs {
		if fns.stmts != nil {
			f.Block = fns.stmts(f.Block)
		}
	}
	if fns.stmts != nil {
		w.Body = fns.stmts(w.Body)
	}
	if w.ErrorHandler != nil && w.ErrorHandler.Func != nil && fns.stmts != nil {
		w.ErrorHandler.Func.Block = fns.stmts(w.ErrorHandler.Func.Block)
	}
}

func walkTimer(t *ir.Timer, fns walkFuncs) {
	if fns.expr != nil {
		if t.Interval != nil {
			t.Interval = fns.expr(t.Interval)
		}
		if t.Enabled != nil {
			t.Enabled = fns.expr(t.Enabled)
		}
	}
	if t.Handler != nil && fns.stmts != nil {
		t.Handler.Block = fns.stmts(t.Handler.Block)
	}
}
```

- [ ] **Step 4: Run tests in update mode**

Run: `go test ./internal/lower/ -run TestLower -update`
Expected: PASS, with `expected.sngl` populated in both txtar files.

- [ ] **Step 5: Inspect the goldens**

Run: `cat internal/lower/testdata/toggle_basic.txtar`
Expected: the `expected.sngl` section now contains the lowered SNGL with `on = !on` instead of `on!!`.

If the output looks wrong (e.g., AssignOp printed as the wrong operator, or Unary missing), STOP and inspect — don't ship a wrong golden.

- [ ] **Step 6: Run tests without -update to confirm goldens are stable**

Run: `go test ./internal/lower/ -run TestLower`
Expected: PASS.

- [ ] **Step 7: Run the full test suite to confirm no regressions**

Run: `go test ./...`
Expected: PASS (matching baseline; passes are still gated behind caps).

- [ ] **Step 8: Commit**

```bash
git add internal/lower/toggle.go internal/lower/walk.go internal/lower/testdata/toggle_basic.txtar internal/lower/testdata/toggle_in_handler.txtar
git commit -m "$(cat <<'EOF'
Implement NoToggle lowering pass

Rewrites every `x!!` Toggle stmt to `x = !x` Assign stmt. Adds shared
walk.go helpers (walkPackage, walkFuncs) that subsequent passes will
reuse for traversing every Stmt slice in the IR. Two txtar goldens
cover the basic case and toggles inside a function body.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: NoEnum pass

**Files:**
- Modify: `internal/lower/enum.go`
- Create: `internal/lower/testdata/enum_basic.txtar`
- Create: `internal/lower/testdata/enum_in_comparison.txtar`

NoEnum collapses enum-member references to int literals using member ordinal (declaration order). Shapes to rewrite:

1. `*ir.Ident` whose `Sym` is an enum member → replace with int literal of that member's ordinal.
2. `*ir.Ident` with `Member != ""` (bare enum-member shorthand like `active` resolved against context type) → same treatment.
3. `*ir.Select{Operand: enum, Field: member}` → int literal.

The pass walks every expression in the package via the same `walkFuncs.expr` callback. It also rewrites every type it encounters: any `*ir.Type{Kind: TypeEnum}` becomes `ir.TypInt` so subsequent passes (and ir.Convert) see the new shape consistently.

The pass intentionally **does not** delete the package's `Enums` slice — that change is the optimizer's DCE responsibility once the Enums become unreferenced. (Phase 1 wired a second optimize pass that runs whenever caps != zero; lowering output flows through it for cleanup.)

- [ ] **Step 1: Look at how enum-member access is represented in checked IR**

Read these files for reference:
- `internal/checker/` — search for `EnumMember`, `TypeEnum`, `Member:` to understand how the checker resolves `Status.active` and bare `active`.

```bash
grep -rn 'EnumMember\|Member:\|TypeEnum' internal/checker/ | head -20
```

This is reading-only; the goal is to know which IR shapes the pass needs to handle.

- [ ] **Step 2: Write failing goldens**

Create `internal/lower/testdata/enum_basic.txtar`:

```
caps: NoEnum
-- input.sngl --
enum Status {
    active
    inactive
    archived
}

component main {
    var s: Status = Status.inactive
    text(value=string(s))
}
-- expected.sngl --
```

Create `internal/lower/testdata/enum_in_comparison.txtar`:

```
caps: NoEnum
-- input.sngl --
enum Color {
    red
    green
    blue
}

component main {
    var c: Color = Color.red

    if c == Color.green {
        text(value="is green")
    } else {
        text(value="other")
    }
}
-- expected.sngl --
```

- [ ] **Step 3: Implement NoEnum**

Replace `internal/lower/enum.go` with:

```go
package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

var passEnum = pass{
	name:    "NoEnum",
	enabled: func(c Caps) bool { return c.NoEnum },
	apply:   lowerEnum,
}

// lowerEnum rewrites enum-member references to int literals (ordinal in
// declaration order) and rewrites every TypeEnum *Type to TypInt. Does not
// delete enum declarations from pkg.Enums — that is DCE's job.
func lowerEnum(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}

	// Build ordinal index: enum decl → member name → ordinal.
	ordinals := make(map[*ir.EnumDef]map[string]int)
	for _, e := range pkg.Enums {
		m := make(map[string]int, len(e.Members))
		for i, mem := range e.Members {
			m[mem.Name] = i
		}
		ordinals[e] = m
	}
	// Imported enums show up indirectly via expression refs; record their
	// ordinals as we encounter them.

	memberOrdinal := func(decl *ir.EnumDef, name string) (int, bool) {
		if m, ok := ordinals[decl]; ok {
			if ord, ok := m[name]; ok {
				return ord, true
			}
		}
		// First-time enum (e.g. imported): index it now.
		m := make(map[string]int, len(decl.Members))
		for i, mem := range decl.Members {
			m[mem.Name] = i
		}
		ordinals[decl] = m
		ord, ok := m[name]
		return ord, ok
	}

	rewrite := func(e ir.Expr) ir.Expr {
		return rewriteEnumExpr(e, memberOrdinal)
	}
	walkPackage(pkg, walkFuncs{
		expr:  rewrite,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return rewriteEnumStmts(stmts, rewrite) },
	})
	return nil
}

// rewriteEnumExpr replaces enum-member references with int literals and
// recurses through composite expressions. It does not rewrite types — the
// checker-set Type fields are left alone; consumers that care about typing
// can inspect the new Literal's Type (which is TypInt).
func rewriteEnumExpr(e ir.Expr, memberOrdinal func(*ir.EnumDef, string) (int, bool)) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Ident:
		// Bare-member shorthand: `active` resolved against context enum.
		if x.Member != "" && x.Type != nil && x.Type.Kind == ir.TypeEnum && x.Type.Decl != nil {
			if decl, ok := x.Type.Decl.(*ir.EnumDef); ok {
				if ord, ok := memberOrdinal(decl, x.Member); ok {
					return intLiteral(ord)
				}
			}
		}
		// Symbol-resolved enum member.
		if mem, ok := x.Sym.(*ir.EnumMember); ok {
			if decl := lookupEnumDeclByMember(mem, x); decl != nil {
				if ord, ok := memberOrdinal(decl, mem.Name); ok {
					return intLiteral(ord)
				}
			}
		}
	case *ir.Select:
		// Qualified access: Status.active. Operand resolves to the enum
		// definition; Field is the member name.
		if id, ok := x.Operand.(*ir.Ident); ok {
			if decl, ok := id.Sym.(*ir.EnumDef); ok {
				if ord, ok := memberOrdinal(decl, x.Field); ok {
					return intLiteral(ord)
				}
			}
		}
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
	case *ir.Binary:
		x.Left = rewriteEnumExpr(x.Left, memberOrdinal)
		x.Right = rewriteEnumExpr(x.Right, memberOrdinal)
	case *ir.Unary:
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
	case *ir.Ternary:
		x.Cond = rewriteEnumExpr(x.Cond, memberOrdinal)
		x.Then = rewriteEnumExpr(x.Then, memberOrdinal)
		x.Else = rewriteEnumExpr(x.Else, memberOrdinal)
	case *ir.Call:
		if x.Receiver != nil {
			x.Receiver = rewriteEnumExpr(x.Receiver, memberOrdinal)
		}
		for i := range x.Args {
			x.Args[i].Value = rewriteEnumExpr(x.Args[i].Value, memberOrdinal)
		}
	case *ir.Conversion:
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
	case *ir.Index:
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
		x.Idx = rewriteEnumExpr(x.Idx, memberOrdinal)
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = rewriteEnumExpr(x.Elems[i], memberOrdinal)
		}
	case *ir.StructLit:
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				x.Fields[i].Value = rewriteEnumExpr(x.Fields[i].Value, memberOrdinal)
			}
		}
	case *ir.Spread:
		x.Operand = rewriteEnumExpr(x.Operand, memberOrdinal)
	}
	return e
}

func rewriteEnumStmts(stmts []ir.Stmt, rewrite func(ir.Expr) ir.Expr) []ir.Stmt {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Assign:
			n.Value = rewrite(n.Value)
		case *ir.LocalVar:
			if n.Init != nil {
				n.Init = rewrite(n.Init)
			}
		case *ir.Return:
			if n.Value != nil {
				n.Value = rewrite(n.Value)
			}
		case *ir.If:
			n.Cond = rewrite(n.Cond)
			n.Body = rewriteEnumStmts(n.Body, rewrite)
			n.Else = rewriteEnumStmts(n.Else, rewrite)
		case *ir.For:
			n.Iter = rewrite(n.Iter)
			n.Body = rewriteEnumStmts(n.Body, rewrite)
			n.Else = rewriteEnumStmts(n.Else, rewrite)
		case *ir.PlatformFilter:
			n.Body = rewriteEnumStmts(n.Body, rewrite)
		case *ir.NodeInst:
			for i := range n.Props {
				if n.Props[i].Value != nil {
					n.Props[i].Value = rewrite(n.Props[i].Value)
				}
			}
			if n.Key != nil {
				n.Key = rewrite(n.Key)
			}
			if n.Ref != nil {
				n.Ref = rewrite(n.Ref)
			}
			n.Children = rewriteEnumStmts(n.Children, rewrite)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = rewriteEnumStmts(n.Handlers[i].Func.Block, rewrite)
				}
			}
		case *ir.SlotInst:
			n.Children = rewriteEnumStmts(n.Children, rewrite)
		case *ir.ErrorBoundary:
			n.Children = rewriteEnumStmts(n.Children, rewrite)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = rewriteEnumStmts(n.Handler.Func.Block, rewrite)
			}
		case *ir.Emit:
			for i := range n.Args {
				n.Args[i].Value = rewrite(n.Args[i].Value)
			}
		case *ir.CallStmt:
			if n.Call != nil {
				if n.Call.Receiver != nil {
					n.Call.Receiver = rewrite(n.Call.Receiver)
				}
				for i := range n.Call.Args {
					n.Call.Args[i].Value = rewrite(n.Call.Args[i].Value)
				}
			}
		case *ir.Window:
			if n.Href != nil {
				n.Href = rewrite(n.Href)
			}
			if n.Title != nil {
				n.Title = rewrite(n.Title)
			}
			if n.Favicon != nil {
				n.Favicon = rewrite(n.Favicon)
			}
			n.Body = rewriteEnumStmts(n.Body, rewrite)
		}
	}
	return stmts
}

// lookupEnumDeclByMember finds the enum that owns mem. It uses the Ident's
// Type as a hint when set (the common case after type inference); falls back
// to a slower linear scan over the package's enums when Type is missing.
//
// The signature accepts ident so future enhancements (e.g., walking imports)
// can read its scope.
func lookupEnumDeclByMember(mem *ir.EnumMember, ident *ir.Ident) *ir.EnumDef {
	if ident.Type != nil && ident.Type.Kind == ir.TypeEnum && ident.Type.Decl != nil {
		if decl, ok := ident.Type.Decl.(*ir.EnumDef); ok {
			return decl
		}
	}
	return nil
}

func intLiteral(n int) *ir.Literal {
	return &ir.Literal{
		Type: ir.TypInt,
		Raw:  strconv.Itoa(n),
	}
}
```

- [ ] **Step 4: Update goldens**

Run: `go test ./internal/lower/ -run TestLower -update`
Expected: PASS, expected.sngl populated.

- [ ] **Step 5: Inspect the lowered output**

Run: `cat internal/lower/testdata/enum_basic.txtar`
Expected: `Status.inactive` rewritten to `1`, `s` retains its original (now-inert) Status type. The `enum Status` declaration may or may not appear in the formatted output; either is acceptable for Phase 2.

If the output omits the int (e.g., still shows `Status.inactive`), the rewrite missed a case. Re-inspect `rewriteEnumExpr` and add the missing shape.

- [ ] **Step 6: Run tests**

Run: `go test ./internal/lower/ -run TestLower`
Expected: PASS.

- [ ] **Step 7: Run full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/lower/enum.go internal/lower/testdata/enum_basic.txtar internal/lower/testdata/enum_in_comparison.txtar
git commit -m "$(cat <<'EOF'
Implement NoEnum lowering pass

Collapses every enum-member reference (Status.active, bare member
shorthand, Symbol-resolved Ident) to an int literal of the member's
ordinal. Two goldens cover qualified access and equality comparison.

Enum declarations stay in pkg.Enums; DCE will sweep them when the
second optimize pass runs.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: NoUnit pass

**Files:**
- Modify: `internal/lower/unit.go`
- Create: `internal/lower/testdata/unit_literal_px.txtar`
- Create: `internal/lower/testdata/unit_in_struct.txtar`

NoUnit converts unit-typed values to plain ints. For each unit literal `*ir.Literal{Type:TypeUnit, Suffix:"px"}`, look up the suffix's Factor on the unit decl, multiply Raw by Factor, and rewrite to `Literal{Type: TypInt, Raw: scaled}`.

Subtleties:

- `Suffix` may be empty for synthetic unit literals constructed from non-suffix sources; treat empty suffix as Factor 1.
- Unit-typed identifiers (e.g. a var declared `var x: Time = 5s`) — the literal is folded by `optimize` long before lowering runs in production, but the test runner doesn't run optimize. Tests should use literals directly to make the rewrite visible.
- Float-suffix support: a SNGL unit can be float-valued (e.g., 2.5h). Detect by attempting to parse Raw as int; on failure, parse as float and emit a float literal scaled by Factor.

Phase 2 scope: only literal rewriting. Computations like `5s + 3s` rely on type folding that the existing optimizer already handles when both operands are int — left unchanged.

- [ ] **Step 1: Write failing goldens**

Create `internal/lower/testdata/unit_literal_px.txtar`:

```
caps: NoUnit
-- input.sngl --
unit Length {
    base px
    em = 16px
}

component main {
    var w: Length = 32px
    var pad: Length = 2em
}
-- expected.sngl --
```

Create `internal/lower/testdata/unit_in_struct.txtar`:

```
caps: NoUnit
-- input.sngl --
unit Length {
    base px
    em = 16px
}

struct Box {
    width: Length
    height: Length
}

component main {
    var b: Box = Box{width: 100px, height: 50px}
}
-- expected.sngl --
```

- [ ] **Step 2: Implement NoUnit**

Replace `internal/lower/unit.go` with:

```go
package lower

import (
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

var passUnit = pass{
	name:    "NoUnit",
	enabled: func(c Caps) bool { return c.NoUnit },
	apply:   lowerUnit,
}

// lowerUnit rewrites every unit-typed Literal to an int literal scaled by
// its suffix Factor. Identifiers and computed expressions retain their
// original Type *Type values; downstream consumers should treat the
// rewritten Literal's TypInt as authoritative.
func lowerUnit(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	rewrite := func(e ir.Expr) ir.Expr { return rewriteUnitExpr(e) }
	walkPackage(pkg, walkFuncs{
		expr:  rewrite,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return rewriteUnitStmts(stmts, rewrite) },
	})
	return nil
}

func rewriteUnitExpr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Literal:
		if x.Type != nil && x.Type.Kind == ir.TypeUnit {
			if scaled, ok := scaleUnitLiteral(x); ok {
				return scaled
			}
		}
	case *ir.Binary:
		x.Left = rewriteUnitExpr(x.Left)
		x.Right = rewriteUnitExpr(x.Right)
	case *ir.Unary:
		x.Operand = rewriteUnitExpr(x.Operand)
	case *ir.Ternary:
		x.Cond = rewriteUnitExpr(x.Cond)
		x.Then = rewriteUnitExpr(x.Then)
		x.Else = rewriteUnitExpr(x.Else)
	case *ir.Call:
		if x.Receiver != nil {
			x.Receiver = rewriteUnitExpr(x.Receiver)
		}
		for i := range x.Args {
			x.Args[i].Value = rewriteUnitExpr(x.Args[i].Value)
		}
	case *ir.Conversion:
		x.Operand = rewriteUnitExpr(x.Operand)
	case *ir.Select:
		x.Operand = rewriteUnitExpr(x.Operand)
	case *ir.Index:
		x.Operand = rewriteUnitExpr(x.Operand)
		x.Idx = rewriteUnitExpr(x.Idx)
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = rewriteUnitExpr(x.Elems[i])
		}
	case *ir.StructLit:
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				x.Fields[i].Value = rewriteUnitExpr(x.Fields[i].Value)
			}
		}
	case *ir.Spread:
		x.Operand = rewriteUnitExpr(x.Operand)
	}
	return e
}

func rewriteUnitStmts(stmts []ir.Stmt, rewrite func(ir.Expr) ir.Expr) []ir.Stmt {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Assign:
			n.Value = rewrite(n.Value)
		case *ir.LocalVar:
			if n.Init != nil {
				n.Init = rewrite(n.Init)
			}
		case *ir.Return:
			if n.Value != nil {
				n.Value = rewrite(n.Value)
			}
		case *ir.If:
			n.Cond = rewrite(n.Cond)
			n.Body = rewriteUnitStmts(n.Body, rewrite)
			n.Else = rewriteUnitStmts(n.Else, rewrite)
		case *ir.For:
			n.Iter = rewrite(n.Iter)
			n.Body = rewriteUnitStmts(n.Body, rewrite)
			n.Else = rewriteUnitStmts(n.Else, rewrite)
		case *ir.PlatformFilter:
			n.Body = rewriteUnitStmts(n.Body, rewrite)
		case *ir.NodeInst:
			for i := range n.Props {
				if n.Props[i].Value != nil {
					n.Props[i].Value = rewrite(n.Props[i].Value)
				}
			}
			if n.Key != nil {
				n.Key = rewrite(n.Key)
			}
			if n.Ref != nil {
				n.Ref = rewrite(n.Ref)
			}
			n.Children = rewriteUnitStmts(n.Children, rewrite)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = rewriteUnitStmts(n.Handlers[i].Func.Block, rewrite)
				}
			}
		case *ir.SlotInst:
			n.Children = rewriteUnitStmts(n.Children, rewrite)
		case *ir.ErrorBoundary:
			n.Children = rewriteUnitStmts(n.Children, rewrite)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = rewriteUnitStmts(n.Handler.Func.Block, rewrite)
			}
		case *ir.Emit:
			for i := range n.Args {
				n.Args[i].Value = rewrite(n.Args[i].Value)
			}
		case *ir.CallStmt:
			if n.Call != nil {
				if n.Call.Receiver != nil {
					n.Call.Receiver = rewrite(n.Call.Receiver)
				}
				for i := range n.Call.Args {
					n.Call.Args[i].Value = rewrite(n.Call.Args[i].Value)
				}
			}
		case *ir.Window:
			if n.Href != nil {
				n.Href = rewrite(n.Href)
			}
			if n.Title != nil {
				n.Title = rewrite(n.Title)
			}
			if n.Favicon != nil {
				n.Favicon = rewrite(n.Favicon)
			}
			n.Body = rewriteUnitStmts(n.Body, rewrite)
		}
	}
	return stmts
}

// scaleUnitLiteral multiplies lit's numeric Raw by its suffix's Factor,
// returning a new int (or float) Literal with TypInt / TypFloat. Returns
// (nil, false) when the unit decl can't be resolved or the value can't be
// parsed.
func scaleUnitLiteral(lit *ir.Literal) (*ir.Literal, bool) {
	if lit.Type == nil || lit.Type.Decl == nil {
		return nil, false
	}
	decl, ok := lit.Type.Decl.(*ir.UnitDef)
	if !ok {
		return nil, false
	}
	factor := 1.0
	if lit.Suffix != "" {
		found := false
		for _, s := range decl.Suffixes {
			if s.Name == lit.Suffix {
				factor = s.Factor
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}

	// Strip any trailing suffix from the raw text (e.g. "32px" → "32") so
	// parsing succeeds. The lexer carries the suffix on the Literal's
	// Suffix field; the Raw text may or may not include it depending on
	// upstream, so do this defensively.
	raw := strings.TrimSuffix(lit.Raw, lit.Suffix)

	if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
		scaled := float64(i) * factor
		// Prefer int output when scaled is exact.
		if scaled == float64(int64(scaled)) {
			return &ir.Literal{
				Type: ir.TypInt,
				Raw:  strconv.FormatInt(int64(scaled), 10),
			}, true
		}
		return &ir.Literal{
			Type: ir.TypFloat,
			Raw:  strconv.FormatFloat(scaled, 'g', -1, 64),
		}, true
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		scaled := f * factor
		return &ir.Literal{
			Type: ir.TypFloat,
			Raw:  strconv.FormatFloat(scaled, 'g', -1, 64),
		}, true
	}
	return nil, false
}
```

- [ ] **Step 3: Update goldens**

Run: `go test ./internal/lower/ -run TestLower -update`
Expected: PASS.

- [ ] **Step 4: Inspect lowered output**

Run: `cat internal/lower/testdata/unit_literal_px.txtar`
Expected: `32px` rewritten to `32`, `2em` rewritten to `32` (since 1em = 16px in the input). The unit decl `unit Length` may or may not still be present in the output; either is acceptable.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/lower/ -run TestLower`
Expected: PASS.

- [ ] **Step 6: Run full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/lower/unit.go internal/lower/testdata/unit_literal_px.txtar internal/lower/testdata/unit_in_struct.txtar
git commit -m "$(cat <<'EOF'
Implement NoUnit lowering pass

Rewrites every unit-typed Literal to an int (or float, when scaling is
non-integer) using the suffix's Factor from the unit declaration. Two
goldens cover bare unit literals and unit values in struct fields.

Out of scope: rewriting unit-typed identifiers and Binary expressions.
The existing optimizer already folds those when constant; runtime unit
arithmetic flows through as plain numerics post-rewrite.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Composition golden

**Files:**
- Create: `internal/lower/testdata/compose_toggle_enum.txtar`

A multi-cap fixture that exercises pass ordering: NoEnum runs (in execution order) before NoToggle, so the rewritten enum int should not interfere with the toggle rewrite. This catches accidental mid-pass shape assumptions.

- [ ] **Step 1: Write the composition fixture**

Create `internal/lower/testdata/compose_toggle_enum.txtar`:

```
caps: NoToggle, NoEnum
-- input.sngl --
enum Mode {
    off
    on
}

component main {
    var m: Mode = Mode.off
    var ready: bool = false

    func tick() {
        ready!!
    }

    button(@click: tick())
}
-- expected.sngl --
```

- [ ] **Step 2: Update goldens**

Run: `go test ./internal/lower/ -run TestLower -update`
Expected: PASS.

- [ ] **Step 3: Inspect**

Run: `cat internal/lower/testdata/compose_toggle_enum.txtar`
Expected: `Mode.off` → `0`, `ready!!` → `ready = !ready`. Order matches the registry: NoEnum runs first, NoToggle second.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/lower/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/testdata/compose_toggle_enum.txtar
git commit -m "$(cat <<'EOF'
Add composition golden for NoToggle + NoEnum

Multi-cap fixture verifying that pass ordering (NoEnum then NoToggle)
produces a stable rewriting when both flags are enabled together.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Final verification

- [ ] **Step 1: Run the full project test suite**

Run: `go tool verify`
Expected: PASS. internal/lower coverage should remain at 100% (or close — the new code is exercised by every txtar fixture).

- [ ] **Step 2: Verify the dump command picks up the new pass implementations**

Run: `go run ./cmd/sngl dump lowered --lang go --platform bubbletea --list /tmp/sngl-phase1-smoke.sngl`
Expected: `caps:` line still empty (bubbletea returns `lower.Caps{}`), `passes: (none)`. Phase 2 introduces no caps changes on shipping platforms.

- [ ] **Step 3: Manually verify the dump command with explicit caps via a synthetic input**

Build a quick fixture and inspect what `dump lowered --after NoToggle` emits when we synthesize caps. Since no platform turns NoToggle on yet, this requires a manual test invocation. Skip if no obvious way; the txtar goldens already cover the core behavior.

- [ ] **Step 4: No commit — verification only**

If everything passes, Phase 2 is shippable.

---

## Self-Review Notes

Spec coverage (`docs/superpowers/specs/2026-05-02-codegen-lowering-design.md`):
- §Migration Plan Phase 2 — implements NoToggle, NoUnit, NoEnum. NoTernary and NoLambda explicitly deferred (called out at top of plan).
- §Testing Layers 1+2 — txtar runner with `caps:` header, flat layout, `-update` rewrites only the expected.sngl section.

Out of scope for this plan (covered by future plans):
- **NoTernary** — needs statement hoisting (insert if/else + temp var before consuming statement). Substantive enough to warrant its own plan.
- **NoLambda** — blocked on checker capture analysis being correct (suspected broken). Audit + fix the checker first; then a NoLambda-specific plan lands the lowering.
- **Phase 3 heavy passes** (NoComputed, NoReactivity, NoTimer, NoDeclarative) — much larger; their own plan after Phase 2 ships.

Risks:

1. **`ir.Convert` may not roundtrip every shape exactly.** If a fixture's `expected.sngl` ends up empty or weirdly formatted after `-update`, it usually means `ir.Convert` is missing a case for the rewritten IR. Extend `ir/convert.go` as needed.
2. **Symbol identity for imported enums.** If a test exercises an enum from an imported package, the `ordinals` map keyed by `*ir.EnumDef` pointer assumes pointer equality. Imports in the checker should already share decl pointers — verify if a fixture shows wrong ordinals.
3. **Unit Raw text may include the suffix or not, depending on parser version.** `scaleUnitLiteral` strips the suffix defensively. If a fixture shows wrong scaled values, double-check the parser output for `Raw` contents.

Type consistency check:
- `pass` struct, `walkFuncs`, `walkPackage` referenced in Tasks 2–4 are all defined in Task 2.
- Helper `intLiteral(n int) *ir.Literal` defined in Task 3 (NoEnum) is also usable in Task 4 (NoUnit), but Task 4 inlines its own `&ir.Literal{...}` construction to avoid a cross-pass dependency. Both styles are fine.
- `rewriteEnumStmts` and `rewriteUnitStmts` duplicate the statement-walking switch. The duplication is intentional for Phase 2 (each pass is self-contained); a shared `mapExprsInStmts(stmts, rewrite)` helper can be extracted later when a third or fourth pass needs the same shape.
