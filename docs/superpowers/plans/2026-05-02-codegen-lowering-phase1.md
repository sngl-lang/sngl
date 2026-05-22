# Codegen Lowering — Phase 1 (Scaffolding) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the `internal/lower` package with all 9 pass stubs, add `Capabilities()` to `PlatformGenerator` and `LangTranslator` interfaces, wire `lower.Lower` into the compile pipeline between two optimize passes, and add a `sngl dump lowered` subcommand. Net behavior change is zero — every existing platform/language returns `lower.Caps{}` so no pass actually runs.

**Architecture:** New package `internal/lower` exposes `Caps` (struct of `No*` bools), a fixed-order pass registry, and a `Lower(pkg, caps, opts)` entry point that mutates `*ir.Package` in place. Pipeline becomes `parse → check → optimize → lower → optimize → codegen`. Capabilities are merged from both `PlatformGenerator.Capabilities()` and `LangTranslator.Capabilities()` at compile time.

**Tech Stack:** Go (Go 1.24+), spf13/cobra, golang.org/x/tools/txtar (already vendored via test deps), existing `ir`/`ast`/`codegen`/`internal/optimize`/`internal/parser`/`internal/checker` packages.

**Reference spec:** `docs/superpowers/specs/2026-05-02-codegen-lowering-design.md`

---

## File Structure

**Create:**
- `internal/lower/caps.go` — `Caps` struct, `Merge`, `String`
- `internal/lower/caps_test.go` — table tests for Merge + String
- `internal/lower/lower.go` — `pass` struct, `passes` ordered slice, `Options`, `Lower` entry point
- `internal/lower/lower_test.go` — tests for `Lower` ordering, `StopAfter`, error on unknown pass name
- `internal/lower/toggle.go` — `passToggle` stub (no-op)
- `internal/lower/ternary.go` — `passTernary` stub
- `internal/lower/lambda.go` — `passLambda` stub
- `internal/lower/unit.go` — `passUnit` stub
- `internal/lower/enum.go` — `passEnum` stub
- `internal/lower/computed.go` — `passComputed` stub
- `internal/lower/reactivity.go` — `passReactivity` stub
- `internal/lower/timer.go` — `passTimer` stub
- `internal/lower/declarative.go` — `passDeclarative` stub

**Modify:**
- `codegen/codegen.go` — add `Capabilities() lower.Caps` to both interfaces
- `codegen/lang/golang/golang.go`, `codegen/lang/javascript/javascript.go`, `codegen/lang/kotlin/kotlin.go`, `codegen/lang/none/none.go` — add zero-cap `Capabilities()` method
- `codegen/platform/android/android.go`, `codegen/platform/bubbletea/bubbletea.go`, `codegen/platform/fyne/fyne.go`, `codegen/platform/html/html.go`, `codegen/platform/none/none.go` — add zero-cap `Capabilities()` method
- `sngl.go` — add `Lower(pkg, caps)` public function
- `cmd/sngl/compile.go` — call `lower.Lower` and second `optimize.Optimize` between current optimize and codegen
- `cmd/sngl/dump.go` — add `dumpLoweredCmd` with `--after` and `--list` flags

---

### Task 1: Caps struct, Merge, String

**Files:**
- Create: `internal/lower/caps.go`
- Create: `internal/lower/caps_test.go`

- [ ] **Step 1: Write the failing tests**

Write `internal/lower/caps_test.go`:

```go
package lower

import (
	"testing"
)

func TestCaps_Merge(t *testing.T) {
	tests := []struct {
		name string
		a, b Caps
		want Caps
	}{
		{
			name: "both zero",
			a:    Caps{},
			b:    Caps{},
			want: Caps{},
		},
		{
			name: "left enables NoToggle",
			a:    Caps{NoToggle: true},
			b:    Caps{},
			want: Caps{NoToggle: true},
		},
		{
			name: "right enables NoToggle",
			a:    Caps{},
			b:    Caps{NoToggle: true},
			want: Caps{NoToggle: true},
		},
		{
			name: "both enable NoReactivity",
			a:    Caps{NoReactivity: true},
			b:    Caps{NoReactivity: true},
			want: Caps{NoReactivity: true},
		},
		{
			name: "different flags from each side OR together",
			a:    Caps{NoToggle: true, NoTernary: true},
			b:    Caps{NoEnum: true, NoUnit: true},
			want: Caps{NoToggle: true, NoTernary: true, NoEnum: true, NoUnit: true},
		},
		{
			name: "all flags",
			a:    Caps{NoToggle: true, NoTernary: true, NoLambda: true, NoUnit: true, NoEnum: true},
			b:    Caps{NoComputed: true, NoTimer: true, NoReactivity: true, NoDeclarative: true},
			want: Caps{NoToggle: true, NoTernary: true, NoLambda: true, NoUnit: true, NoEnum: true, NoComputed: true, NoTimer: true, NoReactivity: true, NoDeclarative: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Merge(tt.b); got != tt.want {
				t.Errorf("Merge(%+v, %+v) = %+v; want %+v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCaps_String(t *testing.T) {
	tests := []struct {
		name string
		c    Caps
		want string
	}{
		{name: "empty", c: Caps{}, want: ""},
		{name: "single", c: Caps{NoToggle: true}, want: "NoToggle"},
		{name: "multiple sorted by declaration order", c: Caps{NoToggle: true, NoReactivity: true, NoEnum: true}, want: "NoEnum,NoToggle,NoReactivity"},
		{name: "all", c: Caps{NoToggle: true, NoTernary: true, NoLambda: true, NoUnit: true, NoEnum: true, NoComputed: true, NoTimer: true, NoReactivity: true, NoDeclarative: true}, want: "NoUnit,NoEnum,NoTernary,NoComputed,NoLambda,NoToggle,NoReactivity,NoTimer,NoDeclarative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.String(); got != tt.want {
				t.Errorf("String() = %q; want %q", got, tt.want)
			}
		})
	}
}
```

