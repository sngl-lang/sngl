package snglparser_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/parser"
	"git.duckfam.us/jonathan/sngl/snglparser"
)

// TestParseFixtures parses each .sngl fixture file and compares the resulting
// AST against the AST produced by the KDL parser from the corresponding
// .sngl.kdl file.
func TestParseFixtures(t *testing.T) {
	dir := filepath.Join("..", "testdata")
	matches, err := filepath.Glob(filepath.Join(dir, "*.sngl.kdl"))
	if err != nil {
		t.Fatal(err)
	}

	for _, kdlPath := range matches {
		base := filepath.Base(kdlPath)
		// Skip error fixtures — they intentionally fail parsing/checking.
		if strings.HasPrefix(base, "error_") {
			continue
		}
		name := strings.TrimSuffix(base, ".sngl.kdl")
		snglPath := filepath.Join(dir, name+".sngl")

		t.Run(name, func(t *testing.T) {
			// Parse KDL fixture.
			kdlFile, err := os.Open(kdlPath)
			if err != nil {
				t.Fatal(err)
			}
			kdlDoc, err := parser.Parse(base, kdlFile)
			kdlFile.Close()
			if err != nil {
				t.Fatalf("KDL parse error: %v", err)
			}

			// Parse SNGL fixture.
			snglFile, err := os.Open(snglPath)
			if err != nil {
				t.Fatalf("missing .sngl fixture (run go run tmp/gensngl.go): %v", err)
			}
			snglDoc, err := snglparser.Parse(name+".sngl", snglFile)
			snglFile.Close()
			if err != nil {
				t.Fatalf("SNGL parse error: %v", err)
			}

			compareDocuments(t, kdlDoc, snglDoc)
		})
	}
}

// compareDocuments compares two Documents structurally, ignoring position info
// and the CEL/AST fields (which only the KDL parser populates).
func compareDocuments(t *testing.T, want, got *ast.Document) {
	t.Helper()

	compareImports(t, want.Imports, got.Imports)
	compareOutputs(t, want.Outputs, got.Outputs)
	compareStructs(t, want.Structs, got.Structs)
	compareEnums(t, want.Enums, got.Enums)
	compareConsts(t, want.Consts, got.Consts)
	compareData(t, want.Data, got.Data)
	compareComputeds(t, want.Computeds, got.Computeds)
	compareComponents(t, want.Components, got.Components)
	compareApp(t, want.App, got.App)
}

func compareImports(t *testing.T, want, got []*ast.Import) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("imports: want %d, got %d", len(want), len(got))
		return
	}
	for i := range want {
		if want[i].Path != got[i].Path {
			t.Errorf("import[%d]: want %q, got %q", i, want[i].Path, got[i].Path)
		}
	}
}

func compareOutputs(t *testing.T, want, got []*ast.Output) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("outputs: want %d, got %d", len(want), len(got))
		return
	}
	for i := range want {
		if want[i].Lang != got[i].Lang {
			t.Errorf("output[%d].Lang: want %q, got %q", i, want[i].Lang, got[i].Lang)
		}
		if want[i].Platform != got[i].Platform {
			t.Errorf("output[%d].Platform: want %q, got %q", i, want[i].Platform, got[i].Platform)
		}
	}
}

func compareStructs(t *testing.T, want, got []*ast.StructDef) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("structs: want %d, got %d", len(want), len(got))
		return
	}
	for i := range want {
		if want[i].Name != got[i].Name {
			t.Errorf("struct[%d].Name: want %q, got %q", i, want[i].Name, got[i].Name)
		}
		if len(want[i].Fields) != len(got[i].Fields) {
			t.Errorf("struct[%d].Fields: want %d, got %d", i, len(want[i].Fields), len(got[i].Fields))
			continue
		}
		for j := range want[i].Fields {
			wf := want[i].Fields[j]
			gf := got[i].Fields[j]
			if wf.Name != gf.Name {
				t.Errorf("struct[%d].field[%d].Name: want %q, got %q", i, j, wf.Name, gf.Name)
			}
			if wf.Type != gf.Type {
				t.Errorf("struct[%d].field[%d].Type: want %q, got %q", i, j, wf.Type, gf.Type)
			}
			compareExpr(t, wf.Default, gf.Default, fmt.Sprintf("struct[%d].field[%d].Default", i, j))
		}
	}
}

func compareEnums(t *testing.T, want, got []*ast.EnumDef) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("enums: want %d, got %d", len(want), len(got))
		return
	}
	for i := range want {
		if want[i].Name != got[i].Name {
			t.Errorf("enum[%d].Name: want %q, got %q", i, want[i].Name, got[i].Name)
		}
		if strings.Join(want[i].Values, ",") != strings.Join(got[i].Values, ",") {
			t.Errorf("enum[%d].Values: want %v, got %v", i, want[i].Values, got[i].Values)
		}
	}
}

