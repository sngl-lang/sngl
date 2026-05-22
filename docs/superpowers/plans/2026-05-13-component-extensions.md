# Component Extensions Implementation Plan (Issue 63 / Plan H)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `component sngl.X { platform <name> { body } }` a real
checker primitive — the platform body is merged into the stdlib
`*ir.Component` so bare `X(...)` user calls bind to a body-bearing
component. Plus children-count enforcement at call sites. After
landing, Plan G Tasks 10-12 resume.

**Tech Stack:** Go, `internal/checker`, `internal/parser` (read-only —
grammar already supports `platform` blocks), `ast.ComponentDecl`,
`ast.PlatformStmt`, `ir.Component`.

**Spec:** `docs/superpowers/specs/2026-05-13-component-extensions-design.md`.

**Predecessors:** Plans A, B, B.2, C, F, G Tasks 1-9 (on `main`).

**Successors:** Plan G Tasks 10-14 (delete `gtk4TagToCType`,
`gtk4Constructor`, `htmlTagToDOM`); Plan D Tasks 10-17.

---

## File Structure

**Modify:**
- `internal/checker/checker.go` — extension declaration validation; reject in `registerComponent` if `Name` is qualified and invalid.
- `internal/checker/stdlib.go` — extension merge pass after stdlib registration; consume `cfg.Platforms[0].Package()` for `sngl.X` decls.
- `internal/checker/components.go` (or wherever `checkNodeInst` lives — likely `internal/checker/visual.go` or `expr.go`) — children-count enforcement at call sites.
- `codegen/platform/html/html.sngl` — rewrite all `component sngl.X()` decls into new form with `platform html { ... }` wrapper.
- `codegen/platform/bubbletea/bubbletea.sngl` — same.
- `codegen/platform/gtk4/gtk4.sngl` — rename bare `component button` → `component sngl.button { platform gtk4 { ... } }`; same for all.
- `codegen/platform/fyne/fyne.sngl` — same rename pattern.

**Create:**
- `internal/checker/testdata/extension_basic.sngl`
- `internal/checker/testdata/extension_rejects_props.sngl`
- `internal/checker/testdata/extension_rejects_children_type.sngl`
- `internal/checker/testdata/extension_rejects_naked_body.sngl`
- `internal/checker/testdata/extension_rejects_non_sngl.sngl`
- `internal/checker/testdata/children_count_required.sngl`
- `internal/checker/testdata/children_count_optional.sngl`
- `internal/checker/testdata/children_count_list.sngl`
- `internal/checker/testdata/children_count_none.sngl`
- `internal/lower/testdata/inline_strict_extension.txtar`

---

## Phase A: Checker validation

### Task 1: Validate extension declarations in `registerComponent`

**Files:**
- Modify: `internal/checker/checker.go` near line 857 (`func (c *checker) registerComponent`).

- [ ] **Step 1: Detect qualified names**

After `irComp := &ir.Component{...}`, before processing Props/Body,
check if `comp.Name` contains a `.`. If so:

```go
if dot := strings.IndexByte(comp.Name, '.'); dot > 0 {
    namespace := comp.Name[:dot]
    local := comp.Name[dot+1:]
    if namespace != "sngl" {
        c.error(comp.Pos, "extension namespace %q not supported (only \"sngl\" is valid)", namespace)
        return
    }
    if len(comp.Props.Props) > 0 {
        c.error(comp.Props.Props[0].(... Pos accessor ...), "component extension %q may not declare props (inherited from stdlib)", comp.Name)
    }
    if comp.ChildrenType != nil {
        c.error(comp.Pos, "component extension %q may not declare children type (inherited from stdlib)", comp.Name)
    }
    for _, s := range comp.Body.Stmts {
        if _, ok := s.(*ast.PlatformStmt); ok {
            continue
        }
        if _, ok := s.(*ast.Comment); ok {
            continue
        }
        c.error(*s.StmtPos(), "component extension %q body must contain only platform blocks", comp.Name)
        break
    }
    // Suppress normal registration — extension merge handles it.
    _ = local
    return
}
```

- [ ] **Step 2: Param/EventDecl position helper**

`ast.ParamOrEventDecl` is an interface (Param/EventDecl). To get a
position for the prop-validation error, type-switch on the interface
and pull `.Pos`. Or use `comp.Props.Pos` as a fallback if needed.

- [ ] **Step 3: Build**

`go build ./internal/checker/...`

### Task 2: Fixtures for extension validation

**Files:**
- Create: `internal/checker/testdata/extension_rejects_props.sngl`,
  `extension_rejects_children_type.sngl`,
  `extension_rejects_naked_body.sngl`,
  `extension_rejects_non_sngl.sngl`.

