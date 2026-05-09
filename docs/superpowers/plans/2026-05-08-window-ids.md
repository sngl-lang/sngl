# Window identifiers + path-derived output Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop emitting `window_L<line>.html` collisions; derive html output paths from each window's folded `href`; let `window #id(...)` declarations be referenced as `id.href`/`id.title` so cross-window links survive renames.

**Architecture:** Reuse the existing visual-node `#id` syntax — the parser already lifts it into `VisualNode.ID`. Drop the `window_L<line>` synthetic name and the literal-href-to-name override in the checker, leaving `ir.Window.Name` to mean "user identifier" and nothing else. Add `struct Window { href, title: string }` to the stdlib so `*ir.Window` symbols type as a regular struct and field access flows through existing checker machinery; the optimizer folds `name.href` to the underlying expression at compile time. For for-loop windows, the unroller emits a list of `Window` struct literals bound to the symbol so refs outside the loop see `list<Window>`. The html platform derives output filenames from the folded literal `Href`.

**Tech Stack:** Go, the SNGL compiler. Test framework: `internal/testutil` (testdata fixtures, `// ERROR(check)` directives), `cmd/sngl/script_test.go` (txtar), `go tool verify`.

Spec: `docs/superpowers/specs/2026-05-08-window-ids-design.md`.

---

### Task 1: Confirm `#id` parses on `window` and reaches IR

The parser already extracts `#id` from any visual node into `VisualNode.ID` (`internal/parser/build.go:920-926`), and `buildWindow` already sets `w.Name = vn.ID` (`internal/checker/checker.go:1344`). Add a fixture that exercises this path so we lock the behavior in before changing anything else.

**Files:**
- Create: `testdata/window_id_parses.sngl`

- [ ] **Step 1: Write the fixture**

```sngl
window #home(title="Home", href="/") {
    text(value="Home")
}

window #about(title="About", href="/about.html") {
    text(value="About")
}
```

- [ ] **Step 2: Run the testdata sweep**

Run: `go test ./internal/parser/... ./internal/checker/...`
Expected: PASS. The fixture is picked up by the testdata glob in `internal/testutil/sample.go:104` and parsed/checked without error.

- [ ] **Step 3: Verify `#id` reaches `ir.Window.Name`**

Run: `go tool sngl dump checked testdata/window_id_parses.sngl 2>&1 | grep -A1 'Window' | grep Name`
Expected output includes `Name: (string) (len=4) "home"` and `Name: (string) (len=5) "about"`.

- [ ] **Step 4: Commit**

```bash
git add testdata/window_id_parses.sngl
git commit -m "test: pin #id-on-window parse + IR carry"
```

---

### Task 2: Drop the `window_L<line>` checker fallback and the literal-href→Name override

The checker currently overwrites `Name` from a literal href and synthesizes `window_L<line>` when neither `#id` nor a literal href is present. Both behaviors must go: `Name` will mean "user identifier or empty" only.

**Files:**
- Modify: `internal/checker/checker.go:1343-1386` (`buildWindow`)
- Create: `testdata/window_anonymous_no_synthetic.sngl`

- [ ] **Step 1: Write the fixture**

```sngl
// An anonymous window must not get a "window_L<line>" name. The checker
// should leave Name empty; the platform decides what to do.
window(title="A", href="/a.html") {
    text(value="a")
}
```

- [ ] **Step 2: Add a checker test that pins the empty-name invariant**

Modify: `internal/checker/checker_test.go` — add at the end:

```go
func TestAnonymousWindowHasEmptyName(t *testing.T) {
	src, err := os.ReadFile("../../testdata/window_anonymous_no_synthetic.sngl")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parser.Parse("window_anonymous_no_synthetic.sngl", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := Check(doc, "", nil)
	if diags.HasErrors() {
		t.Fatalf("check: %v", diags)
	}
	if len(pkg.Windows) != 1 {
		t.Fatalf("want 1 window, got %d", len(pkg.Windows))
	}
	if got := pkg.Windows[0].Name; got != "" {
		t.Fatalf("want empty Name, got %q", got)
	}
}
```

