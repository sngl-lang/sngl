package optimize

import (
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// snglExpr creates an Expr with a SNGL AST node.
func snglExpr(node ast.Node) ast.Expr {
	return ast.Expr{SNGL: node}
}

// platformEq returns PLATFORM == val as a SNGL AST.
func platformEq(val string) ast.Expr {
	return snglExpr(&ast.BinaryExpr{
		Op:    ast.BinEq,
		Left:  &ast.IdentExpr{Name: "PLATFORM"},
		Right: &ast.LiteralExpr{Value: val, Kind: ast.LiteralString},
	})
}

// langExpr returns an IdentExpr for LANGUAGE.
func langExpr() ast.Expr {
	return snglExpr(&ast.IdentExpr{Name: "LANGUAGE"})
}

// platformExpr returns an IdentExpr for PLATFORM.
func platformExpr() ast.Expr {
	return snglExpr(&ast.IdentExpr{Name: "PLATFORM"})
}

func TestIfTrue_Kept(t *testing.T) {
	ifExpr := platformEq("html")
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				If:        &ifExpr,
			}},
		},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if len(doc.App.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(doc.App.Children))
	}
	if doc.App.Children[0].If != nil {
		t.Errorf("expected If to be nil (guard removed), got %+v", doc.App.Children[0].If)
	}
}

func TestIfFalse_Removed(t *testing.T) {
	ifExpr := platformEq("html")
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				If:        &ifExpr,
			}},
		},
	}
	must(t, Optimize(doc, Config{Platform: "bubbletea", Language: "go"}))
	if len(doc.App.Children) != 0 {
		t.Fatalf("expected 0 children (dead branch), got %d", len(doc.App.Children))
	}
}

func TestNonConstIf_Unchanged(t *testing.T) {
	// Uses a non-constant variable reference
	ifExpr := snglExpr(&ast.BinaryExpr{
		Op:    ast.BinAnd,
		Left:  &ast.IdentExpr{Name: "PLATFORM"},
		Right: &ast.IdentExpr{Name: "count"}, // non-constant
	})
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				If:        &ifExpr,
			}},
		},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if len(doc.App.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(doc.App.Children))
	}
	node := doc.App.Children[0]
	if node.If == nil {
		t.Fatal("expected If to remain, got nil")
	}
	if node.If.SNGL == nil {
		t.Error("expected SNGL to remain")
	}
}

func TestPropFolded(t *testing.T) {
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				Props: map[string]ast.Expr{
					"platform": platformExpr(),
				},
			}},
		},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	e := doc.App.Children[0].Props["platform"]
	if e.SNGL != nil {
		t.Errorf("expected SNGL cleared, got %v", e.SNGL)
	}
	if e.Literal != "html" {
		t.Errorf("expected literal 'html', got %v", e.Literal)
	}
}

func TestComputedFolded(t *testing.T) {
	// PLATFORM + "/" + LANGUAGE
	expr := snglExpr(&ast.BinaryExpr{
		Op: ast.BinAdd,
		Left: &ast.BinaryExpr{
			Op:    ast.BinAdd,
			Left:  &ast.IdentExpr{Name: "PLATFORM"},
			Right: &ast.LiteralExpr{Value: "/", Kind: ast.LiteralString},
		},
		Right: &ast.IdentExpr{Name: "LANGUAGE"},
	})
	doc := &ast.Document{
		Functions: []*ast.FuncDef{{
			Name: "target",
			Body: expr,
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	e := doc.Functions[0].Body
	if e.SNGL != nil {
		t.Errorf("expected SNGL cleared, got %v", e.SNGL)
	}
	if e.Literal != "html/js" {
		t.Errorf("expected literal 'html/js', got %v", e.Literal)
	}
}

func TestNestedChildrenRemoved(t *testing.T) {
	ifExpr := platformEq("html")
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				If:        &ifExpr,
				Children: []*ast.VisualNode{
					{Component: "span"},
					{Component: "p"},
				},
			}},
		},
	}
	must(t, Optimize(doc, Config{Platform: "bubbletea", Language: "go"}))
	if len(doc.App.Children) != 0 {
		t.Fatalf("expected 0 children, got %d", len(doc.App.Children))
	}
}