- [ ] **Step 1: extension_rejects_props.sngl**

```sngl
component sngl.text(extra string) {
    platform html {
        span(textContent=value) {}
    }
}

// ERROR(check) "may not declare props"

component main {
    text(value="hi")
}
```

- [ ] **Step 2: extension_rejects_children_type.sngl**

```sngl
component sngl.text list<dyn> {
    platform html { span(textContent=value) {} }
}
// ERROR(check) "may not declare children type"
```

- [ ] **Step 3: extension_rejects_naked_body.sngl**

```sngl
component sngl.text {
    span(textContent=value) {}
}
// ERROR(check) "body must contain only platform blocks"
```

- [ ] **Step 4: extension_rejects_non_sngl.sngl**

```sngl
component foo.bar {
    platform html { span {} }
}
// ERROR(check) "extension namespace \"foo\" not supported"
```

- [ ] **Step 5: Run**

`go test ./internal/checker/... -run TestFixtures` (or the
appropriate runner that picks up `testdata/*.sngl` ERROR directives).

---

## Phase B: Extension merge

### Task 3: Pre-flight: enforce exactly-one platform

**Files:**
- Modify: `internal/checker/stdlib.go` (or `checker.go` near `NewChecker`/`Check` entry).

- [ ] **Step 1: Validate Platforms count at extension-merge time**

Extension merge is a no-op when zero platforms are registered (test
configurations). When `len(cfg.Platforms) > 1`, emit checker error:

```go
if len(c.cfg.Platforms) > 1 {
	c.error(ast.Pos{}, "component extensions require exactly one registered platform (got %d)", len(c.cfg.Platforms))
	return
}
```

Pre-Plan-H test code that registers multiple platforms must be
updated to register one — search `testdata` and `*_test.go` for
patterns like `Platforms: []ir.Platform{a, b}` and split into separate
test invocations.

- [ ] **Step 2: Update tests**

Run `go test ./internal/checker/...`; expect failures from
multi-platform test setups. Fix them by removing extra platforms or
splitting tests.

### Task 4: Implement extension merge

**Files:**
- Modify: `internal/checker/stdlib.go` — new function `mergePlatformExtensions`.
- Modify: `internal/checker/checker.go` — call `mergePlatformExtensions` after `loadStdlib` but before user-package processing.

- [ ] **Step 1: Write merge function**

```go
// mergePlatformExtensions walks the active platform's Package() docs
// for `component sngl.X` declarations, locates the corresponding
// stdlib component, and writes the matching `platform <name>` body
// into the stdlib *ir.Component.AST.Body. The component is then
// re-processed via checkComponentBody to populate the IR Body.
func (c *checker) mergePlatformExtensions() {
	if len(c.cfg.Platforms) != 1 {
		return // pre-flight already errored or test mode
	}
	p := c.cfg.Platforms[0]
	platformName := p.PlatformIdentifier()
	for _, doc := range p.Package() {
		for _, stmt := range doc.Stmts {
			decl, ok := stmt.(*ast.ComponentDecl)
			if !ok || !strings.HasPrefix(decl.Name, "sngl.") {
				continue
			}
			local := strings.TrimPrefix(decl.Name, "sngl.")
			stdComp, ok := c.symtab.Comps[local]
			if !ok {
				c.error(decl.Pos, "extension %q references unknown stdlib component %q", decl.Name, local)
				continue
			}
			// Find the matching platform block.
			var matched *ast.StmtBlock
			for _, s := range decl.Body.Stmts {
				pl, ok := s.(*ast.PlatformStmt)
				if !ok {
					continue
				}
				if pl.Platform == platformName {
					matched = &pl.Body
					break
				}
			}
			if matched == nil {
				continue // platform has no implementation for this stdlib component
			}
			// Replace stdlib component body with the platform-specific one.
			stdComp.AST.Body = *matched
			// Re-process the body now that the AST is populated.
			c.checkComponentBody(stdComp)
		}
	}
}
```

- [ ] **Step 2: Wire the call**

In `checker.Check` (or the constructor flow), after `loadStdlib`
returns and before `registerComponent`/user-package processing
begins, call `c.mergePlatformExtensions()`.

- [ ] **Step 3: Resolution scope**

The platform body references native tags. The platform's
`Namespace.Resolve` (declared in `NewChecker` lines 163-167) handles
qualified access (`html.span`). For unqualified access, the platform
package's `Package()` documents already include `import` statements
for native namespaces — `checkComponentBody` walks the scope chain
that includes them. Verify during implementation: if unqualified
native tag resolution fails, add an implicit platform-namespace
fallback scope under the stdlib component's resolution.