- [ ] **Step 3: Run; expect failure**

Run: `go test ./internal/checker/... -run TestAnonymousWindowHasEmptyName`
Expected: FAIL with `want empty Name, got "window_L3"` (or similar).

- [ ] **Step 4: Strip the override + fallback in `buildWindow`**

Replace `internal/checker/checker.go:1343-1386` with:

```go
func (c *checker) buildWindow(vn *ast.VisualNode) *ir.Window {
	w := &ir.Window{AST: vn, Name: vn.ID}
	// URL template params like `{name}` in href become string vars on the
	// window, in scope for the href literal itself as well as the body.
	for _, name := range hrefPathParams(vn) {
		w.Vars = append(w.Vars, &ir.Var{Name: name, Type: TypString})
	}
	c.pushScope()
	defer c.popScope()
	for _, v := range w.Vars {
		c.scope.Declare(v)
	}
	for _, a := range vn.Args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			switch arg.Name {
			case "href":
				w.Href = c.checkExpr(arg.Value)
			case "title":
				w.Title = c.checkExpr(arg.Value)
			case "favicon":
				w.Favicon = c.checkExpr(arg.Value)
			}
		case ast.EventHandler:
			if arg.Name == "error" {
				w.ErrorHandler = c.buildErrorHandler(&arg)
			}
		}
	}
	return w
}
```

(`strings` import in `checker.go` may become unused; remove it from the import block if so.)

- [ ] **Step 5: Run; expect pass**

Run: `go test ./internal/checker/... -run TestAnonymousWindowHasEmptyName`
Expected: PASS.

- [ ] **Step 6: Run the broader suite — expect pre-existing fixtures to pass**

Run: `go test ./internal/checker/... ./internal/parser/...`
Expected: PASS. The previously-checked fixtures (`window_basic.sngl`, `window_in_main.sngl`, `window_anonymous.sngl`) only assert that checking succeeds, not that `Name` is non-empty.

- [ ] **Step 7: Commit**

```bash
git add internal/checker/checker.go internal/checker/checker_test.go testdata/window_anonymous_no_synthetic.sngl
git commit -m "checker: drop window_L<line> fallback; #id is the only Name source"
```

---

### Task 3: Remove the dead optimizer fold that derived Name from folded href

`internal/optimize/fold.go:165-177` derives `n.Name` from the folded literal href when `n.Name == ""`. With Task 2 applied, anonymous windows do reach the optimizer with empty Name — but we want filename derivation to live in the html platform, not as an IR mutation, so this branch must come out.

**Files:**
- Modify: `internal/optimize/fold.go:164-185`

- [ ] **Step 1: Replace the `case *ir.Window:` block**

Replace `internal/optimize/fold.go:164-185` with:

```go
	case *ir.Window:
		if n.Href != nil {
			n.Href = foldExpr(n.Href, ctx)
		}
		if n.Title != nil {
			n.Title = foldExpr(n.Title, ctx)
		}
		if n.Favicon != nil {
			n.Favicon = foldExpr(n.Favicon, ctx)
		}
		n.Body = foldStmts(n.Body, ctx)
	}
	return s
}
```

(Adjust closing braces to match the surrounding `switch`; check `internal/optimize/fold.go:185-187` lines after edit to ensure syntax.)

- [ ] **Step 2: Run optimizer tests**

Run: `go test ./internal/optimize/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/optimize/fold.go
git commit -m "optimize: remove dead Name-from-href fold (now lives in html platform)"
```

---

### Task 4: Derive html output filename from the folded `Href`

The html platform currently uses `win.Name + ".html"` as the output path. Replace with a function that maps the folded literal `Href` to a filesystem path.

**Files:**
- Modify: `codegen/platform/html/html.go:290-326`
- Create: `codegen/platform/html/path.go`
- Create: `codegen/platform/html/path_test.go`

- [ ] **Step 1: Write the path-derivation tests**

Create `codegen/platform/html/path_test.go`:

```go
package html

import "testing"

func TestPathFromHref(t *testing.T) {
	tests := []struct {
		href, want string
	}{
		{"/", "index.html"},
		{"", "index.html"},
		{"/index.html", "index.html"},
		{"/about.html", "about.html"},
		{"/docs/sngl/components/avatar.html", "docs/sngl/components/avatar.html"},
		{"/foo/", "foo/index.html"},
		{"/foo", "foo/index.html"},
		{"/foo/bar/", "foo/bar/index.html"},
	}
	for _, tc := range tests {
		got := pathFromHref(tc.href)
		if got != tc.want {
			t.Errorf("pathFromHref(%q) = %q, want %q", tc.href, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run; expect compile error**

Run: `go test ./codegen/platform/html/... -run TestPathFromHref`
Expected: FAIL with `undefined: pathFromHref`.

- [ ] **Step 3: Implement `pathFromHref`**

Create `codegen/platform/html/path.go`:

```go
package html

import "strings"

// pathFromHref converts a window's folded href (an absolute URL path) into
// the relative filesystem path under the output directory.
//
//	"/"                              → "index.html"
//	"/index.html"                    → "index.html"
//	"/foo.html"                      → "foo.html"
//	"/foo/"                          → "foo/index.html"
//	"/foo"                           → "foo/index.html" (no extension ⇒ directory)
//	"/foo/bar.html"                  → "foo/bar.html"
func pathFromHref(href string) string {
	href = strings.TrimPrefix(href, "/")
	if href == "" || href == "index.html" {
		return "index.html"
	}
	if strings.HasSuffix(href, "/") {
		return href + "index.html"
	}
	// No extension → treat as a directory route.
	last := href
	if i := strings.LastIndex(href, "/"); i >= 0 {
		last = href[i+1:]
	}
	if !strings.Contains(last, ".") {
		return href + "/index.html"
	}
	return href
}
```

- [ ] **Step 4: Run; expect pass**

Run: `go test ./codegen/platform/html/... -run TestPathFromHref`
Expected: PASS.

- [ ] **Step 5: Wire `pathFromHref` into the per-window loop**

Replace `codegen/platform/html/html.go:290-326` (the `singleWindow := ...; for _, win := range irWindows { ... }` block) with:

```go
	singleWindow := len(irWindows) == 1
	var mainStmts []ir.Stmt
	seenPaths := map[string]ast.Pos{}
	for _, win := range irWindows {
		var name string
		if singleWindow && (win.Window == nil || win.Window.Href == nil) {
			name = "index.html"
		} else {
			href, ok := codegen.IRLiteralString(win.Window.Href)
			if !ok {
				return nil, fmt.Errorf("html: window %q has a non-literal href after folding (internal error)", win.Name)
			}
			name = pathFromHref(href)
		}
		if prev, dup := seenPaths[name]; dup {
			pos := ast.Pos{}
			if win.Window != nil && win.Window.AST != nil {
				pos = win.Window.AST.Pos
			}
			return nil, fmt.Errorf("html: window output path collision: %q emitted by both %s and %s", name, prev, pos)
		}
		if win.Window != nil && win.Window.AST != nil {
			seenPaths[name] = win.Window.AST.Pos
		} else {
			seenPaths[name] = ast.Pos{}
		}
		gen := newHTMLGenFromCtx(ctx, jsLang, opts)
		gen.wasmLoader = wasmLoaderHTML
		gen.wasmPkgs = wasmPkgs
		gen.stylesheet = stylesheetURL
		gen.projectDir = projectDir
		gen.projectFS = projectFS
		gen.irBodyStmts = win.Body
		if win.Window != nil {
			if s, ok := codegen.IRLiteralString(win.Window.Title); ok {
				gen.title = s
			}
			if s, ok := codegen.IRLiteralString(win.Window.Favicon); ok {
				gen.favicon = s
			}
		}
		body, err := gen.generate()
		if err != nil {
			return nil, err
		}
		src := codegen.Header("html", req.Source, "<!-- ", " -->") + body
		if opts.Minify {
			min, err := minifyHTML(src)
			if err != nil {
				return nil, err
			}
			src = min
		}
		c.windows = append(c.windows, htmlWindowOutput{name: name, bytes: []byte(src)})
		if mainStmts == nil {
			mainStmts = win.Body
		}
	}
	return ctx.BuildMutation(mainStmts), nil
}
```

Add the `"git.duckfam.us/jonathan/sngl/ast"` import to `html.go` if not already present.

- [ ] **Step 6: Build**

Run: `go build ./codegen/platform/html/...`
Expected: PASS.

- [ ] **Step 7: Add a script-test fixture for the multi-window output paths**

Create `cmd/sngl/testdata/multi_window_paths.txt`:

```
sngl compile --out out site.sngl
exists out/index.html
exists out/about.html
exists out/docs/intro.html
! exists out/window_L1.html