The `String()` ordering matches the documented pass execution order (NoUnit, NoEnum, NoTernary, NoComputed, NoLambda, NoToggle, NoReactivity, NoTimer, NoDeclarative).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/lower/...`
Expected: FAIL with "package internal/lower not declared" or similar.

- [ ] **Step 3: Write the Caps implementation**

Write `internal/lower/caps.go`:

```go
// Package lower performs capability-driven IR-to-IR transformations between
// the optimizer and codegen. Each lowering pass is gated by a Caps flag:
// passes whose flag is true rewrite high-level constructs into simpler
// primitives that the target platform/language can natively emit.
package lower

import "strings"

// Caps declares which high-level SNGL constructs the target cannot consume
// directly. A flag set to true requests the corresponding lowering pass.
//
// Caps values come from the platform's and language's Capabilities() methods
// and are merged field-wise via OR before lower.Lower runs.
type Caps struct {
	NoToggle      bool // x!! → x = !x
	NoTernary     bool // a ? b : c → if/else stmt with temp var
	NoLambda      bool // closures → top-level funcs + captured-state struct
	NoUnit        bool // unit values → underlying int
	NoEnum        bool // enum members → int constants
	NoComputed    bool // computed vars → inlined exprs or memoized funcs
	NoTimer       bool // timer decls → explicit scheduler.At()/cancel() calls
	NoReactivity  bool // reactive deps → explicit updater stmts after each mutation
	NoDeclarative bool // visual node tree → flat stream of create/update/delete IR calls
}

// Merge returns the field-wise OR of c and other. Either side disabling a
// feature requests the corresponding lowering pass.
func (c Caps) Merge(other Caps) Caps {
	return Caps{
		NoToggle:      c.NoToggle || other.NoToggle,
		NoTernary:     c.NoTernary || other.NoTernary,
		NoLambda:      c.NoLambda || other.NoLambda,
		NoUnit:        c.NoUnit || other.NoUnit,
		NoEnum:        c.NoEnum || other.NoEnum,
		NoComputed:    c.NoComputed || other.NoComputed,
		NoTimer:       c.NoTimer || other.NoTimer,
		NoReactivity:  c.NoReactivity || other.NoReactivity,
		NoDeclarative: c.NoDeclarative || other.NoDeclarative,
	}
}