- [ ] **Step 4: Build**

`go build ./internal/checker/...`

### Task 5: Extension-basic fixture

**Files:**
- Create: `internal/checker/testdata/extension_basic.sngl`.

- [ ] **Step 1: Write fixture**

```sngl
component main {
    text(value="hi")
}
```

This depends on a registered platform that ships `sngl.text` with a
matching `platform <name> { ... }` body. The checker fixture runner
registers html (or whichever platform), and the fixture asserts the
build succeeds and (via `// FOLD(...)`-style directive if available)
that `text` lowers to the platform's native shape.

- [ ] **Step 2: Run**

`go test ./internal/checker/... -run TestFixtures`. Expect pass.

---

## Phase C: Platform .sngl rewrites

### Task 6: Rewrite html.sngl + bubbletea.sngl

**Files:**
- Modify: `codegen/platform/html/html.sngl`.
- Modify: `codegen/platform/bubbletea/bubbletea.sngl`.

- [ ] **Step 1: html.sngl conversion**

For every `component sngl.X(<props>) { body }`:

```diff
-component sngl.text() {
-    span(textContent=value) {}
-}
+component sngl.text {
+    platform html {
+        span(textContent=value) {}
+    }
+}
```

Drop parens, drop ChildrenType, wrap body in `platform html { ... }`.

- [ ] **Step 2: bubbletea.sngl conversion**

Same shape, `platform bubbletea { ... }`.

- [ ] **Step 3: Build + test**

`go test ./codegen/platform/html/... ./codegen/platform/bubbletea/...`.

### Task 7: Rewrite gtk4.sngl

**Files:**
- Modify: `codegen/platform/gtk4/gtk4.sngl`.

- [ ] **Step 1: Rename + wrap**

For each bare wrapper, e.g. `component button() { GtkButton(...) }`:

```diff
-component button() {
-    GtkButton(label=text, @clicked { @click() }) {}
-}
+component sngl.button {
+    platform gtk4 {
+        GtkButton(label=text, @clicked { @click() }) {}
+    }
+}
```

Repeat for `label`, `entry`, `checkbox`, `image`, `vbox`, `hbox`,
`scroll`, `window`.

- [ ] **Step 2: Build + test**

`go test ./codegen/platform/gtk4/...`. With extension merge wired,
bare `text(...)` user calls now bind to the merged stdlib component
whose body is GtkLabel. Lower-time produces native tags.

### Task 8: Rewrite fyne.sngl

**Files:**
- Modify: `codegen/platform/fyne/fyne.sngl`.

- [ ] **Step 1: Rename + wrap**

Apply the same pattern. All 27 wrappers (text, button, entry, hbox,
vbox, image, check, select, spacer, scroll, modal, …) get
`sngl.<name>` + `platform fyne { ... }` wrapper.

- [ ] **Step 2: Build + test**

`go test ./codegen/platform/fyne/...`.

### Task 9: Full test sweep

- [ ] **Step 1: Full test suite**

`go test ./...`. Should be clean.

- [ ] **Step 2: `go tool verify`**

Run `go tool verify`. Expect pass except the pre-existing failure.

---

## Phase D: Children-count enforcement

### Task 10: Locate `checkNodeInst` call-site checker

**Files:**
- Read: search `internal/checker/` for the function that validates
  `*ast.VisualNode` call sites against the resolved component's props
  and children.

- [ ] **Step 1: Identify**

`grep -rn "checkVisualNode\|checkNodeInst\|VisualNode" internal/checker/`.
Likely `internal/checker/visual.go` or `expr.go`.

### Task 11: Enforce children count

**Files:**
- Modify: the file from Task 10.

- [ ] **Step 1: Add validation**

After resolving the component for a call site, compare child count to
ChildrenType:

```go
n := countChildren(node.Body.Stmts) // VisualNode + control-flow contribute 1; comments excluded
ct := comp.ChildrenType
switch {
case ct == nil:
	if n > 0 {
		c.error(node.Pos, "component %q does not accept children", comp.Name)
	}
case ct.Kind == ir.TypeList:
	// any
case ct.Kind == ir.TypeOption:
	if n > 1 {
		c.error(node.Pos, "component %q accepts at most one child (got %d)", comp.Name, n)
	}
default:
	if n != 1 {
		c.error(node.Pos, "component %q requires exactly one child (got %d)", comp.Name, n)
	}
}
```

- [ ] **Step 2: Helper**