-- site.sngl --
output { js { html } }

component main {
    window #home(title="Home", href="/") { text(value="home") }
    window #about(title="About", href="/about.html") { text(value="about") }
    window #intro(title="Intro", href="/docs/intro.html") { text(value="intro") }
}
```

- [ ] **Step 8: Run script tests**

Run: `go test ./cmd/sngl/... -run TestScript/multi_window_paths`
Expected: PASS.

- [ ] **Step 9: Add the path-collision test fixture**

Create `cmd/sngl/testdata/window_path_collision.txt`:

```
! sngl compile --out out site.sngl
stderr 'window output path collision'

-- site.sngl --
output { js { html } }

component main {
    window #a(title="A", href="/dup.html") { text(value="a") }
    window #b(title="B", href="/dup.html") { text(value="b") }
}
```

- [ ] **Step 10: Run script tests**

Run: `go test ./cmd/sngl/... -run TestScript/window_path_collision`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add codegen/platform/html/path.go codegen/platform/html/path_test.go codegen/platform/html/html.go cmd/sngl/testdata/multi_window_paths.txt cmd/sngl/testdata/window_path_collision.txt
git commit -m "html: derive output filename from folded href; detect path collisions"
```

---

### Task 5: Reject duplicate `#id`s at the same scope (checker)