// String returns a comma-separated list of enabled flags in pass-execution
// order (NoUnit first, NoDeclarative last). Empty string when no flags set.
func (c Caps) String() string {
	var parts []string
	if c.NoUnit {
		parts = append(parts, "NoUnit")
	}
	if c.NoEnum {
		parts = append(parts, "NoEnum")
	}
	if c.NoTernary {
		parts = append(parts, "NoTernary")
	}
	if c.NoComputed {
		parts = append(parts, "NoComputed")
	}
	if c.NoLambda {
		parts = append(parts, "NoLambda")
	}
	if c.NoToggle {
		parts = append(parts, "NoToggle")
	}
	if c.NoReactivity {
		parts = append(parts, "NoReactivity")
	}
	if c.NoTimer {
		parts = append(parts, "NoTimer")
	}
	if c.NoDeclarative {
		parts = append(parts, "NoDeclarative")
	}
	return strings.Join(parts, ",")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/lower/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/lower/caps.go internal/lower/caps_test.go
git commit -m "$(cat <<'EOF'
Add lower.Caps struct, Merge, String

Capability flags drive which lowering passes run. Field-wise OR merge
combines platform and language caps. String orders flags by pass-execution
order so dump output matches the runtime sequence.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Pass struct + 9 stub files

**Files:**
- Create: `internal/lower/lower.go` (pass struct + ordered registry, no Lower yet)
- Create: `internal/lower/toggle.go`
- Create: `internal/lower/ternary.go`
- Create: `internal/lower/lambda.go`
- Create: `internal/lower/unit.go`
- Create: `internal/lower/enum.go`
- Create: `internal/lower/computed.go`
- Create: `internal/lower/reactivity.go`
- Create: `internal/lower/timer.go`
- Create: `internal/lower/declarative.go`

- [ ] **Step 1: Write `lower.go` with pass struct + ordered registry**

Write `internal/lower/lower.go`:

```go
package lower

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// pass is one lowering pass: a name (matching its Caps field), a predicate
// over Caps for whether it runs, and the apply function that mutates pkg in
// place.
type pass struct {
	name    string
	enabled func(Caps) bool
	apply   func(*ir.Package) error
}

// passes is the fixed execution order. Earlier passes may not depend on
// transformations performed by later ones; later passes may. Order rationale:
//  1. NoUnit, NoEnum — collapse types, no deps.
//  2. NoTernary — rewrites expressions, no deps on visual model.
//  3. NoComputed — must run before NoReactivity (plain reads vs. computed indirections).
//  4. NoLambda — must run before NoReactivity (helpers may inject closures otherwise).
//  5. NoToggle — cheap stmt rewrite; before NoReactivity so the assignment is visible.
//  6. NoReactivity — analyzes dataflow, injects updaters.
//  7. NoTimer — depends on reactivity decisions (timer handlers may have been wrapped).
//  8. NoDeclarative — last; flattens the visual tree, destroying shape earlier passes used.
var passes = []pass{
	passUnit,
	passEnum,
	passTernary,
	passComputed,
	passLambda,
	passToggle,
	passReactivity,
	passTimer,
	passDeclarative,
}
```

- [ ] **Step 2: Write all 9 stub files**

Write `internal/lower/toggle.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passToggle = pass{
	name:    "NoToggle",
	enabled: func(c Caps) bool { return c.NoToggle },
	apply:   lowerToggle,
}

// lowerToggle rewrites toggle statements (x!!) into assignments (x = !x).
// Phase 1: stub — implementation lands in Phase 2.
func lowerToggle(pkg *ir.Package) error { return nil }
```

Write `internal/lower/ternary.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passTernary = pass{
	name:    "NoTernary",
	enabled: func(c Caps) bool { return c.NoTernary },
	apply:   lowerTernary,
}

// lowerTernary rewrites a ? b : c expressions into if/else statements with
// a temporary variable; the original expression is replaced by a reference
// to that temp.
// Phase 1: stub — implementation lands in Phase 2.
func lowerTernary(pkg *ir.Package) error { return nil }
```

Write `internal/lower/lambda.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passLambda = pass{
	name:    "NoLambda",
	enabled: func(c Caps) bool { return c.NoLambda },
	apply:   lowerLambda,
}

// lowerLambda lifts closures to top-level functions plus captured-state
// structs. Requires accurate capture analysis from the checker.
// Phase 1: stub — implementation lands in Phase 2 or later.
func lowerLambda(pkg *ir.Package) error { return nil }
```

Write `internal/lower/unit.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passUnit = pass{
	name:    "NoUnit",
	enabled: func(c Caps) bool { return c.NoUnit },
	apply:   lowerUnit,
}

// lowerUnit collapses unit-typed values to their underlying int.
// Phase 1: stub — implementation lands in Phase 2.
func lowerUnit(pkg *ir.Package) error { return nil }
```

Write `internal/lower/enum.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passEnum = pass{
	name:    "NoEnum",
	enabled: func(c Caps) bool { return c.NoEnum },
	apply:   lowerEnum,
}

// lowerEnum collapses enum member references to int constants.
// Phase 1: stub — implementation lands in Phase 2.
func lowerEnum(pkg *ir.Package) error { return nil }
```

Write `internal/lower/computed.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passComputed = pass{
	name:    "NoComputed",
	enabled: func(c Caps) bool { return c.NoComputed },
	apply:   lowerComputed,
}

// lowerComputed resolves computed variables either by inlining the
// expression at each use site or by hoisting to a memoized func. Must run
// before NoReactivity so dataflow sees plain reads, not computed
// indirections.
// Phase 1: stub — implementation lands in Phase 3.
func lowerComputed(pkg *ir.Package) error { return nil }
```

Write `internal/lower/reactivity.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passReactivity = pass{
	name:    "NoReactivity",
	enabled: func(c Caps) bool { return c.NoReactivity },
	apply:   lowerReactivity,
}

// lowerReactivity analyzes which mutations affect which visual nodes and
// injects explicit updater statements after each mutation. Owns dataflow
// analysis (currently in codegen/analysis.go and codegen/deps.go; copied
// here in Phase 3).
// Phase 1: stub — implementation lands in Phase 3.
func lowerReactivity(pkg *ir.Package) error { return nil }
```

Write `internal/lower/timer.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passTimer = pass{
	name:    "NoTimer",
	enabled: func(c Caps) bool { return c.NoTimer },
	apply:   lowerTimer,
}

// lowerTimer rewrites timer declarations into explicit scheduler.At() and
// cancel() calls. Depends on reactivity decisions (timer handlers may have
// been wrapped by passReactivity).
// Phase 1: stub — implementation lands in Phase 3.
func lowerTimer(pkg *ir.Package) error { return nil }
```

Write `internal/lower/declarative.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passDeclarative = pass{
	name:    "NoDeclarative",
	enabled: func(c Caps) bool { return c.NoDeclarative },
	apply:   lowerDeclarative,
}

// lowerDeclarative flattens the visual node tree into a stream of explicit
// create / append / update IR calls. Last pass because it destroys the tree
// shape earlier passes rely on.
// Phase 1: stub — implementation lands in Phase 3.
func lowerDeclarative(pkg *ir.Package) error { return nil }
```

- [ ] **Step 3: Run build to verify compiles**

Run: `go build ./internal/lower/...`
Expected: success, no output.

- [ ] **Step 4: Add a registry sanity test**

Append to `internal/lower/lower_test.go` (create the file):

```go
package lower

import "testing"

func TestPassRegistry_OrderAndUniqueness(t *testing.T) {
	expectedOrder := []string{
		"NoUnit",
		"NoEnum",
		"NoTernary",
		"NoComputed",
		"NoLambda",
		"NoToggle",
		"NoReactivity",
		"NoTimer",
		"NoDeclarative",
	}
	if len(passes) != len(expectedOrder) {
		t.Fatalf("passes length = %d; want %d", len(passes), len(expectedOrder))
	}
	seen := make(map[string]bool)
	for i, p := range passes {
		if p.name != expectedOrder[i] {
			t.Errorf("passes[%d].name = %q; want %q", i, p.name, expectedOrder[i])
		}
		if seen[p.name] {
			t.Errorf("duplicate pass name %q", p.name)
		}
		seen[p.name] = true
		if p.apply == nil {
			t.Errorf("passes[%d] (%s) has nil apply", i, p.name)
		}
		if p.enabled == nil {
			t.Errorf("passes[%d] (%s) has nil enabled", i, p.name)
		}
	}
}

func TestPassRegistry_StubsAreNoOps(t *testing.T) {
	// Each pass.apply on a nil pkg should not panic and should return nil.
	// (Stubs ignore pkg entirely; this is a structural sanity check that
	// every registered apply is callable.)
	for _, p := range passes {
		if err := p.apply(nil); err != nil {
			t.Errorf("pass %s stub returned error: %v", p.name, err)
		}
	}
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/lower/...`
Expected: PASS for both `TestPassRegistry_*`.

- [ ] **Step 6: Commit**

```bash
git add internal/lower/
git commit -m "$(cat <<'EOF'
Add lower pass registry and 9 no-op pass stubs

Each pass declares its Caps predicate and apply func. Fixed execution order
encodes pass dependencies: NoUnit → NoEnum → NoTernary → NoComputed →
NoLambda → NoToggle → NoReactivity → NoTimer → NoDeclarative. All stubs
are no-ops; real implementations land in Phases 2 and 3.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Lower entry point with StopAfter

**Files:**
- Modify: `internal/lower/lower.go` — add `Options`, `Lower` function, helpers
- Modify: `internal/lower/lower_test.go` — add tests for `Lower` behavior

- [ ] **Step 1: Write the failing tests**

Append to `internal/lower/lower_test.go`:

```go
import "git.duckfam.us/jonathan/sngl/ir"

func TestLower_NoCapsIsNoop(t *testing.T) {
	pkg := &ir.Package{}
	err := Lower(pkg, Caps{}, Options{})
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
}

func TestLower_RunsEnabledPassesInOrder(t *testing.T) {
	var ran []string
	// Replace passes for the duration of the test with instrumented ones.
	orig := passes
	t.Cleanup(func() { passes = orig })
	passes = []pass{
		{name: "NoUnit", enabled: func(c Caps) bool { return c.NoUnit }, apply: func(*ir.Package) error { ran = append(ran, "NoUnit"); return nil }},
		{name: "NoEnum", enabled: func(c Caps) bool { return c.NoEnum }, apply: func(*ir.Package) error { ran = append(ran, "NoEnum"); return nil }},
		{name: "NoToggle", enabled: func(c Caps) bool { return c.NoToggle }, apply: func(*ir.Package) error { ran = append(ran, "NoToggle"); return nil }},
	}
	pkg := &ir.Package{}
	err := Lower(pkg, Caps{NoUnit: true, NoToggle: true}, Options{})
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	want := []string{"NoUnit", "NoToggle"}
	if len(ran) != len(want) {
		t.Fatalf("ran = %v; want %v", ran, want)
	}
	for i, n := range want {
		if ran[i] != n {
			t.Errorf("ran[%d] = %q; want %q", i, ran[i], n)
		}
	}
}

func TestLower_StopAfter(t *testing.T) {
	var ran []string
	orig := passes
	t.Cleanup(func() { passes = orig })
	passes = []pass{
		{name: "NoUnit", enabled: func(c Caps) bool { return true }, apply: func(*ir.Package) error { ran = append(ran, "NoUnit"); return nil }},
		{name: "NoEnum", enabled: func(c Caps) bool { return true }, apply: func(*ir.Package) error { ran = append(ran, "NoEnum"); return nil }},
		{name: "NoToggle", enabled: func(c Caps) bool { return true }, apply: func(*ir.Package) error { ran = append(ran, "NoToggle"); return nil }},
	}
	err := Lower(&ir.Package{}, Caps{}, Options{StopAfter: "NoEnum"})
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	want := []string{"NoUnit", "NoEnum"}
	if len(ran) != len(want) {
		t.Fatalf("ran = %v; want %v", ran, want)
	}
	for i, n := range want {
		if ran[i] != n {
			t.Errorf("ran[%d] = %q; want %q", i, ran[i], n)
		}
	}
}

func TestLower_StopAfterNone(t *testing.T) {
	var ran []string
	orig := passes
	t.Cleanup(func() { passes = orig })
	passes = []pass{
		{name: "NoUnit", enabled: func(c Caps) bool { return true }, apply: func(*ir.Package) error { ran = append(ran, "NoUnit"); return nil }},
	}
	err := Lower(&ir.Package{}, Caps{NoUnit: true}, Options{StopAfter: "none"})
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if len(ran) != 0 {
		t.Errorf("ran = %v; want []", ran)
	}
}

func TestLower_StopAfterUnknown(t *testing.T) {
	err := Lower(&ir.Package{}, Caps{}, Options{StopAfter: "NoBogus"})
	if err == nil {
		t.Fatal("Lower: want error for unknown StopAfter, got nil")
	}
}

func TestLower_PropagatesPassError(t *testing.T) {
	orig := passes
	t.Cleanup(func() { passes = orig })
	wantErr := fmt.Errorf("kaboom")
	passes = []pass{
		{name: "NoUnit", enabled: func(c Caps) bool { return true }, apply: func(*ir.Package) error { return wantErr }},
	}
	err := Lower(&ir.Package{}, Caps{NoUnit: true}, Options{})
	if err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("Lower err = %v; want wrapped %q", err, wantErr)
	}
}
```

Add the new imports at the top of the test file (replacing the bare `import "testing"`):

```go
import (
	"fmt"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/lower/...`
Expected: FAIL with "undefined: Lower" / "undefined: Options" / etc.

- [ ] **Step 3: Implement Lower and Options**

Append to `internal/lower/lower.go`:

```go
import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// Options controls a single Lower invocation.
type Options struct {
	// StopAfter, when non-empty, stops the pipeline after the named pass
	// runs (matching pass.name, e.g. "NoToggle"). Special value "none"
	// runs no passes — useful for the dump command's "show pre-lowering
	// state" mode. Empty string runs all enabled passes.
	StopAfter string
}

// Lower applies all enabled lowering passes to pkg in execution order,
// mutating pkg in place. Caps determines which passes run; opts.StopAfter
// optionally short-circuits the pipeline after a named pass.
//
// Returns an error wrapping the failing pass's name when any pass fails
// or when opts.StopAfter names a pass that does not exist.
func Lower(pkg *ir.Package, caps Caps, opts Options) error {
	if opts.StopAfter == "none" {
		return nil
	}
	if opts.StopAfter != "" {
		known := false
		for _, p := range passes {
			if p.name == opts.StopAfter {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("lower: unknown StopAfter %q (valid: %s)", opts.StopAfter, PassNames())
		}
	}
	for _, p := range passes {
		if !p.enabled(caps) {
			continue
		}
		if err := p.apply(pkg); err != nil {
			return fmt.Errorf("lower: pass %s: %w", p.name, err)
		}
		if opts.StopAfter != "" && p.name == opts.StopAfter {
			break
		}
	}
	return nil
}

// PassNames returns the ordered list of pass names. Used for help text and
// error messages.
func PassNames() []string {
	names := make([]string, len(passes))
	for i, p := range passes {
		names[i] = p.name
	}
	return names
}

// EnabledPasses returns the ordered list of pass names that would run for
// the given caps. Used by `dump lowered --list`.
func EnabledPasses(caps Caps) []string {
	var out []string
	for _, p := range passes {
		if p.enabled(caps) {
			out = append(out, p.name)
		}
	}
	return out
}
```

Note: the import block at the top of `lower.go` (already importing `ir`) needs `fmt` added. Use one consolidated import block:

```go
import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)
```

Replace any existing import in `lower.go` with this consolidated form.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/lower/...`
Expected: PASS — all 8 tests (`TestCaps_Merge`, `TestCaps_String`, `TestPassRegistry_OrderAndUniqueness`, `TestPassRegistry_StubsAreNoOps`, `TestLower_NoCapsIsNoop`, `TestLower_RunsEnabledPassesInOrder`, `TestLower_StopAfter`, `TestLower_StopAfterNone`, `TestLower_StopAfterUnknown`, `TestLower_PropagatesPassError`).

- [ ] **Step 5: Commit**

```bash
git add internal/lower/lower.go internal/lower/lower_test.go
git commit -m "$(cat <<'EOF'
Add lower.Lower entry point with StopAfter option

Lower iterates passes in fixed order, runs enabled ones, and supports
opts.StopAfter for partial-pipeline dumps. Special "none" value runs no
passes (used by `dump lowered --after none` to show pre-lowering state).
PassNames and EnabledPasses surface registry info for the dump command.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Add Capabilities() to PlatformGenerator + LangTranslator interfaces

**Files:**
- Modify: `codegen/codegen.go` — add interface methods
- Modify: `codegen/platform/android/android.go`, `codegen/platform/bubbletea/bubbletea.go`, `codegen/platform/fyne/fyne.go`, `codegen/platform/html/html.go`, `codegen/platform/none/none.go`
- Modify: `codegen/lang/golang/golang.go`, `codegen/lang/javascript/javascript.go`, `codegen/lang/kotlin/kotlin.go`, `codegen/lang/none/none.go`

This task does not have a TDD-style failing test first — interface additions break the build and must be fixed together. The "test" is `go build ./...` succeeds.

- [ ] **Step 1: Add interface methods to codegen.go**

Edit `codegen/codegen.go`. Find the `LangTranslator` interface (around line 73 — it begins `type LangTranslator interface`) and add `Capabilities() lower.Caps` as a new method. Find the `PlatformGenerator` interface (around line 93 — `type PlatformGenerator interface`) and do the same.

Add the import for `lower`:

```go
import (
	"errors"
	"io"
	"io/fs"
	"strings"
	"text/template"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)
```

Update interfaces:

```go
type LangTranslator interface {
	ir.Language

	// Capabilities declares which high-level SNGL constructs this language's
	// translator cannot consume. Drives lowering passes; return lower.Caps{}
	// when no lowering is needed.
	Capabilities() lower.Caps

	// New IR-based API (v2).
	WriteExpr(w io.Writer, expr ir.Expr, scope *ir.Scope) error
	WriteStmt(w io.Writer, expr ir.Stmt, scope *ir.Scope) error
	WriteType(w io.Writer, t *ir.Type) error
	GenerateIdentifier(name *ir.Ident) string
	Eval(expr ir.Expr) string

	// IR-typed public API.
	TranslateIRExpr(e ir.Expr, scope *ExprScope) string
	TranslateIRMutation(s ir.Stmt, scope *ExprScope) []string
	TranslateIRLiteral(e ir.Expr) string

	TypeToNative(hint string) string
	ExportName(name string) string
}

type PlatformGenerator interface {
	ir.Platform
	SupportedLangs() []string

	// Capabilities declares which high-level SNGL constructs this platform
	// cannot consume. Drives lowering passes; return lower.Caps{} when no
	// lowering is needed.
	Capabilities() lower.Caps

	Generate(req *Request) (*Response, error)
}
```

- [ ] **Step 2: Verify build breaks**

Run: `go build ./...`
Expected: FAIL with errors about each existing platform/lang missing `Capabilities()` method.

- [ ] **Step 3: Add stub Capabilities() to every platform**

For `codegen/platform/android/android.go`, locate the existing methods on `*Generator` and add (after `IsLanguageSupported`):

```go
func (g *Generator) Capabilities() lower.Caps { return lower.Caps{} }
```

Add the import line `"git.duckfam.us/jonathan/sngl/internal/lower"` to its import block.

Repeat the same change for:
- `codegen/platform/bubbletea/bubbletea.go`
- `codegen/platform/fyne/fyne.go`
- `codegen/platform/html/html.go`
- `codegen/platform/none/none.go`

For each, the addition is the single method `func (g *Generator) Capabilities() lower.Caps { return lower.Caps{} }` plus the import.

- [ ] **Step 4: Add stub Capabilities() to every language**

For `codegen/lang/golang/golang.go`, locate the existing methods on `*Translator` and add (after `Resolve`):

```go
func (t *Translator) Capabilities() lower.Caps { return lower.Caps{} }
```

Add the import line `"git.duckfam.us/jonathan/sngl/internal/lower"` to its import block.

Repeat for:
- `codegen/lang/javascript/javascript.go`
- `codegen/lang/kotlin/kotlin.go`
- `codegen/lang/none/none.go`

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: success, no output.

- [ ] **Step 6: Run full test suite**

Run: `go test ./...`
Expected: PASS for all packages (or unchanged from main — net behavior is zero so failures must already exist on main).

- [ ] **Step 7: Commit**

```bash
git add codegen/codegen.go codegen/platform/ codegen/lang/
git commit -m "$(cat <<'EOF'
Add Capabilities() to PlatformGenerator and LangTranslator

Every platform and language now declares its lowering capabilities via a
new interface method. All existing implementations return lower.Caps{} for
zero net behavior change; real caps land per-platform in later phases.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Public sngl.Lower wrapper

**Files:**
- Modify: `sngl.go`
- Test: covered by integration test in Task 6.

- [ ] **Step 1: Add sngl.Lower**

Edit `sngl.go`. Add the import for `lower` (alongside the existing imports):

```go
import (
	"io"
	"os"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)
```

Add at the bottom of the file:

```go
// Lower runs the lowering pipeline on a checked + optimized IR Package.
// caps comes from merging the target platform's and language's
// Capabilities(). Mutates pkg in place.
//
// Lower is a thin wrapper exposing internal/lower.Lower as part of the
// stable public API alongside Parse, Check, Convert.
func Lower(pkg *ir.Package, caps lower.Caps) error {
	return lower.Lower(pkg, caps, lower.Options{})
}

// Caps is the capability struct used by Lower. Re-exported here so callers
// don't need to import internal/lower directly.
type Caps = lower.Caps
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add sngl.go
git commit -m "$(cat <<'EOF'
Add public sngl.Lower wrapper and Caps type alias

Exposes internal/lower behind the stable sngl.* surface so external callers
(LSP, playground, future tooling) can run lowering without depending on
internal packages.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Wire lower into compile pipeline

**Files:**
- Modify: `cmd/sngl/compile.go`

The compile pipeline runs `optimize.Optimize` once today. Phase 1 inserts:
1. After existing optimize: collect caps from platform + language
2. `lower.Lower(pkg, caps, Options{})`
3. Second `optimize.Optimize` pass to clean up lowering artifacts

Since all caps are zero in Phase 1, lowering and the second optimize are net no-ops. They must still run so Phase 2+ work is plumbed.

- [ ] **Step 1: Modify runCompile in cmd/sngl/compile.go**

Edit `cmd/sngl/compile.go`. Find the section in `runCompile` that calls `optimize.Optimize` (around lines 117–127). After the call, add:

Before the existing `optimize.Optimize` block, add `lower` import to the file's import group:

```go
import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/spf13/cobra"
)
```

Replace the optimize+codegen section in `runCompile` (currently lines 117–139) with:

```go
		for _, target := range targets {
			if target.Options == nil {
				target.Options = &ir.StructLit{}
			}
			// Set projectDir for resolving relative paths in output options
			// (icon paths from output declarations are relative to the .sngl dir)
			if _, ok := codegen.OptionField(target.Options, "projectDir"); !ok {
				codegen.SetOptionField(target.Options, "projectDir", dir)
			}

			start = time.Now()
			optCfg := &optimize.Config{
				Platform:    target.Platform,
				Language:    target.Lang,
				Dir:         dir,
				NoCacheBust: optionBool(target.Options, "noCacheBust"),
			}
			if err := optimize.Optimize(pkg, optCfg); err != nil {
				return fmt.Errorf("%s: %w", dir, err)
			}
			slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

			// Lowering: rewrite high-level constructs into primitives the
			// platform/language can consume. Capabilities come from both
			// sides via field-wise OR.
			plat := codegen.LookupPlatform(target.Platform)
			lang := codegen.LookupLang(target.Lang)
			if plat == nil {
				return fmt.Errorf("%s: unknown platform %q (available: %v)", filename, target.Platform, codegen.Platforms())
			}
			if lang == nil {
				return fmt.Errorf("%s: unknown language %q (available: %v)", filename, target.Lang, codegen.Langs())
			}
			caps := plat.Capabilities().Merge(lang.Capabilities())
			start = time.Now()
			if err := lower.Lower(pkg, caps, lower.Options{}); err != nil {
				return fmt.Errorf("%s: %w", dir, err)
			}
			slog.Info("lower", "dir", dir, "caps", caps.String(), "duration", time.Since(start))

			// Second optimize pass cleans up artifacts of lowering
			// (folded toggles, dead branches, etc.).
			start = time.Now()
			if err := optimize.Optimize(pkg, optCfg); err != nil {
				return fmt.Errorf("%s: %w", dir, err)
			}
			slog.Info("optimize2", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

			// Convert optimizer file assets to codegen file assets.
			var fileAssets []codegen.FileAsset
			for _, fa := range optCfg.FileAssets {
				fileAssets = append(fileAssets, codegen.FileAsset{SrcPath: fa.SrcPath, OutPath: fa.OutPath, Data: fa.Data})
			}

			start = time.Now()
			if err := generateTarget(filename, pkg, target, outDir, fileAssets, quiet(cmd)); err != nil {
				return err
			}
			slog.Info("codegen", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))
		}