Write `countChildren` that walks stmts and counts VisualNode +
control-flow (If, For, PlatformStmt body length). Comments excluded.

### Task 12: Children-count fixtures

**Files:**
- Create: `internal/checker/testdata/children_count_required.sngl`,
  `children_count_optional.sngl`, `children_count_list.sngl`,
  `children_count_none.sngl`.

- [ ] **Step 1: children_count_required.sngl**

```sngl
component wrapper(dyn) {
    vbox { @children }
}
component main {
    wrapper {}
    wrapper {
        text(value="a")
        text(value="b")
    }
    wrapper { text(value="ok") }
}

// ERROR(check) "requires exactly one child"
// ERROR(check) "requires exactly one child"
// OK
```

(Adapt to actual SNGL child-binding syntax.)

- [ ] **Step 2: children_count_optional.sngl**

```sngl
component wrapper option<dyn> {
    vbox { @children }
}
component main {
    wrapper {}
    wrapper { text(value="x") }
    wrapper {
        text()
        text()
    }
}

// OK
// OK
// ERROR(check) "at most one child"
```

- [ ] **Step 3: children_count_list.sngl**

```sngl
component wrapper list<dyn> {
    vbox { @children }
}
component main {
    wrapper {
        text()
        text()
        text()
    }
}

// OK
```

- [ ] **Step 4: children_count_none.sngl**

```sngl
component leaf {
    text(value="leaf")
}
component main {
    leaf { text() }
}
// ERROR(check) "does not accept children"
```

- [ ] **Step 5: Run**

`go test ./internal/checker/... -run TestFixtures`.

### Task 13: Audit existing fixtures for child-count violations

- [ ] **Step 1: Run full suite**

`go test ./...`. Some pre-existing fixtures may now fail because they
silently produce extra/missing children. Fix each by adjusting the
fixture to match the new rules (or report back if a fixture
intentionally exercises previously-accepted-but-now-rejected shapes;
that's a real scope question).

---

## Phase E: Resume Plan G

### Task 14: Run Plan G Tasks 10-14

- [ ] **Step 1: Trigger inlining on gtk4/html**

With extensions merged, `passInlinePure` now sees bodies for
`text`/`button`/etc. Run `go test ./codegen/platform/gtk4/...` and
`./codegen/platform/html/...` — translators should still receive
native tags only. If `OnCreateNode` is called with stdlib tag, the
extension merge isn't taking effect; debug.

- [ ] **Step 2: Delete `gtk4TagToCType` + `gtk4Constructor`**

In `codegen/platform/gtk4/intrinsic_translator.go`, remove the
switches. `OnCreateNode` reads constructor metadata from
`Component.Native` (see fyne for the pattern, but gtk4 has a
GIR-resolved approach — match it). If `Component.Native` isn't
populated for native tags, file as a Plan G Task 10 follow-up before
deleting the switches.

- [ ] **Step 3: Delete `htmlTagToDOM`**

In `codegen/platform/html/intrinsic_translator.go`, remove
`htmlTagToDOM`. Simplify `htmlPropSetter` — props/events live on
native HTML tags now.

- [ ] **Step 4: Final verification**

`go test ./...` and `go tool verify`. Run `go tool sngl run examples/hello-i18n --platform html` and `--platform gtk4` if
practical. Mark TaskList 70-72 completed.

### Task 15: inline_strict_extension fixture

**Files:**
- Create: `internal/lower/testdata/inline_strict_extension.txtar`.

- [ ] **Step 1: Write fixture**

Use the teststub platform from `internal/lower/golden_test.go`. Extend
the stub source to ship `component sngl.text { platform teststub { nativespan(textContent=value) {} } }`. Fixture asserts user `text(value="hi")`
inlines to the `nativespan` body.

- [ ] **Step 2: Run**

`go test ./internal/lower/...`. Expect pass.

---

## Risk register

- **Resolution scope inside merged extension body**: native tags may
  not resolve if the platform body doesn't import its own namespace.
  Verify Task 4 Step 3 in implementation; add fallback scope if
  needed.
- **`passPlatform` interaction**: existing lowering pass strips
  non-matching platform blocks. Merged extensions land the matching
  body directly in the component's AST, so `passPlatform` shouldn't
  see them — but it might still run over the substituted body during
  inlining. Verify the pass is idempotent / safe on already-stripped
  bodies.
- **Test config breakage**: pre-flight rejection of multi-platform
  configs will break checker tests that register e.g. html + bubbletea
  together. Pre-Task 3 audit recommended.
- **Children-count fixtures**: Task 13 may surface real bugs in
  existing fixtures or stdlib components. Surface, don't paper over.
