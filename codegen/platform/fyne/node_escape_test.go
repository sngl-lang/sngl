package fyne

import (
	"regexp"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/lower"
)

const recursiveTreeSrc = `
import . "sngl:ui"
struct TreeNode {
    value int = 0
    left dyn = null
    right dyn = null
}
component TreeView(node dyn = null) ui {
    vbox {
        text(value=string(node.value))
        if node.left != null { TreeView(node=node.left) }
        if node.right != null { TreeView(node=node.right) }
    }
}
component main ui {
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
	pkg := checkForFyne(t, src)
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
	const marker = "func newTreeViewInstance("
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

// TestNodeEscape_RecursiveRenderUsesLocals asserts a recursive component's
// widget temps are per-frame, never fields of the one Model. Before the
// escape-analysis fix the render method used m.__nX fields; recursion
// clobbered the parent frame's temp, so the tree mis-rendered (a child
// container added to itself).
//
// A recursive component is now built as an instance record, so "per-frame"
// is `c.__nX` on a record the frame allocated for itself, or a bare local --
// either is the frame's own. `m.__nX` is the one spelling that is not.
func TestNodeEscape_RecursiveRenderUsesLocals(t *testing.T) {
	model := generateFyneModel(t, recursiveTreeSrc)
	body := renderTreeViewBodyFyne(t, model)

	if mfield := regexp.MustCompile(`m\.__n\d`).FindString(body); mfield != "" {
		t.Errorf("newTreeViewInstance still references shared Model field %q; a widget temp must belong to the frame.\n--- newTreeViewInstance ---\n%s", mfield, body)
	}
	if !regexp.MustCompile(`(c\.__n\d =|__n\d :?=)`).MatchString(body) {
		t.Errorf("newTreeViewInstance declares no per-frame widget temps.\n--- newTreeViewInstance ---\n%s", body)
	}
	// Each frame allocates its own record; without that the fields above are
	// per-frame in spelling only.
	if !strings.Contains(body, "c := &TreeViewInstance{}") {
		t.Errorf("newTreeViewInstance allocates no record of its own.\n--- newTreeViewInstance ---\n%s", body)
	}
	// And the recursion goes through the ctor, so a nested TreeView is a
	// different instance rather than a re-entry into this one.
	if !strings.Contains(model, "newTreeViewInstance(") {
		t.Errorf("nothing instantiates TreeView through its ctor:\n%s", model)
	}
}