func compareConsts(t *testing.T, want, got []*ast.Const) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("consts: want %d, got %d", len(want), len(got))
		return
	}
	for i := range want {
		if want[i].Name != got[i].Name {
			t.Errorf("const[%d].Name: want %q, got %q", i, want[i].Name, got[i].Name)
		}
		compareExpr(t, want[i].Init, got[i].Init, fmt.Sprintf("const[%d].Init", i))
	}
}

func compareData(t *testing.T, want, got []*ast.Data) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("data: want %d, got %d", len(want), len(got))
		for i, d := range want {
			t.Logf("  want[%d]: %s", i, d.Name)
		}
		for i, d := range got {
			t.Logf("  got[%d]: %s", i, d.Name)
		}
		return
	}
	for i := range want {
		w, g := want[i], got[i]
		if w.Name != g.Name {
			t.Errorf("data[%d].Name: want %q, got %q", i, w.Name, g.Name)
		}
		if w.Extern != g.Extern {
			t.Errorf("data[%d=%s].Extern: want %v, got %v", i, w.Name, w.Extern, g.Extern)
		}
		if w.IsFunc != g.IsFunc {
			t.Errorf("data[%d=%s].IsFunc: want %v, got %v", i, w.Name, w.IsFunc, g.IsFunc)
		}
		if w.Trigger != g.Trigger {
			t.Errorf("data[%d=%s].Trigger: want %q, got %q", i, w.Name, w.Trigger, g.Trigger)
		}
		compareExpr(t, w.Init, g.Init, fmt.Sprintf("data[%d=%s].Init", i, w.Name))
	}
}

func compareComputeds(t *testing.T, want, got []*ast.Computed) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("computeds: want %d, got %d", len(want), len(got))
		return
	}
	for i := range want {
		if want[i].Name != got[i].Name {
			t.Errorf("computed[%d].Name: want %q, got %q", i, want[i].Name, got[i].Name)
		}
		compareExpr(t, want[i].Expr, got[i].Expr, fmt.Sprintf("computed[%d=%s].Expr", i, want[i].Name))
	}
}

func compareComponents(t *testing.T, want, got []*ast.Component) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("components: want %d, got %d", len(want), len(got))
		return
	}
	for i := range want {
		w, g := want[i], got[i]
		if w.Name != g.Name {
			t.Errorf("component[%d].Name: want %q, got %q", i, w.Name, g.Name)
		}
		compareParams(t, w.Params, g.Params, w.Name)
		compareVisualNodes(t, w.Body, g.Body, "component["+w.Name+"]")
	}
}

func compareParams(t *testing.T, want, got []*ast.Param, compName string) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("component[%s].Params: want %d, got %d", compName, len(want), len(got))
		return
	}
	for i := range want {
		if want[i].Name != got[i].Name {
			t.Errorf("component[%s].param[%d].Name: want %q, got %q", compName, i, want[i].Name, got[i].Name)
		}
		compareExpr(t, want[i].Default, got[i].Default, fmt.Sprintf("component[%s].param[%d].Default", compName, i))
	}
}

func compareApp(t *testing.T, want, got *ast.App) {
	t.Helper()
	if (want == nil) != (got == nil) {
		t.Errorf("app: want nil=%v, got nil=%v", want == nil, got == nil)
		return
	}
	if want == nil {
		return
	}
	compareVisualNodes(t, want.Children, got.Children, "app")
}

func compareVisualNodes(t *testing.T, want, got []*ast.VisualNode, ctx string) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("%s.Children: want %d, got %d", ctx, len(want), len(got))
		for i, vn := range want {
			t.Logf("  want[%d]: %s", i, vn.Component)
		}
		for i, vn := range got {
			t.Logf("  got[%d]: %s", i, vn.Component)
		}
		return
	}
	for i := range want {
		compareVisualNode(t, want[i], got[i], ctx+"["+want[i].Component+"]")
	}
}

func compareVisualNode(t *testing.T, want, got *ast.VisualNode, ctx string) {
	t.Helper()
	if want.Component != got.Component {
		t.Errorf("%s.Component: want %q, got %q", ctx, want.Component, got.Component)
	}

	// Special props
	compareExprPtr(t, want.ID, got.ID, ctx+".ID")
	compareExprPtr(t, want.Key, got.Key, ctx+".Key")
	compareExprPtr(t, want.Class, got.Class, ctx+".Class")
	compareExprPtr(t, want.Ref, got.Ref, ctx+".Ref")
	compareExprPtr(t, want.If, got.If, ctx+".If")

	// For clause
	if (want.For == nil) != (got.For == nil) {
		t.Errorf("%s.For: want nil=%v, got nil=%v", ctx, want.For == nil, got.For == nil)
	} else if want.For != nil {
		if want.For.Variable != got.For.Variable {
			t.Errorf("%s.For.Variable: want %q, got %q", ctx, want.For.Variable, got.For.Variable)
		}
		if want.For.IndexVar != got.For.IndexVar {
			t.Errorf("%s.For.IndexVar: want %q, got %q", ctx, want.For.IndexVar, got.For.IndexVar)
		}
		compareExpr(t, want.For.Iterable, got.For.Iterable, ctx+".For.Iterable")
	}

	// Props
	compareExprMap(t, want.Props, got.Props, ctx+".Props")

	// Events
	compareExprMap(t, want.Events, got.Events, ctx+".Events")

	// Style attrs
	compareExprMap(t, want.StyleAttrs, got.StyleAttrs, ctx+".StyleAttrs")

	// Style block
	compareExprMap(t, want.StyleBlock, got.StyleBlock, ctx+".StyleBlock")

	// Children
	compareVisualNodes(t, want.Children, got.Children, ctx)
}

