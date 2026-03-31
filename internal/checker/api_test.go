package checker

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestAPINamespaceStruct(t *testing.T) {
	src := `output { js { testplat } }

component main {
    var x testplat.MyStruct
    text(value="hello")
}
`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	apiDoc := &ast.Document{
		Structs: []*ast.StructDef{{
			Name: "MyStruct",
			Fields: []*ast.StructField{
				{Name: "name", Type: "string"},
			},
		}},
	}
	apis := &APIConfig{
		Namespaces: map[string]*ast.Document{
			"testplat": apiDoc,
		},
	}

	err = Check(doc, ".", nil, nil, apis, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestAPINamespaceData(t *testing.T) {
	src := `output { js { testplat } }

component main {
    var x int = testplat.magicNumber
    text(value="hello")
}
`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	apiDoc := &ast.Document{
		Data: []*ast.Data{{
			Name:   "magicNumber",
			Extern: true,
			Init:   ast.Expr{TypeHint: "int"},
		}},
	}
	apis := &APIConfig{
		Namespaces: map[string]*ast.Document{
			"testplat": apiDoc,
		},
	}

	err = Check(doc, ".", nil, nil, apis, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestAPINamespaceEnum(t *testing.T) {
	src := `output { js { testplat } }

component main {
    var x testplat.Color = "red"
    text(value="hello")
}
`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	apiDoc := &ast.Document{
		Enums: []*ast.EnumDef{{
			Name:   "Color",
			Values: []string{"red", "green", "blue"},
		}},
	}
	apis := &APIConfig{
		Namespaces: map[string]*ast.Document{
			"testplat": apiDoc,
		},
	}

	err = Check(doc, ".", nil, nil, apis, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestAPIOutputOptsValid(t *testing.T) {
	src := `output { js { testplat(pkg="main") } }

component main {
    text(value="hello")
}
`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	apiDoc := &ast.Document{
		Structs: []*ast.StructDef{{
			Name: "Opts",
			Fields: []*ast.StructField{
				{Name: "pkg", Type: "string"},
				{Name: "main", Type: "bool"},
			},
		}},
	}
	apis := &APIConfig{
		Namespaces: map[string]*ast.Document{
			"testplat": apiDoc,
		},
	}

	err = Check(doc, ".", nil, nil, apis, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestAPIOutputOptsUnknown(t *testing.T) {
	src := `output { js { testplat(badopt="yes") } }

component main {
    text(value="hello")
}
`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	apiDoc := &ast.Document{
		Structs: []*ast.StructDef{{
			Name: "Opts",
			Fields: []*ast.StructField{
				{Name: "pkg", Type: "string"},
			},
		}},
	}
	apis := &APIConfig{
		Namespaces: map[string]*ast.Document{
			"testplat": apiDoc,
		},
	}

	err = Check(doc, ".", nil, nil, apis, true)
	if err == nil {
		t.Fatal("expected error for unknown option, got nil")
	}
	if !strings.Contains(err.Error(), `unknown option "badopt"`) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAPIDynamicResolve(t *testing.T) {
	src := `output { js { testplat } }

component main {
    var x int = testplat.dynamicVal
    text(value="hello")
}
`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	called := false
	apis := &APIConfig{
		Namespaces: map[string]*ast.Document{
			"testplat": {}, // empty static API
		},
		DynamicNS: func(namespace, name string) *ast.NativeDecls {
			if namespace == "testplat" && name == "dynamicVal" {
				called = true
				return &ast.NativeDecls{
					Data: []*ast.Data{{
						Name:   "dynamicVal",
						Extern: true,
						Init:   ast.Expr{TypeHint: "int"},
					}},
				}
			}
			return nil
		},
	}

	err = Check(doc, ".", nil, nil, apis, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !called {
		t.Error("dynamic resolver was not called")
	}
}

func TestAPINoOptsStructSkipsValidation(t *testing.T) {
	src := `output { js { testplat(anything="goes") } }

component main {
    text(value="hello")
}
`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// API doc without an Opts struct — validation should be skipped
	apiDoc := &ast.Document{
		Structs: []*ast.StructDef{{
			Name:   "NotOpts",
			Fields: []*ast.StructField{{Name: "x", Type: "int"}},
		}},
	}
	apis := &APIConfig{
		Namespaces: map[string]*ast.Document{
			"testplat": apiDoc,
		},
	}

	err = Check(doc, ".", nil, nil, apis, true)
	if err != nil {
		t.Fatalf("expected no error (no Opts struct), got: %v", err)
	}
}
