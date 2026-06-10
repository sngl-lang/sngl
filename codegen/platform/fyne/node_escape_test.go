package fyne

import (
	"regexp"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

const recursiveTreeSrc = `
struct TreeNode {
    value int = 0
    left dyn = null
    right dyn = null
}
component TreeView(node dyn = null) {
    vbox {
        text(value=string(node.value))
        if node.left != null { TreeView(node=node.left) }
        if node.right != null { TreeView(node=node.right) }
    }
}
component main {
    var count = 0
    var tree = TreeNode{value=5, left=TreeNode{value=3}, right=TreeNode{value=8}}
    vbox {
        text(value=string(count))
        TreeView(node=tree)
    }
}
`

func generateFyneModel(t *testing.T, src string) string {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s", d.Msg)
		}
	}
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "fyne"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	modelSrc, ok := mem.Files()["model.go"]
	if !ok {
		t.Fatal("model.go not found in generated files")
	}
	return string(modelSrc)
}

func renderTreeViewBodyFyne(t *testing.T, src string) string {
	t.Helper()
	const marker = "func (m *Model) renderTreeView("
	i := strings.Index(src, marker)
	if i < 0 {
		t.Fatalf("renderTreeView method not found in generated model.go:\n%s", src)
	}
	rest := src[i:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

// TestNodeEscape_RecursiveRenderUsesLocals asserts the recursive component's
// fyne render method declares its internal widget temps as function-LOCAL
// variables (not shared m.__nX Model fields). Before the escape-analysis fix
// the method used m.__nX fields; recursion clobbered the parent frame's temp,
// so the tree mis-rendered (a child container added to itself).
func TestNodeEscape_RecursiveRenderUsesLocals(t *testing.T) {
	model := generateFyneModel(t, recursiveTreeSrc)
	body := renderTreeViewBodyFyne(t, model)

	if mfield := regexp.MustCompile(`m\.__n\d`).FindString(body); mfield != "" {
		t.Errorf("renderTreeView still references shared Model field %q; internal refs must be function-local.\n--- renderTreeView ---\n%s", mfield, body)
	}
	if !regexp.MustCompile(`__n\d :?=`).MatchString(body) {
		t.Errorf("renderTreeView declares no local widget temps; expected `__nN :=`.\n--- renderTreeView ---\n%s", body)
	}
}