```

Note: `generateTarget` already does its own `LookupPlatform`/`LookupLang` and validates support; the lookups here are duplicate but harmless. They are needed because we need the Capabilities() before generateTarget is called. (Optimization opportunity for a later phase: thread plat/lang into generateTarget.)

Also be aware: `optCfg.FileAssets` is populated by the *second* optimize pass; the first pass's FileAssets get overwritten. This matches today's single-pass semantics — assets like file:// references are deterministic in const folding regardless of pass count.

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Run all tests including script tests**

Run: `go test ./...`
Expected: PASS, including `cmd/sngl` script tests. Net behavior change is zero; any existing failures on `main` carry over but no new failures.

If any new failures appear, they indicate the second optimize pass behaves differently than the first. Investigate before proceeding — most likely cause is the optimizer's `Config.FileAssets` not being reset between passes (if duplicated assets cause issues, change the second-pass call to use a fresh `optCfg2 := &optimize.Config{...}` matching the first).

- [ ] **Step 4: Commit**

```bash
git add cmd/sngl/compile.go
git commit -m "$(cat <<'EOF'
Wire lower into compile pipeline

Pipeline becomes: optimize → lower → optimize → codegen. Caps merged from
platform and language. Second optimize pass cleans lowering artifacts.
Net behavior change is zero (all platforms return lower.Caps{} today).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: dump lowered subcommand

