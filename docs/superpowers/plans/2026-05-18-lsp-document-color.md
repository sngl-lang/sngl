# LSP Document Color Swatches Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `textDocument/documentColor` and `textDocument/colorPresentation` in `sngl lsp` so editors can render color swatches next to `#hex` color literals.

**Architecture:** Walk the parsed document for `ast.LiteralExpr{Kind: LiteralColor}` nodes; for each, parse the raw `#rgb` / `#rrggbb` / `#rrggbbaa` literal into LSP `Color{red,green,blue,alpha}` (0..1 floats) and return its source range. `colorPresentation` formats the picked color back to a hex string. No new dependencies.

**Tech Stack:** Go, existing `internal/lsp` package, existing `ast` and `internal/parser` packages.

**Spec:** `docs/superpowers/specs/2026-05-18-lsp-previews-design.md` §F2.

---

## File Structure

| File                         | Status | Responsibility                                                                                     |
|------------------------------|--------|----------------------------------------------------------------------------------------------------|
| `internal/lsp/color.go`      | create | `documentColor` + `colorPresentation` handlers; hex parsing and formatting                         |
| `internal/lsp/color_test.go` | create | Table-driven tests over fixture .sngl files                                                        |
| `internal/lsp/protocol.go`   | modify | Add `ColorProvider` capability flag + DocumentColor/ColorInformation/Color/ColorPresentation types |
| `internal/lsp/handler.go`    | modify | Advertise `colorProvider: true` in initialize                                                      |
| `internal/lsp/server.go`     | modify | Dispatch `textDocument/documentColor` and `textDocument/colorPresentation`                         |
| `testdata/lsp/colors.sngl`   | create | Fixture with hex literals across forms                                                             |

---

## Task 1: Add protocol types

**Files:**
- Modify: `internal/lsp/protocol.go`

- [ ] **Step 1: Add `ColorProvider` capability to `ServerCapabilities`**

Edit `internal/lsp/protocol.go`. Find the `ServerCapabilities` struct (around line 96) and add the field:

```go
type ServerCapabilities struct {
	TextDocumentSync       int                    `json:"textDocumentSync"` // 1=Full
	HoverProvider          bool                   `json:"hoverProvider,omitempty"`
	CompletionProvider     *CompletionOptions     `json:"completionProvider,omitempty"`
	DiagnosticProvider     *DiagnosticOptions     `json:"diagnosticProvider,omitempty"`
	SemanticTokensProvider *SemanticTokensOptions `json:"semanticTokensProvider,omitempty"`
	ColorProvider          bool                   `json:"colorProvider,omitempty"`
}
```

- [ ] **Step 2: Append documentColor and colorPresentation types at end of file**

Append to `internal/lsp/protocol.go`:

```go
// --- Document Color ---

type DocumentColorParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type ColorInformation struct {
	Range Range `json:"range"`
	Color Color `json:"color"`
}

type Color struct {
	Red   float64 `json:"red"`
	Green float64 `json:"green"`
	Blue  float64 `json:"blue"`
	Alpha float64 `json:"alpha"`
}

type ColorPresentationParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Color        Color                  `json:"color"`
	Range        Range                  `json:"range"`
}

type ColorPresentation struct {
	Label string `json:"label"`
}
```

- [ ] **Step 3: Verify compile**

Run: `go build ./internal/lsp/...`
Expected: success, no output.

- [ ] **Step 4: Commit**

```bash
git add internal/lsp/protocol.go
git commit -m "lsp(protocol): add documentColor capability and types"
```

---

## Task 2: Hex parsing helper with tests

**Files:**
- Create: `internal/lsp/color.go`
- Create: `internal/lsp/color_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/lsp/color_test.go`:

