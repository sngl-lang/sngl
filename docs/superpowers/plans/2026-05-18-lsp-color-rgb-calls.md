# LSP documentColor — color.rgb()/color.rgba() Calls Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the existing `textDocument/documentColor` and `textDocument/colorPresentation` handlers so `color.rgb(r, g, b)` and `color.rgba(r, g, b, a)` calls with literal integer arguments also surface as editable color swatches, in addition to `#hex` literals.

**Architecture:** Walk the AST for `*ast.CallExpr` whose `Func` is `color.rgb` / `color.rgba` (a `SelectExpr` over `IdentExpr{Name:"color"}`) and whose args are all `LiteralInt` literals in 0..255. Emit `ColorInformation` with a range spanning the whole call. `colorPresentation` inspects the source text at the input range to round-trip in the original form — hex stays hex, calls stay calls.

**Tech Stack:** Go, existing `internal/lsp` package, existing `ast`, `internal/lspcore`.

**Spec:** `docs/superpowers/specs/2026-05-18-lsp-previews-design.md` §F2 ("Out of scope (v1): `color.rgb(...)` function calls"). This plan ships that v2 follow-up.

---

## File Structure

| File                         | Status | Responsibility                                                                                                                                      |
|------------------------------|--------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `internal/lsp/color.go`      | modify | Add `ColorSource` tagged-union, extend walker to find color calls, update `computeDocumentColors`, update `computeColorPresentations` to round-trip |
| `internal/lsp/color_test.go` | modify | Add tests for rgb/rgba detection and round-trip presentations                                                                                       |
| `testdata/lsp/colors.sngl`   | modify | Extend fixture with `color.rgb(...)` and `color.rgba(...)` cases                                                                                    |

---

## Task 1: Fixture — add rgb/rgba call cases

**Files:**
- Modify: `testdata/lsp/colors.sngl`

- [ ] **Step 1: Replace fixture content**

Replace `testdata/lsp/colors.sngl` with:

```sngl
window #demo(title="Demo", href="/") {
    vbox(style={gap = 8, padding = 16}) {
        text(value="hi", style={color = #ff0000})
        text(value="hi", style={color = #00ff00})
        vbox(style={background = #aabbcc}) {}
        vbox(style={background = #11223344}) {}
        var label = "hex value #cafe in string"
        var fromCall = color.rgb(255, 128, 64)
        var withAlpha = color.rgba(10, 20, 30, 200)
    }
}

// comment with #abcdef should not match
```

- [ ] **Step 2: Run the existing test, expect failure**

Run: `go test ./internal/lsp/ -run TestDocumentColor -v`
Expected: FAIL. The existing test asserts exactly 4 colors (the hex literals); the fixture now has 2 more call-based colors, so `len(got) != 4` will fail because the parser still parses the new lines and the count check breaks.

Actually — until the implementation lands, the new call-based forms are NOT detected, so `len(got)` stays at 4 and the test would PASS. But the count is the regression we want to catch: when the implementation lands, the test must expand to 6.

So **first** update the test expectations in Task 2. For this task just commit the fixture and note the test will be expanded next task.

- [ ] **Step 3: Commit**

```bash
git add testdata/lsp/colors.sngl
git commit -m "lsp(color): fixture adds color.rgb/rgba call cases"
```

---

## Task 2: Detect color.rgb / color.rgba calls

**Files:**
- Modify: `internal/lsp/color.go`
- Modify: `internal/lsp/color_test.go`

- [ ] **Step 1: Extend the documentColor test**

Edit `internal/lsp/color_test.go`. Find `TestDocumentColor` and replace its body with:

```go
func TestDocumentColor(t *testing.T) {
	src, err := os.ReadFile("../../testdata/lsp/colors.sngl")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := parser.Parse("colors.sngl", string(src))
	got := computeDocumentColors(string(src), doc)

	want := []ColorInformation{
		// #ff0000
		{Range: Range{Start: Position{Line: 1, Character: 23}, End: Position{Line: 1, Character: 30}}, Color: Color{Red: 1, Green: 0, Blue: 0, Alpha: 1}},
		// #00ff00
		{Range: Range{Start: Position{Line: 2, Character: 23}, End: Position{Line: 2, Character: 30}}, Color: Color{Red: 0, Green: 1, Blue: 0, Alpha: 1}},
		// #aabbcc
		{Range: Range{Start: Position{Line: 3, Character: 13}, End: Position{Line: 3, Character: 20}}, Color: Color{Red: 0xaa / 255.0, Green: 0xbb / 255.0, Blue: 0xcc / 255.0, Alpha: 1}},
		// #11223344
		{Range: Range{Start: Position{Line: 4, Character: 13}, End: Position{Line: 4, Character: 22}}, Color: Color{Red: 0x11 / 255.0, Green: 0x22 / 255.0, Blue: 0x33 / 255.0, Alpha: 0x44 / 255.0}},
		// color.rgb(255, 128, 64)
		{Range: rangeOfSubstring(t, string(src), "color.rgb(255, 128, 64)"), Color: Color{Red: 1, Green: 128.0 / 255.0, Blue: 64.0 / 255.0, Alpha: 1}},
		// color.rgba(10, 20, 30, 200)
		{Range: rangeOfSubstring(t, string(src), "color.rgba(10, 20, 30, 200)"), Color: Color{Red: 10.0 / 255.0, Green: 20.0 / 255.0, Blue: 30.0 / 255.0, Alpha: 200.0 / 255.0}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d colors, want %d:\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i, w := range want {
		g := got[i]
		if g.Range != w.Range {
			t.Errorf("color %d range: got %+v, want %+v", i, g.Range, w.Range)
		}
		if !floatEq(g.Color.Red, w.Color.Red) || !floatEq(g.Color.Green, w.Color.Green) || !floatEq(g.Color.Blue, w.Color.Blue) || !floatEq(g.Color.Alpha, w.Color.Alpha) {
			t.Errorf("color %d value: got %+v, want %+v", i, g.Color, w.Color)
		}
	}
}

// rangeOfSubstring returns the LSP Range for the first occurrence of needle in src.
func rangeOfSubstring(t *testing.T, src, needle string) Range {
	t.Helper()
	idx := strings.Index(src, needle)
	if idx < 0 {
		t.Fatalf("substring %q not found in source", needle)
	}
	// Compute line/column (0-based, LSP style)
	line := 0
	col := 0
	for i := 0; i < idx; i++ {
		if src[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return Range{
		Start: Position{Line: line, Character: col},
		End:   Position{Line: line, Character: col + len(needle)},
	}
}
```

Add `"strings"` to the imports at the top of `internal/lsp/color_test.go` if absent.

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lsp/ -run TestDocumentColor -v`
Expected: FAIL with `got 4 colors, want 6` because the rgb/rgba calls aren't detected yet.

- [ ] **Step 3: Add the call walker and update computeDocumentColors**

In `internal/lsp/color.go`, find `computeDocumentColors` and replace it with:

```go
// computeDocumentColors walks the parsed document and returns a
// ColorInformation entry for every `#hex` color literal and every
// color.rgb(...) / color.rgba(...) call with all-int-literal args.
func computeDocumentColors(content string, doc *ast.Document) []ColorInformation {
	out := []ColorInformation{}
	if doc == nil {
		return out
	}
	lspcore.WalkLiterals(doc, func(lit *ast.LiteralExpr) {
		if lit.Kind != ast.LiteralColor {
			return
		}
		c, ok := parseHexColor(lit.Raw)
		if !ok {
			return
		}
		startLine := lit.Pos.Line - 1
		startCol := lit.Pos.Column - 1
		out = append(out, ColorInformation{
			Range: Range{
				Start: Position{Line: startLine, Character: startCol},
				End:   Position{Line: startLine, Character: startCol + len(lit.Raw)},
			},
			Color: c,
		})
	})

	walkColorCalls(doc, content, func(call *ast.CallExpr, c Color, r Range) {
		out = append(out, ColorInformation{Range: r, Color: c})
	})

	sortColorInformation(out)
	return out
}