func compareExprPtr(t *testing.T, want, got *ast.Expr, ctx string) {
	t.Helper()
	if (want == nil) != (got == nil) {
		t.Errorf("%s: want nil=%v, got nil=%v", ctx, want == nil, got == nil)
		return
	}
	if want != nil {
		compareExpr(t, *want, *got, ctx)
	}
}

func compareExprMap(t *testing.T, want, got map[string]ast.Expr, ctx string) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("%s: want %d entries, got %d", ctx, len(want), len(got))
		for k := range want {
			if _, ok := got[k]; !ok {
				t.Logf("  missing key %q", k)
			}
		}
		for k := range got {
			if _, ok := want[k]; !ok {
				t.Logf("  extra key %q", k)
			}
		}
		return
	}
	for k, wExpr := range want {
		gExpr, ok := got[k]
		if !ok {
			t.Errorf("%s: missing key %q", ctx, k)
			continue
		}
		compareExpr(t, wExpr, gExpr, ctx+"["+k+"]")
	}
}

// compareExpr compares two Exprs by comparing their SNGL expression trees.
// The KDL parser populates both CEL and SNGL; the SNGL parser only populates SNGL.
// Literal-only exprs (no CEL) may have SNGL=nil in the KDL doc; we compare Literal directly.
func compareExpr(t *testing.T, want, got ast.Expr, ctx string) {
	t.Helper()

	// Both have SNGL nodes — compare the formatted expressions.
	if want.SNGL != nil && got.SNGL != nil {
		wStr := snglparser.FormatNode(want.SNGL)
		gStr := snglparser.FormatNode(got.SNGL)
		if wStr != gStr {
			t.Errorf("%s SNGL: want %q, got %q", ctx, wStr, gStr)
		}
		return
	}

	// KDL literal (no CEL) vs SNGL expression
	if want.SNGL == nil && want.Literal != nil {
		if got.SNGL != nil {
			// SNGL parser produces an SNGL node for literals
			gotStr := snglparser.FormatNode(got.SNGL)
			wantStr := fmtLiteral(want.Literal, want.TypeHint)
			if wantStr != gotStr {
				t.Errorf("%s: want literal %q, got SNGL %q", ctx, wantStr, gotStr)
			}
			return
		}
		// Both are plain literals
		if !literalEqual(want.Literal, got.Literal) {
			t.Errorf("%s Literal: want %v(%T), got %v(%T)", ctx, want.Literal, want.Literal, got.Literal, got.Literal)
		}
		return
	}

	// Neither has content — both empty
	if want.SNGL == nil && want.Literal == nil && want.CEL == "" &&
		got.SNGL == nil && got.Literal == nil && got.CEL == "" {
		return
	}

	t.Errorf("%s: shape mismatch — want SNGL=%v Literal=%v CEL=%q, got SNGL=%v Literal=%v CEL=%q",
		ctx, want.SNGL != nil, want.Literal, want.CEL, got.SNGL != nil, got.Literal, got.CEL)
}

func fmtLiteral(v any, typeHint string) string {
	return snglparser.FormatNode(literalToNode(v, typeHint))
}

func literalToNode(v any, typeHint string) ast.Node {
	switch val := v.(type) {
	case string:
		if strings.HasPrefix(val, "#") && (typeHint == "color" || typeHint == "") {
			return &ast.LiteralExpr{Value: val, Kind: ast.LiteralColor}
		}
		if typeHint == "duration" {
			return &ast.LiteralExpr{Value: val, Kind: ast.LiteralDuration}
		}
		return &ast.LiteralExpr{Value: val, Kind: ast.LiteralString}
	case int:
		return &ast.LiteralExpr{Value: val, Kind: ast.LiteralInt}
	case float64:
		return &ast.LiteralExpr{Value: val, Kind: ast.LiteralFloat}
	case bool:
		return &ast.LiteralExpr{Value: val, Kind: ast.LiteralBool}
	case nil:
		return &ast.LiteralExpr{Value: nil, Kind: ast.LiteralNull}
	default:
		return &ast.LiteralExpr{Value: v, Kind: ast.LiteralString}
	}
}

func literalEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	// Handle int vs int64 differences
	ai, aIsInt := toInt64(a)
	bi, bIsInt := toInt64(b)
	if aIsInt && bIsInt {
		return ai == bi
	}
	return a == b
}

func toInt64(v any) (int64, bool) {
	switch val := v.(type) {
	case int:
		return int64(val), true
	case int64:
		return val, true
	case float64:
		if val == float64(int64(val)) {
			return int64(val), true
		}
	}
	return 0, false
}