```go
package lsp

import (
	"math"
	"testing"
)

func TestParseHexColor(t *testing.T) {
	tests := []struct {
		in                         string
		wantR, wantG, wantB, wantA float64
		wantOK                     bool
	}{
		{"#000", 0, 0, 0, 1, true},
		{"#fff", 1, 1, 1, 1, true},
		{"#f00", 1, 0, 0, 1, true},
		{"#000000", 0, 0, 0, 1, true},
		{"#ffffff", 1, 1, 1, 1, true},
		{"#ff8040", 1, 128.0 / 255.0, 64.0 / 255.0, 1, true},
		{"#00000000", 0, 0, 0, 0, true},
		{"#ff000080", 1, 0, 0, 128.0 / 255.0, true},
		{"#abc", 0xaa / 255.0, 0xbb / 255.0, 0xcc / 255.0, 1, true},
		{"", 0, 0, 0, 0, false},
		{"#", 0, 0, 0, 0, false},
		{"#xyz", 0, 0, 0, 0, false},
		{"#fffff", 0, 0, 0, 0, false}, // 5 digits not valid
		{"#ffffffffff", 0, 0, 0, 0, false},
		{"abc", 0, 0, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			c, ok := parseHexColor(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if !floatEq(c.Red, tt.wantR) || !floatEq(c.Green, tt.wantG) || !floatEq(c.Blue, tt.wantB) || !floatEq(c.Alpha, tt.wantA) {
				t.Fatalf("got {%v %v %v %v}, want {%v %v %v %v}", c.Red, c.Green, c.Blue, c.Alpha, tt.wantR, tt.wantG, tt.wantB, tt.wantA)
			}
		})
	}
}

func TestFormatHexColor(t *testing.T) {
	tests := []struct {
		c    Color
		want string
	}{
		{Color{1, 0, 0, 1}, "#ff0000"},
		{Color{0, 0, 0, 1}, "#000000"},
		{Color{1, 1, 1, 1}, "#ffffff"},
		{Color{1, 0, 0, 0.5}, "#ff000080"},
		{Color{1, 0, 0, 0}, "#ff000000"},
		{Color{128.0 / 255.0, 64.0 / 255.0, 32.0 / 255.0, 1}, "#804020"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatHexColor(tt.c)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func floatEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
```

- [ ] **Step 2: Run test, verify it fails (undefined functions)**

Run: `go test ./internal/lsp/ -run TestParseHexColor -v`
Expected: FAIL with "undefined: parseHexColor".

- [ ] **Step 3: Implement parser and formatter**

Create `internal/lsp/color.go`:

```go
package lsp

import "fmt"

// parseHexColor converts "#rgb", "#rrggbb", or "#rrggbbaa" to a Color.
// Alpha defaults to 1.0 when not specified.
func parseHexColor(s string) (Color, bool) {
	if len(s) == 0 || s[0] != '#' {
		return Color{}, false
	}
	hex := s[1:]
	switch len(hex) {
	case 3:
		r, ok1 := hexNibble(hex[0])
		g, ok2 := hexNibble(hex[1])
		b, ok3 := hexNibble(hex[2])
		if !(ok1 && ok2 && ok3) {
			return Color{}, false
		}
		return Color{
			Red:   float64(r*16+r) / 255.0,
			Green: float64(g*16+g) / 255.0,
			Blue:  float64(b*16+b) / 255.0,
			Alpha: 1.0,
		}, true
	case 6:
		r, ok1 := hexByte(hex[0], hex[1])
		g, ok2 := hexByte(hex[2], hex[3])
		b, ok3 := hexByte(hex[4], hex[5])
		if !(ok1 && ok2 && ok3) {
			return Color{}, false
		}
		return Color{
			Red:   float64(r) / 255.0,
			Green: float64(g) / 255.0,
			Blue:  float64(b) / 255.0,
			Alpha: 1.0,
		}, true
	case 8:
		r, ok1 := hexByte(hex[0], hex[1])
		g, ok2 := hexByte(hex[2], hex[3])
		b, ok3 := hexByte(hex[4], hex[5])
		a, ok4 := hexByte(hex[6], hex[7])
		if !(ok1 && ok2 && ok3 && ok4) {
			return Color{}, false
		}
		return Color{
			Red:   float64(r) / 255.0,
			Green: float64(g) / 255.0,
			Blue:  float64(b) / 255.0,
			Alpha: float64(a) / 255.0,
		}, true
	default:
		return Color{}, false
	}
}

// formatHexColor renders a Color as #rrggbb (alpha=1) or #rrggbbaa.
func formatHexColor(c Color) string {
	r := clamp8(c.Red)
	g := clamp8(c.Green)
	b := clamp8(c.Blue)
	if c.Alpha >= 1.0 {
		return fmt.Sprintf("#%02x%02x%02x", r, g, b)
	}
	a := clamp8(c.Alpha)
	return fmt.Sprintf("#%02x%02x%02x%02x", r, g, b, a)
}

func clamp8(v float64) int {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return int(v*255.0 + 0.5)
}

func hexNibble(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

func hexByte(hi, lo byte) (int, bool) {
	h, ok1 := hexNibble(hi)
	l, ok2 := hexNibble(lo)
	if !(ok1 && ok2) {
		return 0, false
	}
	return h*16 + l, true
}
```

- [ ] **Step 4: Run tests, verify they pass**