// sortColorInformation orders entries by (line, column) so test expectations
// and editor displays are stable regardless of walk order.
func sortColorInformation(out []ColorInformation) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Range.Start.Line != out[j].Range.Start.Line {
			return out[i].Range.Start.Line < out[j].Range.Start.Line
		}
		return out[i].Range.Start.Character < out[j].Range.Start.Character
	})
}
```

Add `"sort"` to the imports of `internal/lsp/color.go` if absent.

Append the call walker to `internal/lsp/color.go`:

```go
// walkColorCalls invokes fn for every well-formed color.rgb(r,g,b) or
// color.rgba(r,g,b,a) call in the document. Args must be integer literals
// in 0..255 — anything else (variables, arithmetic, color.lighten, etc.)
// is silently skipped.
func walkColorCalls(doc *ast.Document, content string, fn func(*ast.CallExpr, Color, Range)) {
	walkCallExprs(doc, func(call *ast.CallExpr) {
		sel, ok := call.Func.(*ast.SelectExpr)
		if !ok {
			return
		}
		ident, ok := sel.Operand.(*ast.IdentExpr)
		if !ok || ident.Name != "color" {
			return
		}
		var wantArgs int
		switch sel.Field {
		case "rgb":
			wantArgs = 3
		case "rgba":
			wantArgs = 4
		default:
			return
		}
		if len(call.Args.Args) != wantArgs {
			return
		}
		ints := make([]int, 0, wantArgs)
		for _, a := range call.Args.Args {
			arg, ok := a.(ast.Arg)
			if !ok || arg.Name != "" {
				return
			}
			lit, ok := arg.Value.(*ast.LiteralExpr)
			if !ok || lit.Kind != ast.LiteralInt {
				return
			}
			n, err := strconv.Atoi(lit.Raw)
			if err != nil || n < 0 || n > 255 {
				return
			}
			ints = append(ints, n)
		}
		c := Color{
			Red:   float64(ints[0]) / 255.0,
			Green: float64(ints[1]) / 255.0,
			Blue:  float64(ints[2]) / 255.0,
			Alpha: 1.0,
		}
		if wantArgs == 4 {
			c.Alpha = float64(ints[3]) / 255.0
		}
		rng, ok := callRange(call, content)
		if !ok {
			return
		}
		fn(call, c, rng)
	})
}

// callRange computes the LSP Range covering a CallExpr from its starting
// position to the matching close-paren on the same logical span.
func callRange(call *ast.CallExpr, content string) (Range, bool) {
	startLine := call.Pos.Line - 1
	startCol := call.Pos.Column - 1
	endLine, endCol, ok := scanCloseParen(content, call.Pos.Line, call.Pos.Column)
	if !ok {
		return Range{}, false
	}
	return Range{
		Start: Position{Line: startLine, Character: startCol},
		End:   Position{Line: endLine - 1, Character: endCol},
	}, true
}