func TestCloneIsolation(t *testing.T) {
	ifExpr := platformEq("html")
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				If:        &ifExpr,
			}},
		},
	}
	cloned := doc.Clone()
	must(t, Optimize(cloned, Config{Platform: "bubbletea", Language: "go"}))

	// Original should be untouched.
	if len(doc.App.Children) != 1 {
		t.Fatalf("original mutated: expected 1 child, got %d", len(doc.App.Children))
	}
	if doc.App.Children[0].If == nil {
		t.Error("original If was cleared")
	}

	// Clone should have the branch eliminated.
	if len(cloned.App.Children) != 0 {
		t.Fatalf("clone: expected 0 children, got %d", len(cloned.App.Children))
	}
}

func TestDataFieldFolded(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "lang",
			Init: langExpr(),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	e := doc.Data[0].Init
	if e.SNGL != nil {
		t.Errorf("expected SNGL cleared, got %v", e.SNGL)
	}
	if e.Literal != "js" {
		t.Errorf("expected literal 'js', got %v", e.Literal)
	}
}

func TestStylePropFolded(t *testing.T) {
	// PLATFORM == "html" ? "red" : "blue"
	doc := &ast.Document{
		Styles: []*ast.StyleDecl{{
			Name: "main",
			Props: map[string]ast.Expr{
				"color": snglExpr(&ast.TernaryExpr{
					Cond: &ast.BinaryExpr{Op: ast.BinEq, Left: &ast.IdentExpr{Name: "PLATFORM"}, Right: &ast.LiteralExpr{Value: "html", Kind: ast.LiteralString}},
					Then: &ast.LiteralExpr{Value: "red", Kind: ast.LiteralString},
					Else: &ast.LiteralExpr{Value: "blue", Kind: ast.LiteralString},
				}),
			},
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	e := doc.Styles[0].Props["color"]
	if e.Literal != "red" {
		t.Errorf("expected literal 'red', got %v", e.Literal)
	}
}

func TestComponentParamDefaultFolded(t *testing.T) {
	doc := &ast.Document{
		Components: []*ast.Component{{
			Name: "Foo",
			Params: []*ast.Param{{
				Name:    "target",
				Default: platformExpr(),
			}},
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	e := doc.Components[0].Params[0].Default
	if e.SNGL != nil {
		t.Errorf("expected SNGL cleared, got %v", e.SNGL)
	}
	if e.Literal != "html" {
		t.Errorf("expected literal 'html', got %v", e.Literal)
	}
}

func TestStructFieldDefaultFolded(t *testing.T) {
	// LANGUAGE == "go"
	doc := &ast.Document{
		Structs: []*ast.StructDef{{
			Name: "Config",
			Fields: []*ast.StructField{{
				Name: "isGo",
				Default: snglExpr(&ast.BinaryExpr{
					Op:    ast.BinEq,
					Left:  &ast.IdentExpr{Name: "LANGUAGE"},
					Right: &ast.LiteralExpr{Value: "go", Kind: ast.LiteralString},
				}),
			}},
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "go"}))
	e := doc.Structs[0].Fields[0].Default
	if e.Literal != true {
		t.Errorf("expected literal true, got %v", e.Literal)
	}
}

func TestOptimizeReturnsNil(t *testing.T) {
	doc := &ast.Document{}
	if err := Optimize(doc, Config{}); err != nil {
		t.Errorf("expected no error for empty doc, got %v", err)
	}
}

func TestForIterableFolded(t *testing.T) {
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				For: &ast.ForClause{
					Variable: "item",
					Iterable: platformExpr(),
				},
			}},
		},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	e := doc.App.Children[0].For.Iterable
	if e.Literal != "html" {
		t.Errorf("expected literal 'html', got %v", e.Literal)
	}
}

func TestEventFolded(t *testing.T) {
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "button",
				Events: map[string]ast.Expr{
					"click": langExpr(),
				},
			}},
		},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	e := doc.App.Children[0].Events["click"]
	if e.Literal != "js" {
		t.Errorf("expected literal 'js', got %v", e.Literal)
	}
}

func TestForEmptyList_Removed(t *testing.T) {
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				For: &ast.ForClause{
					Variable: "item",
					Iterable: snglExpr(&ast.ListExpr{Elements: nil}),
				},
				Children: []*ast.VisualNode{{Component: "span"}},
			}},
		},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if len(doc.App.Children) != 0 {
		t.Fatalf("expected 0 children (for over empty list), got %d", len(doc.App.Children))
	}
}