Run: `go test ./internal/lsp/ -run "TestParseHexColor|TestFormatHexColor" -v`
Expected: PASS, all subtests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/lsp/color.go internal/lsp/color_test.go
git commit -m "lsp(color): hex parser and formatter for documentColor"
```

---

## Task 3: documentColor handler

**Files:**
- Create: `testdata/lsp/colors.sngl`
- Modify: `internal/lsp/color.go` (add handler)
- Modify: `internal/lsp/color_test.go` (add handler test)

- [ ] **Step 1: Create fixture file**

Create `testdata/lsp/colors.sngl`:

```sngl
window #demo(title="Demo", href="/") {
    vbox(style={gap = 8, padding = 16}) {
        text(value="hi", style={color = #ff0000})
        text(value="hi", style={color = #00ff00})
        vbox(style={background = #aabbcc}) {}
        vbox(style={background = #11223344}) {}
        var label = "hex value #cafe in string"
    }
}

// comment with #abcdef should not match
```

- [ ] **Step 2: Write failing handler test**

Add to `internal/lsp/color_test.go`:

```go
func TestDocumentColor(t *testing.T) {
	src, err := os.ReadFile("../../testdata/lsp/colors.sngl")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := parser.Parse("colors.sngl", string(src))
	got := computeDocumentColors(string(src), doc)

	want := []ColorInformation{
		{Range: Range{Start: Position{Line: 1, Character: 24}, End: Position{Line: 1, Character: 31}}, Color: Color{Red: 1, Green: 0, Blue: 0, Alpha: 1}},
		{Range: Range{Start: Position{Line: 2, Character: 24}, End: Position{Line: 2, Character: 31}}, Color: Color{Red: 0, Green: 1, Blue: 0, Alpha: 1}},
		{Range: Range{Start: Position{Line: 3, Character: 13}, End: Position{Line: 3, Character: 17}}, Color: Color{Red: 0xaa / 255.0, Green: 0xbb / 255.0, Blue: 0xcc / 255.0, Alpha: 1}},
		{Range: Range{Start: Position{Line: 4, Character: 13}, End: Position{Line: 4, Character: 22}}, Color: Color{Red: 0x11 / 255.0, Green: 0x22 / 255.0, Blue: 0x33 / 255.0, Alpha: 0x44 / 255.0}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d colors, want %d: %+v", len(got), len(want), got)
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
```

Add imports to top of `color_test.go`: `"os"` and `"git.duckfam.us/jonathan/sngl/internal/parser"`.

- [ ] **Step 3: Run test, verify it fails**

Run: `go test ./internal/lsp/ -run TestDocumentColor -v`
Expected: FAIL with "undefined: computeDocumentColors".

- [ ] **Step 4: Implement handler**

Append to `internal/lsp/color.go`:

```go
import (
	"encoding/json"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Replace the existing `import "fmt"` line at the top of the file with the
// combined import block above (fmt + encoding/json + ast).

// computeDocumentColors walks the parsed document and returns a
// ColorInformation entry for every `#hex` color literal.
func computeDocumentColors(content string, doc *ast.Document) []ColorInformation {
	out := []ColorInformation{}
	if doc == nil {
		return out
	}
	walkLiterals(doc, func(lit *ast.LiteralExpr) {
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
	return out
}

func (s *Server) handleDocumentColor(id json.RawMessage, params json.RawMessage) {
	var p DocumentColorParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	fs := s.ws.get(p.TextDocument.URI)
	if fs == nil || fs.Doc == nil {
		s.sendResult(id, []ColorInformation{})
		return
	}
	s.sendResult(id, computeDocumentColors(fs.Content, fs.Doc))
}
```

- [ ] **Step 5: Add the AST walker**

The `ast` package has no general walker. Add a local one to `internal/lsp/color.go`:

```go
// walkLiterals invokes fn for every LiteralExpr in the document.
// Minimal walker scoped to what documentColor needs.
func walkLiterals(doc *ast.Document, fn func(*ast.LiteralExpr)) {
	var walkE func(e ast.Expr)
	var walkS func(s ast.Stmt)
	walkE = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
			return
		case *ast.LiteralExpr:
			fn(x)
		case *ast.BinaryExpr:
			walkE(x.Left)
			walkE(x.Right)
		case *ast.UnaryExpr:
			walkE(x.Operand)
		case *ast.CallExpr:
			walkE(x.Func)
			for _, a := range x.Args {
				walkE(a)
			}
		case *ast.SelectExpr:
			walkE(x.Receiver)
		case *ast.IndexExpr:
			walkE(x.Receiver)
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
			for _, st := range x.Body {
				walkS(st)
			}
		case *ast.InterpolationExpr:
			for _, p := range x.Parts {
				walkE(p)
			}
		}
	}
	walkS = func(s ast.Stmt) {
		switch x := s.(type) {
		case nil:
			return
		case *ast.VarDecl:
			walkE(x.Value)
		case *ast.ConstDecl:
			walkE(x.Value)
		case *ast.AssignStmt:
			walkE(x.Value)
		case *ast.EmitStmt:
			walkE(x.Value)
		case *ast.StmtBlock:
			for _, c := range x.Stmts {
				walkS(c)
			}
		case *ast.IfStmt:
			walkE(x.Cond)
			for _, c := range x.Then {
				walkS(c)
			}
			for _, c := range x.Else {
				walkS(c)
			}
		case *ast.ForStmt:
			walkE(x.Iter)
			for _, c := range x.Body {
				walkS(c)
			}
		case *ast.VisualNode:
			for _, p := range x.Props {
				walkE(p.Value)
			}
			for _, child := range x.Children {
				walkS(child)
			}
		case *ast.ComponentDecl:
			for _, c := range x.Body {
				walkS(c)
			}
		case *ast.FuncDef:
			for _, c := range x.Body {
				walkS(c)
			}
		case *ast.PlatformStmt:
			for _, c := range x.Body {
				walkS(c)
			}
		}
	}
	for _, st := range doc.Stmts {
		walkS(st)
	}
}
```

Before writing, run this to confirm field names match the current ast package:

Run: `grep -nE "^type (BinaryExpr|UnaryExpr|CallExpr|SelectExpr|IndexExpr|TernaryExpr|ListExpr|StructExpr|LambdaExpr|InterpolationExpr|AssignStmt|EmitStmt|StmtBlock|IfStmt|ForStmt|VisualNode|ComponentDecl|FuncDef|VarDecl|ConstDecl|PlatformStmt) " ast/*.go`

If any struct field used in the walker differs (e.g. `x.Operand` vs `x.Expr`), adjust the walker to match. The fixture test in Step 6 will catch most mistakes.

- [ ] **Step 6: Run handler test**

Run: `go test ./internal/lsp/ -run TestDocumentColor -v`
Expected: PASS. If column offsets fail, adjust the fixture's expected ranges to match actual `lit.Pos.Column - 1` values — the parser is authoritative for positions.

- [ ] **Step 7: Run full lsp tests**

Run: `go test ./internal/lsp/ -v`
Expected: all tests pass, including previously existing ones.

- [ ] **Step 8: Commit**

```bash
git add internal/lsp/color.go internal/lsp/color_test.go testdata/lsp/colors.sngl
git commit -m "lsp(color): documentColor handler and AST literal walker"
```

---

## Task 4: colorPresentation handler

**Files:**
- Modify: `internal/lsp/color.go`
- Modify: `internal/lsp/color_test.go`

- [ ] **Step 1: Write failing test**

Add to `internal/lsp/color_test.go`:

```go
func TestColorPresentations(t *testing.T) {
	tests := []struct {
		name string
		c    Color
		want string
	}{
		{"opaque red", Color{1, 0, 0, 1}, "#ff0000"},
		{"semi red", Color{1, 0, 0, 0.5}, "#ff000080"},
		{"black", Color{0, 0, 0, 1}, "#000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeColorPresentations(tt.c)
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

- [ ] **Step 2: Run, verify it fails**

Run: `go test ./internal/lsp/ -run TestColorPresentations -v`
Expected: FAIL with "undefined: computeColorPresentations".

- [ ] **Step 3: Implement**

Append to `internal/lsp/color.go`:

```go
func computeColorPresentations(c Color) []ColorPresentation {
	return []ColorPresentation{{Label: formatHexColor(c)}}
}

func (s *Server) handleColorPresentation(id json.RawMessage, params json.RawMessage) {
	var p ColorPresentationParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	s.sendResult(id, computeColorPresentations(p.Color))
}
```

- [ ] **Step 4: Run test**

Run: `go test ./internal/lsp/ -run TestColorPresentations -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lsp/color.go internal/lsp/color_test.go
git commit -m "lsp(color): colorPresentation handler"
```

---

## Task 5: Wire handlers into server and advertise capability

**Files:**
- Modify: `internal/lsp/handler.go`
- Modify: `internal/lsp/server.go`

- [ ] **Step 1: Advertise capability**

Edit `internal/lsp/handler.go`. In `handleInitialize`, add `ColorProvider: true` to the `ServerCapabilities` struct literal:

```go
Capabilities: ServerCapabilities{
	TextDocumentSync: 1,
	HoverProvider:    true,
	CompletionProvider: &CompletionOptions{
		TriggerCharacters: []string{".", "@", "("},
	},
	SemanticTokensProvider: &SemanticTokensOptions{
		Legend: SemanticTokensLegend{
			TokenTypes:     SemanticTokenTypes(),
			TokenModifiers: []string{},
		},
		Full: true,
	},
	ColorProvider: true,
},
```

- [ ] **Step 2: Dispatch new methods**

Edit `internal/lsp/server.go`. In the `switch req.Method` block, add cases before the `default:` line:

```go
case "textDocument/documentColor":
	s.handleDocumentColor(req.ID, req.Params)
case "textDocument/colorPresentation":
	s.handleColorPresentation(req.ID, req.Params)
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 4: Run all tests**

Run: `go test ./internal/lsp/...`
Expected: PASS.

- [ ] **Step 5: Manual smoke test via existing server_test.go pattern**

Open `internal/lsp/server_test.go` and confirm the existing test helpers (`startTestServer`, `sendRequest`) are present. Add an end-to-end test:

```go
func TestE2EDocumentColor(t *testing.T) {
	srv, cleanup := startTestServer(t)
	defer cleanup()

	srv.sendRequest(t, 1, "initialize", InitializeParams{RootURI: "file:///tmp"})
	srv.expectResult(t, 1)

	srv.sendNotify(t, "textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        "file:///tmp/x.sngl",
			LanguageID: "sngl",
			Version:    1,
			Text:       "window W {\n    Text(\"x\", color = #ff0000)\n}\n",
		},
	})

	srv.sendRequest(t, 2, "textDocument/documentColor", DocumentColorParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///tmp/x.sngl"},
	})
	res := srv.expectResult(t, 2)

	var colors []ColorInformation
	if err := json.Unmarshal(res, &colors); err != nil {
		t.Fatal(err)
	}
	if len(colors) != 1 {
		t.Fatalf("got %d colors, want 1", len(colors))
	}
	if !floatEq(colors[0].Color.Red, 1) || !floatEq(colors[0].Color.Green, 0) {
		t.Fatalf("got %+v, want red", colors[0].Color)
	}
}
```

If `server_test.go` uses different helper names, adapt to match — the existing tests demonstrate the call conventions. Add `import "encoding/json"` if missing.

Run: `go test ./internal/lsp/ -run TestE2EDocumentColor -v`
Expected: PASS.

If the existing test scaffolding can't be reused as-is, skip the e2e test and rely on the unit tests from Task 3. Note this in the commit message.

- [ ] **Step 6: Commit**

```bash
git add internal/lsp/handler.go internal/lsp/server.go internal/lsp/server_test.go
git commit -m "lsp: dispatch documentColor and colorPresentation, advertise capability"
```

---

## Task 6: Rebuild binary and verify in editor

**Files:** none

- [ ] **Step 1: Rebuild**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 2: Verify nvim picks it up**

Open any `.sngl` file with hex colors in Neovim. Confirm color swatches render inline (provided by `vim.lsp` documentColor support and a colorizer plugin like `nvim-colorizer.lua` or built-in `:set termguicolors` + LSP color highlights — exact UX depends on user config).

If nothing shows: run `:lua =vim.lsp.get_clients()[1].server_capabilities.colorProvider` — should print `true`. If `nil`, the binary is stale; rebuild and `:LspRestart`.

- [ ] **Step 3 (optional): Commit any docs touch-ups**

If the user wants a changelog entry for this slice, add to `CHANGELOG.md` or equivalent — none exists today, so skip unless requested.

---

## Self-Review

**Spec coverage (F2):**
- ✅ Advertise `colorProvider: true` — Task 5.
- ✅ Walk AST for `#hex` literals — Task 3 walker.
- ✅ Parse `#rgb`, `#rrggbb`, `#rrggbbaa` — Task 2.
- ✅ Return `ColorInformation` with range and 0..1 floats — Task 3.
- ✅ `colorPresentation` returns single `#rrggbb` / `#rrggbbaa` — Task 4.
- ✅ `color.rgb(...)` calls explicitly excluded — not implemented (matches spec non-goal).

**Placeholder scan:** none.

**Type consistency:** `Color`, `ColorInformation`, `DocumentColorParams`, `ColorPresentationParams`, `ColorPresentation` defined in Task 1, used identically in Tasks 2–5. Helpers `parseHexColor`, `formatHexColor`, `computeDocumentColors`, `computeColorPresentations`, `walkLiterals`, `handleDocumentColor`, `handleColorPresentation` named once and referenced consistently.