// scanCloseParen walks `content` starting at the given 1-based line/column
// (the start of a callee identifier), finds the first '(' that begins the
// arg list, then returns the 1-based line and 0-based char-after-')' of the
// matching close paren. Tracks nested parens; ignores string contents.
func scanCloseParen(content string, line, col int) (endLine, endChar int, ok bool) {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return 0, 0, false
	}
	depth := 0
	seenOpen := false
	inString := false
	var stringQuote byte
	curLine := line
	curIdx := col - 1
	for curLine <= len(lines) {
		l := lines[curLine-1]
		for curIdx < len(l) {
			ch := l[curIdx]
			if inString {
				if ch == '\\' && curIdx+1 < len(l) {
					curIdx += 2
					continue
				}
				if ch == stringQuote {
					inString = false
				}
				curIdx++
				continue
			}
			switch ch {
			case '"', '\'', '`':
				inString = true
				stringQuote = ch
			case '(':
				depth++
				seenOpen = true
			case ')':
				depth--
				if seenOpen && depth == 0 {
					return curLine, curIdx + 1, true
				}
			}
			curIdx++
		}
		curLine++
		curIdx = 0
	}
	return 0, 0, false
}
```

- [ ] **Step 4: Add the `walkCallExprs` helper**

Append to `internal/lsp/color.go`:

```go
// walkCallExprs invokes fn for every CallExpr in the document.
// Mirrors the structure of lspcore.WalkLiterals but for CallExpr.
func walkCallExprs(doc *ast.Document, fn func(*ast.CallExpr)) {
	var walkE func(e ast.Expr)
	var walkS func(s ast.Stmt)
	var walkBlock func(b ast.StmtBlock)
	var walkArgs func(args ast.ArgList)
	walkE = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
			return
		case *ast.CallExpr:
			fn(x)
			walkE(x.Func)
			walkArgs(x.Args)
		case *ast.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *ast.UnaryExpr:
			walkE(x.Operand)
		case *ast.SelectExpr:
			walkE(x.Operand)
		case *ast.IndexExpr:
			walkE(x.Operand)
			walkE(x.Index)
		case *ast.TernaryExpr:
			walkE(x.Cond)
			walkE(x.Then)
			walkE(x.Else)
		case *ast.ListExpr:
			for _, el := range x.Elements {
				walkE(el)
			}
		case *ast.StructExpr:
			for _, f := range x.Fields {
				walkE(f.Value)
			}
		case *ast.LambdaExpr:
			walkBlock(x.Block)
			walkE(x.Body)
		case *ast.InterpolationExpr:
			for _, p := range x.Parts {
				walkE(p)
			}
		case *ast.ParenExpr:
			walkE(x.Inner)
		}
	}
	walkArgs = func(args ast.ArgList) {
		for _, a := range args.Args {
			switch ar := a.(type) {
			case ast.Arg:
				walkE(ar.Value)
			case ast.EventHandler:
				walkBlock(ar.Body)
			}
		}
	}
	walkBlock = func(b ast.StmtBlock) {
		for _, s := range b.Stmts {
			walkS(s)
		}
	}
	walkS = func(s ast.Stmt) {
		switch x := s.(type) {
		case nil:
			return
		case *ast.VarDecl:
			for _, sp := range x.Specs {
				walkE(sp.Default)
			}
		case *ast.ConstDecl:
			for _, sp := range x.Specs {
				walkE(sp.Default)
			}
		case *ast.AssignStmt:
			walkE(x.Value)
		case *ast.EmitStmt:
			walkArgs(x.Args)
		case *ast.IfStmt:
			walkE(x.Cond)
			walkBlock(x.Then)
			walkBlock(x.Else)
		case *ast.ForStmt:
			walkE(x.Iter)
			walkBlock(x.Body)
		case *ast.VisualNode:
			walkArgs(x.Args)
			walkBlock(x.Block)
		case *ast.ComponentDecl:
			walkBlock(x.Block)
		case *ast.FuncDef:
			walkE(x.Body)
			walkBlock(x.Block)
		case *ast.PlatformStmt:
			walkBlock(x.Block)
		case *ast.ReturnStmt:
			walkE(x.Value)
		case *ast.VarStmt:
			walkE(x.Init)
		}
	}
	for _, s := range doc.Stmts {
		walkS(s)
	}
}
```

**IMPORTANT:** the exact field names of `ast.LambdaExpr`, `ast.ParenExpr`, `ast.EventHandler`, `ast.VisualNode`, `ast.ComponentDecl`, `ast.FuncDef`, `ast.PlatformStmt`, `ast.ReturnStmt`, `ast.VarStmt`, `ast.IfStmt`, `ast.ForStmt`, `ast.AssignStmt`, `ast.EmitStmt` are what F2's walker discovered. Before writing this code, run:

```bash
grep -nE "^type (LambdaExpr|ParenExpr|EventHandler|VisualNode|ComponentDecl|FuncDef|PlatformStmt|ReturnStmt|VarStmt|IfStmt|ForStmt|AssignStmt|EmitStmt) " ast/*.go
```

If any struct's field doesn't match (e.g. `EventHandler.Block` vs `.Body`), adapt to match the real AST. The fixture test will catch missed cases — if a color call lives inside an unhandled node, you'll see `got 4 want 6`.

Alternative shortcut: if the existing `lspcore.WalkLiterals` already traverses every node container, copy its body and replace the `*ast.LiteralExpr` callback with a `*ast.CallExpr` callback. This is the recommended path — preserve F2's verified field-name discoveries.

- [ ] **Step 5: Run, verify pass**

Run: `go test ./internal/lsp/ -run TestDocumentColor -v`
Expected: PASS with 6 colors.

If FAIL on count, the walker is missing a node type — fix by comparing with `lspcore.WalkLiterals` in `internal/lspcore/walk.go`. If FAIL on range, `scanCloseParen` is off — debug with a `t.Logf` showing what range it produced.

- [ ] **Step 6: Run full lsp tests**

Run: `go test ./internal/lsp/...`
Expected: PASS, including `TestE2EDocumentColor` (still asserts one red color for a `#ff0000`-only doc).

- [ ] **Step 7: Commit**

```bash
git add internal/lsp/color.go internal/lsp/color_test.go
git commit -m "lsp(color): detect color.rgb/rgba calls as document colors"
```

---

## Task 3: colorPresentation round-trip honoring original form

Today's `computeColorPresentations` only takes a `Color` and returns `#rrggbb` or `#rrggbbaa`. With call-based colors, picking a new color from the swatch should rewrite the call (not replace it with hex). The handler needs access to the source text at the input range to detect the original form.

**Files:**
- Modify: `internal/lsp/color.go`
- Modify: `internal/lsp/color_test.go`

- [ ] **Step 1: Add failing tests**

Append to `internal/lsp/color_test.go`:

```go
func TestColorPresentations_HonorsOriginal(t *testing.T) {
	tests := []struct {
		name   string
		source string // source text at the range
		color  Color
		want   string
	}{
		{"hex opaque", "#ff0000", Color{1, 0, 0, 1}, "#ff0000"},
		{"hex with alpha", "#ff0000", Color{1, 0, 0, 0.5}, "#ff000080"},
		{"rgb call opaque", "color.rgb(255, 0, 0)", Color{0, 1, 0, 1}, "color.rgb(0, 255, 0)"},
		{"rgb call gains alpha", "color.rgb(255, 0, 0)", Color{1, 0, 0, 0.5}, "color.rgba(255, 0, 0, 128)"},
		{"rgba call retains form", "color.rgba(10, 20, 30, 200)", Color{1, 1, 1, 1}, "color.rgba(255, 255, 255, 255)"},
		{"rgba call alpha=1", "color.rgba(10, 20, 30, 200)", Color{0, 0, 0, 1}, "color.rgba(0, 0, 0, 255)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeColorPresentationsForSource(tt.source, tt.color)
			if len(got) != 1 {
				t.Fatalf("got %d presentations, want 1", len(got))
			}
			if got[0].Label != tt.want {
				t.Errorf("label = %q, want %q", got[0].Label, tt.want)
			}
		})
	}
}
```

The existing `TestColorPresentations` is unchanged — `computeColorPresentations(c Color)` keeps its current behavior (returns hex) as the no-source fallback.

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lsp/ -run TestColorPresentations_HonorsOriginal -v`
Expected: FAIL with "undefined: computeColorPresentationsForSource".

- [ ] **Step 3: Implement source-aware formatter**

Append to `internal/lsp/color.go`:

```go
// computeColorPresentationsForSource picks a hex-or-call rendering based on
// the source text that the editor highlighted. Falls back to hex if the
// source doesn't begin with "color.".
func computeColorPresentationsForSource(source string, c Color) []ColorPresentation {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "color.rgba(") {
		return []ColorPresentation{{Label: formatColorRgbaCall(c)}}
	}
	if strings.HasPrefix(source, "color.rgb(") {
		if c.Alpha < 1.0 {
			// Caller gained alpha — promote to rgba so we don't silently drop it.
			return []ColorPresentation{{Label: formatColorRgbaCall(c)}}
		}
		return []ColorPresentation{{Label: formatColorRgbCall(c)}}
	}
	return computeColorPresentations(c)
}

func formatColorRgbCall(c Color) string {
	return fmt.Sprintf("color.rgb(%d, %d, %d)", clamp8(c.Red), clamp8(c.Green), clamp8(c.Blue))
}

func formatColorRgbaCall(c Color) string {
	return fmt.Sprintf("color.rgba(%d, %d, %d, %d)", clamp8(c.Red), clamp8(c.Green), clamp8(c.Blue), clamp8(c.Alpha))
}
```

`strings` and `fmt` are already imported. `clamp8` is defined earlier in `color.go`.

- [ ] **Step 4: Wire `handleColorPresentation` to use the source-aware variant**

In `internal/lsp/color.go`, replace `handleColorPresentation` with:

```go
func (s *Server) handleColorPresentation(id json.RawMessage, params json.RawMessage) {
	var p ColorPresentationParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	source := ""
	if fs := s.ws.get(p.TextDocument.URI); fs != nil {
		source = sliceRange(fs.Content, p.Range)
	}
	s.sendResult(id, computeColorPresentationsForSource(source, p.Color))
}

// sliceRange extracts the substring of `content` covered by an LSP Range.
// Lines and characters are 0-based. Returns "" if the range is malformed.
func sliceRange(content string, r Range) string {
	lines := strings.Split(content, "\n")
	if r.Start.Line < 0 || r.Start.Line >= len(lines) {
		return ""
	}
	if r.End.Line < 0 || r.End.Line >= len(lines) {
		return ""
	}
	if r.Start.Line == r.End.Line {
		l := lines[r.Start.Line]
		if r.Start.Character < 0 || r.End.Character > len(l) || r.Start.Character > r.End.Character {
			return ""
		}
		return l[r.Start.Character:r.End.Character]
	}
	var sb strings.Builder
	sb.WriteString(lines[r.Start.Line][r.Start.Character:])
	for i := r.Start.Line + 1; i < r.End.Line; i++ {
		sb.WriteByte('\n')
		sb.WriteString(lines[i])
	}
	sb.WriteByte('\n')
	end := lines[r.End.Line]
	if r.End.Character > len(end) {
		return ""
	}
	sb.WriteString(end[:r.End.Character])
	return sb.String()
}
```

- [ ] **Step 5: Run all color tests**

Run: `go test ./internal/lsp/ -run "TestColorPresentations|TestDocumentColor" -v`
Expected: PASS for both the existing hex-only `TestColorPresentations` and the new `TestColorPresentations_HonorsOriginal`.

- [ ] **Step 6: Full regression**

Run: `go test ./internal/lsp/...`
Expected: PASS, including `TestE2EDocumentColor`.

- [ ] **Step 7: Commit**

```bash
git add internal/lsp/color.go internal/lsp/color_test.go
git commit -m "lsp(color): colorPresentation round-trips call form when present"
```

---

## Task 4: Rebuild binary

**Files:** none

- [ ] **Step 1: Rebuild**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 2: Manual smoke**

`:LspRestart` in nvim. Open a `.sngl` file with `color.rgb(255, 0, 0)` — expect a swatch next to the call. Try clicking the swatch in an editor that supports color-picker workflows (VS Code, some nvim plugins); confirm replacement preserves call form.

---

## Self-Review

**Spec coverage (§F2 v2):**
- ✅ Detect `color.rgb(r, g, b)` calls — Task 2.
- ✅ Detect `color.rgba(r, g, b, a)` calls — Task 2.
- ✅ Skip non-literal-int args (variables, arithmetic) — Task 2 (`walkColorCalls` rejects non-`LiteralInt`).
- ✅ Skip out-of-range args (>255) — Task 2 (`n < 0 || n > 255` check).
- ✅ Skip unrelated `color.*` methods (lighten, darken, opacity) — Task 2 (`switch sel.Field` only matches rgb/rgba).
- ✅ Round-trip in original form — Task 3.
- ✅ Promote rgb to rgba when alpha gained — Task 3 (`if c.Alpha < 1.0` branch).

**Placeholder scan:** none. Every code step shows full code.

**Type consistency:** `Color`, `ColorInformation`, `Range`, `Position`, `ColorPresentation`, `ColorPresentationParams` defined earlier; reused unchanged. New helpers: `walkColorCalls`, `walkCallExprs`, `callRange`, `scanCloseParen`, `sortColorInformation`, `computeColorPresentationsForSource`, `formatColorRgbCall`, `formatColorRgbaCall`, `sliceRange`. All declared once and referenced consistently. `handleColorPresentation` signature unchanged; its body updated.