**Files:**
- Modify: `cmd/sngl/dump.go`

- [ ] **Step 1: Add dumpLoweredCmd command and runner**

Edit `cmd/sngl/dump.go`. Add the `lower` import:

```go
import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/spf13/cobra"
)
```

Add the new subcommand near the existing dump subcommands. Replace the existing `init()` to register it:

```go
var dumpLoweredCmd = &cobra.Command{
	Use:   "lowered [file|dir]",
	Short: "Dump IR after lowering passes",
	Long: `Dump IR after lowering. Use --after PASS to dump intermediate state
after a specific pass (case-sensitive, matching Caps field name).
--after none dumps post-optimize / pre-lower state.
--list prints resolved caps and pass list, then exits.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDumpLowered,
}

func init() {
	dumpCmd.PersistentFlags().String("format", "spew", "output format (spew, json, sngl)")
	dumpCmd.PersistentFlags().String("color", "auto", "colorize output (auto, on, off)")
	dumpCmd.PersistentFlags().String("input", "auto", "input source (auto, sngl, stdin, txtar, markdown)")
	dumpCmd.PersistentFlags().Bool("pointers", false, "show pointer addresses (useful for identifying shared objects)")
	dumpCmd.PersistentFlags().Int("depth", 0, "maximum depth for spew output (default unlimited)")
	dumpCmd.PersistentFlags().StringSlice("omit", nil, "omit struct fields by name (comma-separated, e.g. --omit AST,Pos)")

	dumpOptimizedCmd.Flags().String("lang", "", "target language")
	dumpOptimizedCmd.Flags().String("platform", "", "target platform")
	dumpAnalysisCmd.Flags().String("lang", "", "target language")
	dumpAnalysisCmd.Flags().String("platform", "", "target platform")

	dumpLoweredCmd.Flags().String("lang", "", "target language")
	dumpLoweredCmd.Flags().String("platform", "", "target platform")
	dumpLoweredCmd.Flags().String("after", "", "dump IR after named pass (e.g. NoToggle); 'none' = pre-lower state")
	dumpLoweredCmd.Flags().Bool("list", false, "print resolved caps and pass list, then exit")

	dumpCmd.AddCommand(dumpParsedCmd, dumpCheckedCmd, dumpOptimizedCmd, dumpAnalysisCmd, dumpLoweredCmd)
}
```

Add the runner at the bottom of the file:

```go
func runDumpLowered(cmd *cobra.Command, args []string) error {
	f, inp, err := dumpResolveFlags(cmd, args)
	if err != nil {
		return err
	}

	doc, dir, err := dumpParseInput(inp, args)
	if err != nil {
		return err
	}

	start := time.Now()
	pkg, err := checkDoc(doc, dir, true)
	if err != nil {
		return err
	}
	slog.Info("check", "dir", dir, "duration", time.Since(start))

	target, err := dumpResolveTarget(cmd, pkg)
	if err != nil {
		return err
	}

	plat := codegen.LookupPlatform(target.Platform)
	lang := codegen.LookupLang(target.Lang)
	if plat == nil {
		return fmt.Errorf("unknown platform %q", target.Platform)
	}
	if lang == nil {
		return fmt.Errorf("unknown language %q", target.Lang)
	}
	caps := plat.Capabilities().Merge(lang.Capabilities())

	if listOnly, _ := cmd.Flags().GetBool("list"); listOnly {
		enabled := lower.EnabledPasses(caps)
		fmt.Printf("caps: %s\n", caps.String())
		if len(enabled) == 0 {
			fmt.Println("passes: (none)")
		} else {
			fmt.Printf("passes: %s\n", strings.Join(enabled, " → "))
		}
		return nil
	}

	start = time.Now()
	if err := optimize.Optimize(pkg, &optimize.Config{
		Platform: target.Platform,
		Language: target.Lang,
		Dir:      dir,
	}); err != nil {
		return err
	}
	slog.Info("optimize", "dir", dir, "lang", target.Lang, "platform", target.Platform, "duration", time.Since(start))

	stopAfter, _ := cmd.Flags().GetString("after")
	start = time.Now()
	if err := lower.Lower(pkg, caps, lower.Options{StopAfter: stopAfter}); err != nil {
		return err
	}
	slog.Info("lower", "dir", dir, "caps", caps.String(), "stopAfter", stopAfter, "duration", time.Since(start))

	doc = ir.Convert(pkg)
	return dumpDocument(f, doc)
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./cmd/sngl/...`
Expected: success.

- [ ] **Step 3: Manual smoke test — list mode**

Pick any existing example file, e.g.:

Run: `go run ./cmd/sngl dump lowered --lang go --platform bubbletea --list testdata/test_arithmetic.sngl`
Expected output (something like):

```
caps:
passes: (none)
```

(Empty caps because every platform/lang returns `lower.Caps{}` in Phase 1.)

- [ ] **Step 4: Manual smoke test — sngl format**

Run: `go run ./cmd/sngl dump lowered --lang go --platform bubbletea --format sngl testdata/test_arithmetic.sngl`
Expected: SNGL source roughly equivalent to the input (lowering is no-op so this matches `dump optimized --format sngl` byte-for-byte).

- [ ] **Step 5: Manual smoke test — unknown pass**

Run: `go run ./cmd/sngl dump lowered --lang go --platform bubbletea --after NoBogus testdata/test_arithmetic.sngl`
Expected: error including `unknown StopAfter "NoBogus"` and the list of valid pass names.

- [ ] **Step 6: Commit**

```bash
git add cmd/sngl/dump.go
git commit -m "$(cat <<'EOF'
Add `sngl dump lowered` subcommand

Dumps IR after lowering, with --after PASS for intermediate state and
--list to inspect resolved caps + pass order without running the pipeline.
Inherits format/color/omit from parent dump command.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Regression script test

**Files:**
- Create: `cmd/sngl/testdata/script/dump_lowered_list.txt`

This is a txtar-format script test that verifies `dump lowered --list` runs without error against a sample file for every shipping platform — Phase 1's "no platform breaks" canary.

- [ ] **Step 1: Look at an existing script test for the txtar conventions**

Run: `ls cmd/sngl/testdata/script/ | head -10`

Read one for syntax reference, e.g. `cmd/sngl/testdata/script/<first-file>.txt`. Note the `--` block separators and how stdout/stderr expectations are written.

- [ ] **Step 2: Write the script test**

Create `cmd/sngl/testdata/script/dump_lowered_list.txt`:

```
# Verifies `dump lowered --list` succeeds for every shipping platform
# in Phase 1 (all caps zero so pass list is empty).

sngl dump lowered --lang go --platform bubbletea --list main.sngl
stdout 'caps:'
stdout 'passes: \(none\)'

sngl dump lowered --lang go --platform fyne --list main.sngl
stdout 'caps:'
stdout 'passes: \(none\)'

sngl dump lowered --lang go --platform html --list main.sngl
stdout 'caps:'
stdout 'passes: \(none\)'

sngl dump lowered --lang js --platform html --list main.sngl
stdout 'caps:'
stdout 'passes: \(none\)'

sngl dump lowered --lang kotlin --platform android --list main.sngl
stdout 'caps:'
stdout 'passes: \(none\)'

-- main.sngl --
component main {
    text(value="hello")
}
```

- [ ] **Step 3: Run the script test**

Run: `go test ./cmd/sngl/ -run TestScript/dump_lowered_list -v`
Expected: PASS.

If it fails because `--list` produces unexpected output (e.g., leading whitespace), inspect actual output and adjust the regex patterns. The literal output from `runDumpLowered` is `caps: ` (followed by empty string) and `passes: (none)` — the regex pattern `caps:` alone matches.

- [ ] **Step 4: Run full test suite**

Run: `go test ./...`
Expected: PASS for all packages (modulo any failures already on main).

- [ ] **Step 5: Commit**

```bash
git add cmd/sngl/testdata/script/dump_lowered_list.txt
git commit -m "$(cat <<'EOF'
Add script test verifying `dump lowered --list` works on all platforms

Phase 1 regression canary: every shipping platform/language combination
must run `dump lowered --list` without error. Validates Capabilities()
plumbing across the whole codegen registry.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Final verification

- [ ] **Step 1: Run the full project test suite**

Run: `go tool verify`
Expected: PASS (matching baseline on main; Phase 1 introduces no behavior changes).

- [ ] **Step 2: Build the CLI**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 3: Verify pipeline works end-to-end on a real example**

Pick a known-working example, e.g.:

Run: `sngl compile --lang go --platform bubbletea --out /tmp/sngl-phase1-check examples/<some-example>.sngl`
Expected: success, output files in `/tmp/sngl-phase1-check/`.

Run: `diff -r /tmp/sngl-phase1-check <previous-output-from-main>` (if you have one)
Expected: no diff. Generated output should be byte-identical to pre-Phase-1 output.

If a slight diff appears (e.g., file ordering, optimizer running twice changing cache-bust hashes): investigate. The second optimize pass may have effects that need taming. Most likely fix: make the second pass aware it's a "cleanup" pass via a Config flag, or skip cache-busting on the second pass.

- [ ] **Step 4: Commit any tweaks needed for clean output**

If Step 3 surfaced changes, fix them in their own focused commit and re-run Step 3 until clean.

---

## Self-Review Notes

Spec coverage:
- §Architecture pipeline → Task 6 wires `optimize → lower → optimize`.
- §Capability sources → Task 4 adds methods to both interfaces.
- §Caps struct → Task 1.
- §Pass ordering → Task 2 (registry) + Task 3 (Lower iterates in registry order).
- §Pass shape → Task 2 (`pass` struct).
- §Dump UX → Task 7 (`dump lowered`, `--after`, `--list`).
- §Migration Plan Phase 1 ("Net behavior: zero") → Tasks 1–9 collectively. All caps return zero; no behavior changes.
- §Migration Plan Phase 1 "Tests verify the dump command works" → Task 8.

Out of scope for this plan (covered by future Phase 2+ plans):
- Real implementations of any pass (all stubs in Phase 1).
- HTML/Fyne porting to consume lowered IR (Phase 4–5).
- `MutationModelEmitter` deletion (Phase 6).
- New imperative platform (Phase 7, separate spec).
- txtar test scaffolding for `internal/lower/testdata/` (lands with first real pass implementation in Phase 2).