func TestTernaryFolded(t *testing.T) {
	// true ? "a" : "b" → "a"
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "val",
			Init: snglExpr(&ast.TernaryExpr{
				Cond: &ast.BinaryExpr{
					Op:    ast.BinEq,
					Left:  &ast.IdentExpr{Name: "PLATFORM"},
					Right: &ast.LiteralExpr{Value: "html", Kind: ast.LiteralString},
				},
				Then: &ast.LiteralExpr{Value: "a", Kind: ast.LiteralString},
				Else: &ast.LiteralExpr{Value: "b", Kind: ast.LiteralString},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != "a" {
		t.Errorf("expected 'a', got %v", doc.Data[0].Init.Literal)
	}
}

func TestCallStringFolded(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "val",
			Init: snglExpr(&ast.CallExpr{
				Func: "string",
				Args: []ast.Node{&ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != "42" {
		t.Errorf("expected literal '42', got %v", doc.Data[0].Init.Literal)
	}
}

func TestCallIntFolded(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "val",
			Init: snglExpr(&ast.CallExpr{
				Func: "int",
				Args: []ast.Node{&ast.LiteralExpr{Value: 3.14, Kind: ast.LiteralFloat}},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != 3 {
		t.Errorf("expected literal 3, got %v", doc.Data[0].Init.Literal)
	}
}

func TestCallFloatFolded(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "val",
			Init: snglExpr(&ast.CallExpr{
				Func: "float",
				Args: []ast.Node{&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != 1.0 {
		t.Errorf("expected literal 1.0, got %v", doc.Data[0].Init.Literal)
	}
}

func TestStringLengthFolded(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "val",
			Init: snglExpr(&ast.MethodExpr{
				Receiver: &ast.IdentExpr{Name: "string"},
				Method:   "length",
				Args:     []ast.Node{&ast.LiteralExpr{Value: "hello", Kind: ast.LiteralString}},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != 5 {
		t.Errorf("expected literal 5, got %v", doc.Data[0].Init.Literal)
	}
}

func TestMethodLengthFolded(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "val",
			Init: snglExpr(&ast.MethodExpr{
				Receiver: &ast.LiteralExpr{Value: "abc", Kind: ast.LiteralString},
				Method:   "length",
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != 3 {
		t.Errorf("expected literal 3, got %v", doc.Data[0].Init.Literal)
	}
}

func TestMethodContainsFolded(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "val",
			Init: snglExpr(&ast.MethodExpr{
				Receiver: &ast.LiteralExpr{Value: "hello", Kind: ast.LiteralString},
				Method:   "contains",
				Args:     []ast.Node{&ast.LiteralExpr{Value: "ell", Kind: ast.LiteralString}},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != true {
		t.Errorf("expected literal true, got %v", doc.Data[0].Init.Literal)
	}
}

func TestInterpolationFolded(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "msg",
			Init: snglExpr(&ast.InterpolationExpr{
				Parts: []ast.Node{
					&ast.LiteralExpr{Value: "platform: ", Kind: ast.LiteralString},
					&ast.IdentExpr{Name: "PLATFORM"},
				},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != "platform: html" {
		t.Errorf("expected 'platform: html', got %v", doc.Data[0].Init.Literal)
	}
}

func TestCallIntOnInt_ReturnsSame(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "val",
			Init: snglExpr(&ast.CallExpr{
				Func: "int",
				Args: []ast.Node{&ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != 42 {
		t.Errorf("expected literal 42, got %v", doc.Data[0].Init.Literal)
	}
}

func TestCallFloatOnFloat_ReturnsSame(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "val",
			Init: snglExpr(&ast.CallExpr{
				Func: "float",
				Args: []ast.Node{&ast.LiteralExpr{Value: 2.5, Kind: ast.LiteralFloat}},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != 2.5 {
		t.Errorf("expected literal 2.5, got %v", doc.Data[0].Init.Literal)
	}
}

func TestEvalMethodListLength(t *testing.T) {
	// Directly test the evalMethod path for []any .length()
	recv := []any{"a", "b", "c"}
	result, ok := evalMethod("length", recv, nil)
	if !ok {
		t.Fatal("expected evalMethod to succeed for []any .length()")
	}
	if result != 3 {
		t.Errorf("expected 3, got %v", result)
	}
}

func TestClassExprFolded(t *testing.T) {
	classExpr := platformExpr()
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				Class:     &classExpr,
			}},
		},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	e := doc.App.Children[0].Class
	if e.SNGL != nil {
		t.Errorf("expected SNGL cleared, got %v", e.SNGL)
	}
	if e.Literal != "html" {
		t.Errorf("expected literal 'html', got %v", e.Literal)
	}
}

func TestRefExprFolded(t *testing.T) {
	refExpr := langExpr()
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{{
				Component: "div",
				Ref:       &refExpr,
			}},
		},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	e := doc.App.Children[0].Ref
	if e.SNGL != nil {
		t.Errorf("expected SNGL cleared, got %v", e.SNGL)
	}
	if e.Literal != "js" {
		t.Errorf("expected literal 'js', got %v", e.Literal)
	}
}

func TestIsConstExprListExpr(t *testing.T) {
	vars := map[string]any{
		"PLATFORM": "html",
		"LANGUAGE": "js",
	}
	// All-literal list should be constant
	list := &ast.ListExpr{
		Elements: []ast.Node{
			&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
			&ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
			&ast.LiteralExpr{Value: 3, Kind: ast.LiteralInt},
		},
	}
	if !isConstExpr(list, vars) {
		t.Error("expected ListExpr with all literals to be constant")
	}

	// List with non-constant element should not be constant
	listNonConst := &ast.ListExpr{
		Elements: []ast.Node{
			&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
			&ast.IdentExpr{Name: "count"}, // not in vars
		},
	}
	if isConstExpr(listNonConst, vars) {
		t.Error("expected ListExpr with non-constant element to not be constant")
	}

	// List with PLATFORM reference should be constant
	listWithVar := &ast.ListExpr{
		Elements: []ast.Node{
			&ast.IdentExpr{Name: "PLATFORM"},
			&ast.LiteralExpr{Value: "x", Kind: ast.LiteralString},
		},
	}
	if !isConstExpr(listWithVar, vars) {
		t.Error("expected ListExpr with PLATFORM to be constant")
	}
}

func TestUnaryNegFloat(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "x",
			Init: snglExpr(&ast.UnaryExpr{
				Op:      ast.UnaryNeg,
				Operand: &ast.LiteralExpr{Value: 3.14, Kind: ast.LiteralFloat},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != -3.14 {
		t.Errorf("expected -3.14, got %v", doc.Data[0].Init.Literal)
	}
}

func TestUnaryNotBool(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "x",
			Init: snglExpr(&ast.UnaryExpr{
				Op:      ast.UnaryNot,
				Operand: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != false {
		t.Errorf("expected false, got %v", doc.Data[0].Init.Literal)
	}
}

func TestUnaryNegInt(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "x",
			Init: snglExpr(&ast.UnaryExpr{
				Op:      ast.UnaryNeg,
				Operand: &ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != -42 {
		t.Errorf("expected -42, got %v", doc.Data[0].Init.Literal)
	}
}

func TestFloatModReturnsNil(t *testing.T) {
	// Float mod should not fold — numericOp falls through for BinMod on floats.
	result, ok := numericOp(ast.BinMod, 3.5, 2.0)
	if ok {
		t.Errorf("expected float mod to not fold, but got %v", result)
	}
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}
}

func TestToStrNonString(t *testing.T) {
	// Interpolation with an int part exercises the toStr non-string branch.
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "msg",
			Init: snglExpr(&ast.InterpolationExpr{
				Parts: []ast.Node{
					&ast.LiteralExpr{Value: "count: ", Kind: ast.LiteralString},
					&ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt},
				},
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != "count: 42" {
		t.Errorf("expected 'count: 42', got %v", doc.Data[0].Init.Literal)
	}
}

func TestEvalMethodUnknown(t *testing.T) {
	// Calling an unknown method on a string receiver should return nil, false.
	result, ok := evalMethod("bogusMethod", "hello", nil)
	if ok {
		t.Errorf("expected unknown method to return false, got true with %v", result)
	}
}

func TestEvalCallFuncZeroArgs(t *testing.T) {
	// A function call with 0 args hits the len(args) != 1 early return.
	result, ok := evalCallFunc("string", nil)
	if ok {
		t.Errorf("expected 0-arg call to return false, got true with %v", result)
	}
}

func TestFoldFixtures(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "..", "testdata", "optimize_*.sngl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no optimize_*.sngl fixtures found")
	}
	for _, path := range matches {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			folds, err := testutil.ParseFoldDirectives(path)
			if err != nil {
				t.Fatalf("parse fold directives: %v", err)
			}
			if len(folds) == 0 {
				t.Skip("no FOLD directives")
			}
			doc, err := testutil.ParseFile(path)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
			testutil.AssertFolds(t, doc, folds)
		})
	}
}