Two windows with the same `#id` at the package root, or within the same component body, should be a checker error. (For-loop bodies don't count — Task 7 makes a single `#id` inside a loop produce a `list<Window>`.)

**Files:**
- Modify: `internal/checker/checker.go` — wherever `*ir.Window`s are appended to `pkg.Windows` and `mainComponent.Body`. Find both insertion points and add a duplicate check.
- Create: `testdata/error_window_id_collision.sngl`

- [ ] **Step 1: Write the error fixture**

```sngl
window #home(title="A", href="/a.html") {
    text(value="A")
}

window #home(title="B", href="/b.html") {
    text(value="B")
}
// ERROR(check) "duplicate window id \"home\""
```

- [ ] **Step 2: Locate the insertion points**

Run: `grep -n "Windows = append\|Pkg.Windows" internal/checker/*.go`
Expected: identifies the lines that append a window to `pkg.Windows` and to a component body.

- [ ] **Step 3: Add a helper + call sites**

In `internal/checker/checker.go`, near `buildWindow`, add:

```go
// checkDuplicateWindowID reports an error if w.Name is non-empty and another
// window with the same Name already exists in seen. Returns true when the
// window was unique (and was added to seen).
func (c *checker) checkDuplicateWindowID(w *ir.Window, seen map[string]bool) bool {
	if w.Name == "" {
		return true
	}
	if seen[w.Name] {
		c.error(w.AST.Pos, "duplicate window id %q", w.Name)
		return false
	}
	seen[w.Name] = true
	return true
}
```

Call it from each site that collects windows at a single scope (package root and main-component body). Example wiring (adjust to actual call sites):

```go
seen := map[string]bool{}
for _, vn := range topLevelWindowNodes {
	w := c.buildWindow(vn)
	c.checkDuplicateWindowID(w, seen)
	pkg.Windows = append(pkg.Windows, w)
}
```

- [ ] **Step 4: Run the checker error sweep**

Run: `go test ./internal/checker/... -run TestErrorFixtures`
(Or whatever existing test runs `// ERROR(check)` directives — find with `grep -rn "ERROR(check)" internal/checker/*.go internal/testutil/*.go`.)
Expected: PASS (the `error_window_id_collision.sngl` fixture's directive matches the emitted error).

- [ ] **Step 5: Commit**

```bash
git add internal/checker/checker.go testdata/error_window_id_collision.sngl
git commit -m "checker: reject duplicate window #id at the same scope"
```

---

### Task 6: Add `struct Window { href: string, title: string }` to stdlib

`*ir.Window` must type as a struct so existing field-access machinery handles `home.href`. Adding a struct decl in the stdlib gives us that for free.

**Files:**
- Modify: `lib/types.sngl`
- Modify: `internal/checker/stdlib.go` — locate the `Window` lookup (or where stdlib structs are registered) so `*ir.Window`'s SymType returns the new struct's type.
- Modify: `ir/ir.go:247-248` (`Window.SymType`)

- [ ] **Step 1: Add the struct to stdlib**

Append to `lib/types.sngl`:

```sngl
// Window is the type of `window #id(...)` declarations. Reading
// `name.href` / `name.title` lets links survive a window's path change
// without copying the path string.
struct Window {
	href: string
	title: string
}
```

- [ ] **Step 2: Wire `*ir.Window`'s SymType to the stdlib struct**

Modify `ir/ir.go:247-248`:

```go
func (w *Window) SymName() string { return w.Name }
func (w *Window) SymType() *Type  { return w.Typ }
```

Add a field to the struct (`internal/checker/stdlib.go` will set it after looking up the stdlib decl):

```go
// Window represents a window declaration at the root or component level.
type Window struct {
	AST          *ast.VisualNode
	Name         string
	Typ          *Type // *Type{Kind:TypeStruct, Decl: stdlib's Window struct}; nil if unresolved
	Href         Expr
	Title        Expr
	Favicon      Expr
	Vars         []*Var
	Funcs        []*Func
	Body         []Stmt
	Checked      bool
	ErrorHandler *EventHandler
}
```

- [ ] **Step 3: Set `Typ` in `buildWindow`**

In `internal/checker/checker.go`, modify `buildWindow` to set `w.Typ` from the stdlib struct. The stdlib loader (`internal/checker/stdlib.go`) already exposes structs to the checker — find the field for the parsed stdlib structs (grep for `Stdlib\|stdlibStructs\|Window` in `internal/checker/*.go`) and look up `Window` once at checker construction.

Concrete sketch (adapt to the actual stdlib accessor in this codebase):

```go
// near checker construction
for _, sd := range c.stdlib.Structs {
	if sd.Name == "Window" {
		c.windowType = &ir.Type{Kind: ir.TypeStruct, Decl: sd}
		break
	}
}
```

Then in `buildWindow`:

```go
w := &ir.Window{AST: vn, Name: vn.ID, Typ: c.windowType}
```

- [ ] **Step 4: Add a fixture exercising `name.href`**

Create `testdata/window_id_href_ref.sngl`:

```sngl
window #home(title="Home", href="/index.html") {
    text(value="Home")
}

window #about(title="About", href="/about.html") {
    link(text="Home", href=home.href)
    link(text="Self", href=about.href)
}
```

- [ ] **Step 5: Run; expect pass**

Run: `go test ./internal/checker/...`
Expected: PASS — `home.href` resolves through normal struct field access, types as `string`, no errors.

- [ ] **Step 6: Add a fold rule so `name.href` becomes the literal**

Without folding, `home.href` reaches codegen as a `Select` over a window symbol — which the language translators don't understand. Add a fold case:

In `internal/optimize/fold.go`, in `foldExpr` (find the `*ir.Select` case; if absent add it), add:

```go
case *ir.Select:
	x.Operand = foldExpr(x.Operand, ctx)
	if id, ok := x.Operand.(*ir.Ident); ok {
		if win, ok := id.Sym.(*ir.Window); ok {
			switch x.Field {
			case "href":
				if win.Href != nil {
					return win.Href
				}
			case "title":
				if win.Title != nil {
					return win.Title
				}
			}
		}
	}
```

- [ ] **Step 7: Run optimizer tests + the fixture**

Run: `go test ./internal/optimize/... ./internal/checker/...`
Expected: PASS.

- [ ] **Step 8: Pin the fold via a script test**

Create `cmd/sngl/testdata/window_href_ref.txt`:

```
sngl compile --out out site.sngl
exists out/index.html
exists out/about.html
stdout 'about\.html'

-- site.sngl --
output { js { html } }

component main {
    window #home(title="Home", href="/") { text(value="home") }
    window #about(title="About", href="/about.html") {
        html.a(href=home.href, innerText="Home")
    }
}
```

(`stdout 'about.html'` is a smoke check; replace with `grep` over `out/about.html` for the literal `href="/"` if the script harness supports file content checks.)

- [ ] **Step 9: Run**

Run: `go test ./cmd/sngl/... -run TestScript/window_href_ref`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add lib/types.sngl ir/ir.go internal/checker/checker.go internal/checker/stdlib.go internal/optimize/fold.go testdata/window_id_href_ref.sngl cmd/sngl/testdata/window_href_ref.txt
git commit -m "stdlib: add struct Window; fold name.href to underlying expr"
```

---

### Task 7: For-loop windows expose `list<Window>` outside the loop

When a `window #cp(...)` lives inside a for-loop, the symbol `cp` should resolve to a `list<Window>` outside the loop and to a scalar `Window` inside. The optimizer already unrolls const-iterable for-loops (`internal/optimize/expand.go:43`); extend it to also collect each iteration's window into a list bound to the symbol.

**Files:**
- Modify: `internal/optimize/expand.go`
- Modify: `internal/checker/checker.go` (or wherever the `#id` symbol is registered) so that inside a for-loop the symbol resolves as scalar `Window` and outside as `list<Window>`.

- [ ] **Step 1: Write the fixture**

Create `testdata/window_id_for_loop.sngl`:

```sngl
data items: list<struct{slug: string, title: string}> = [
	{slug="one", title="One"},
	{slug="two", title="Two"},
]

component main {
	for it = items {
		window #page(title=it.title, href="/" + it.slug + ".html") {
			text(value=it.title)
		}
	}

	window #index(title="Index", href="/index.html") {
		for p = page {
			html.a(href=p.href, innerText=p.title)
		}
	}
}
```

- [ ] **Step 2: Run; expect failure**

Run: `go tool sngl compile --out /tmp/wl-test testdata/window_id_for_loop.sngl`
Expected: FAIL — `page` resolves as a scalar `Window` (current state) and the outer `for p = page` errors with "iter source must be iter/list/map".

- [ ] **Step 3: Register the symbol with the right type at the right scope**

Where the checker descends into `*ast.ForStmt` bodies that contain windows, two things need to happen:

1. Inside the for-body, declare `#id` as a scalar `Window` symbol (current behavior, since `buildWindow` already produces an `*ir.Window` symbol with struct type).
2. Outside the for-body, bind `#id` as a `list<Window>` symbol that the optimizer can populate during expansion.

Sketch the second binding. Add to the `*ast.ForStmt` checker visitor (find with `grep -n 'ForStmt' internal/checker/*.go`):

```go
// After visiting the for-body, hoist any window #id declared inside as a
// list<Window> symbol into the enclosing scope.
for _, vn := range innerWindows {
	if vn.ID == "" {
		continue
	}
	listSym := &ir.Var{
		Name: vn.ID,
		Type: &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{c.windowType}},
	}
	c.scope.Declare(listSym)
}
```

(The exact mechanism depends on existing scope-handling — adapt.)

- [ ] **Step 4: Populate the list during for-loop expansion**

In `internal/optimize/expand.go:43-90` (`expandForStmt`), accumulate per-iteration `Window` struct literals keyed by `#id`:

```go
windowsByID := map[string][]any{}
for i, item := range items {
	// ... existing per-iteration setup ...
	cloned := make([]ir.Stmt, len(fs.Body))
	for i, bodyStmt := range fs.Body {
		cloned[i] = cloneStmt(bodyStmt)
	}
	folded := foldStmts(cloned, childCtx)
	for _, s := range folded {
		if w, ok := s.(*ir.Window); ok && w.Name != "" {
			windowsByID[w.Name] = append(windowsByID[w.Name], windowStructValue(w))
		}
	}
	result = append(result, folded...)
	ctx.fileAssets = childCtx.fileAssets
}
for id, vals := range windowsByID {
	ctx.values[lookupSymbol(ctx, id)] = vals // shape matches list-value convention used elsewhere
}
```

`windowStructValue(w)` returns whatever shape const-eval uses for struct values (look at the `*ir.StructLit` evaluator in `internal/optimize/consteval.go` for the convention; reuse it).

- [ ] **Step 5: Run the fixture compile**

Run: `go tool sngl compile --out /tmp/wl-test testdata/window_id_for_loop.sngl`
Expected: PASS. `/tmp/wl-test/one.html`, `/tmp/wl-test/two.html`, `/tmp/wl-test/index.html` exist; `index.html` contains links to `/one.html` and `/two.html`.

- [ ] **Step 6: Add a script test**

Create `cmd/sngl/testdata/window_for_loop_list.txt`:

```
sngl compile --out out site.sngl
exists out/one.html
exists out/two.html
exists out/index.html
grep '/one\.html' out/index.html
grep '/two\.html' out/index.html

-- site.sngl --
output { js { html } }

data items: list<struct{slug: string, title: string}> = [
    {slug="one", title="One"},
    {slug="two", title="Two"},
]

component main {
    for it = items {
        window #page(title=it.title, href="/" + it.slug + ".html") {
            text(value=it.title)
        }
    }
    window #index(title="Index", href="/index.html") {
        for p = page {
            html.a(href=p.href, innerText=p.title)
        }
    }
}
```

(If the script-test harness lacks a `grep` command, replace those lines with substring assertions on a captured `cat`.)

- [ ] **Step 7: Run**

Run: `go test ./cmd/sngl/... -run TestScript/window_for_loop_list`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/optimize/expand.go internal/checker/checker.go testdata/window_id_for_loop.sngl cmd/sngl/testdata/window_for_loop_list.txt
git commit -m "checker+optimize: window #id inside for-loop hoists as list<Window>"
```

---

### Task 8: Migrate `examples/http-session/app.sngl`

Two windows currently use the unsupported `window home(...)` form (parses as two visual nodes). Update to `#id`.

**Files:**
- Modify: `examples/http-session/app.sngl:12,31`

- [ ] **Step 1: Apply edits**

```diff
-    window home(title="Dashboard", href="/") {
+    window #home(title="Dashboard", href="/") {
```

```diff
-    window about(title="About", href="/about") {
+    window #about(title="About", href="/about") {
```

- [ ] **Step 2: Verify the example compiles**

Run: `cd examples/http-session && go tool sngl compile --out /tmp/hsess .` (from repo root: `go tool sngl compile --out /tmp/hsess examples/http-session`)
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add examples/http-session/app.sngl
git commit -m "examples(http-session): use window #id syntax"
```

---

### Task 9: Adopt `.href` refs in `website.sngl` where they reduce duplication

Replace the most-duplicated literal href patterns (`"/docs/sngl/components/" + comp.name + ".html"` repeated in both the window decl and the gallery card) with references through the loop-bound `Window` symbol. Don't try to convert every link — only the ones that reference a window declared in the same file.

**Files:**
- Modify: `website.sngl`

- [ ] **Step 1: Find the candidate links**

Run: `grep -n 'href="/docs/sngl/components/" + comp\|"/docs/" + pkg' website.sngl`
Expected: lines around 225 and 381 (gallery card + per-package links) plus the per-component window decl at 336.

- [ ] **Step 2: Tag the per-component window with `#id` and reuse**

Apply the diff (line numbers approximate; adjust to the file):

```diff
-		for comp = components {
-			window(title=comp.name, href="/docs/sngl/components/" + comp.name + ".html", favicon=logo) {
-				PageLayout(title=comp.name, currentHref="/docs/sngl/components/" + comp.name + ".html") {
+		for comp = components {
+			window #componentPage(title=comp.name, href="/docs/sngl/components/" + comp.name + ".html", favicon=logo) {
+				PageLayout(title=comp.name, currentHref=componentPage.href) {
```

Similarly tag `for d = allDeclPages` (`#declPage`), `for pkg = otherPackages` (`#packagePage`), `for page = pages` (`#docPage`) with `#id` and use `<id>.href` for the inner `currentHref` reference.

The gallery card (`window(title="Components"...)` at ~line 217) iterates `components` and could use `componentPage` as `list<Window>` once Task 7 lands — but that's a deeper refactor. Skip unless trivial.

- [ ] **Step 3: Build the docs site**

Run: `go tool docsgen`
Expected: PASS. `_site/window_L*.html` does NOT appear:

```bash
ls _site/window_L*.html
```

Expected: `ls: cannot access '_site/window_L*.html': No such file or directory`.

- [ ] **Step 4: Spot-check that per-component pages exist and are fresh**

Run:

```bash
ls -la _site/docs/sngl/components/avatar.html
grep -c '<title>avatar' _site/docs/sngl/components/avatar.html
```

Expected: file modified within the last minute; grep count ≥ 1.

- [ ] **Step 5: Commit**

```bash
git add website.sngl
git commit -m "docs(website): tag iterating windows with #id; use .href for inner refs"
```

---

### Task 10: Final verify

Catch anything the per-task checks missed.

- [ ] **Step 1: Stale-output sweep**

```bash
rm -rf _site
go tool docsgen
ls _site/window_L*.html 2>/dev/null && echo "FAIL: synthetic names still emitted" || echo "ok: no synthetic names"
```

Expected: `ok: no synthetic names`.

- [ ] **Step 2: Full test sweep**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 3: Format**

Run: `go fmt ./...`
Expected: no diff (or a trivial one — commit if so).

- [ ] **Step 4: Commit any verify-driven cleanups**

```bash
git status
git diff
# only commit if there are actual changes
```

---

## Self-Review

**Spec coverage**

- Goal 1 (path-from-href): Tasks 4, 9, 10.
- Goal 2 (`#id` + `Window` type with `.href`): Tasks 1, 2, 6.
- Goal 3 (rename-safe links): Tasks 6, 9.
- Non-goals respected — no changes to server-mode `routes.go`, no typed `link(to=...)` API, no link-text validation pass.
- Scoping rules (scalar inside loop, `list<Window>` outside): Task 7.
- Filename table: Task 4 step 1 covers each row.
- Path collision detection: Task 4 step 9.
- `#id` collision detection: Task 5.
- Migrations: Tasks 8, 9.

**Type consistency**

- `ir.Window` gains a `Typ *Type` field (Task 6 step 2); `SymType` returns it; checker sets it in `buildWindow` (Task 6 step 3); used by `for p = page` resolution and `home.href` field access.
- `pathFromHref` is package-private to `codegen/platform/html`; used only inside that package.
- `windowType` lives on the checker; populated once from stdlib and reused.

**Placeholders**

- Task 5 step 2 says "find with grep" — that's a discovery step, not a placeholder; the next steps act on what it returns.
- Task 6 step 3 sketch ("adapt to the actual stdlib accessor") is unavoidable: I haven't read the stdlib loader's exact API. The sketch is concrete enough that a developer can locate the field by grep.
- Task 7 step 4 references `windowStructValue` and `lookupSymbol` as helpers to write. The shape of the const-eval struct value is documented in `internal/optimize/consteval.go` — the implementing dev should mirror the convention there. This is the riskiest step; surface area is contained.

No "TBD"s, no "fill in later"s.
