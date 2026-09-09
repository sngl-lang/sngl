package gtk4

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

// recursiveTreeSrc is the repro: a recursive TreeView component that
// flattens to shared Model widget temps under the old behavior. When the
// render method recurses, the child overwrites the parent's m.__nX, so the
// parent appends a box to itself → GTK parent-child cycle → infinite layout.
const recursiveTreeSrc = `
import . "sngl:ui"
struct TreeNode {
    value int = 0
    left dyn = null
    right dyn = null
}
component TreeView(node dyn = null) ui {
    vbox(style={paddingLeft=12, gap=2}) {
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

func generateGTK4Model(t *testing.T, src string) string {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Targets: []ir.StaticTarget{{Platform: "gtk4", Language: "go"}}})
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
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "gtk4"}); err != nil {
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

// renderTreeViewBody extracts the body of TreeView's instance ctor from the
// generated model.go source.
func renderTreeViewBody(t *testing.T, src string) string {
	t.Helper()
	const marker = "func newTreeViewInstance("
	i := strings.Index(src, marker)
	if i < 0 {
		t.Fatalf("renderTreeView method not found in generated model.go:\n%s", src)
	}
	rest := src[i:]
	// Body ends at the first line that is exactly "}" at column 0.
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

// TestNodeEscape_RecursiveRenderUsesLocals asserts a recursive component's
// widget temps are per-frame -- never fields of the one Model -- and that no
// widget is appended to itself. Before the escape-analysis fix the render
// method used m.__nX fields, causing the recursion self-append GTK hang.
//
// A recursive component is now built as an instance record, so "per-frame"
// is `c.__nX` on a record the frame allocated for itself, or a bare local.
// `m.__nX` is the one spelling that is not.
func TestNodeEscape_RecursiveRenderUsesLocals(t *testing.T) {
	skipWithoutGIR(t)
	model := generateGTK4Model(t, recursiveTreeSrc)
	body := renderTreeViewBody(t, model)

	// The internal vbox/text temps must NOT be Model fields inside the
	// recursive method — that is the bug. Each recursion frame needs its
	// own locals.
	if mfield := regexp.MustCompile(`m\.__n\d`).FindString(body); mfield != "" {
		t.Errorf("newTreeViewInstance still references shared Model field %q; a widget temp must belong to the frame.\n--- newTreeViewInstance ---\n%s", mfield, body)
	}

	// It must declare at least one per-frame widget temp.
	if !regexp.MustCompile(`(c\.__n\d =|var __n\d|__n\d :?=)`).MatchString(body) {
		t.Errorf("newTreeViewInstance declares no per-frame widget temps.\n--- newTreeViewInstance ---\n%s", body)
	}

	// Each frame allocates its own record, and the recursion goes through the
	// ctor -- without both, the fields above are per-frame in spelling only.
	if !strings.Contains(body, "c := &TreeViewInstance{}") {
		t.Errorf("newTreeViewInstance allocates no record of its own.\n--- newTreeViewInstance ---\n%s", body)
	}
	if !strings.Contains(model, "newTreeViewInstance(") {
		t.Errorf("nothing instantiates TreeView through its ctor:\n%s", model)
	}

	// No widget may be appended to itself: gtk_box_append(x, x).
	for _, m := range regexp.MustCompile(`gtk_box_append\(([^,]+),\s*([^)]+)\)`).FindAllStringSubmatch(body, -1) {
		lhs := stripCast(m[1])
		rhs := stripCast(m[2])
		if lhs == rhs {
			t.Errorf("newTreeViewInstance appends a widget to itself: %s == %s", m[1], m[2])
		}
	}
}

// stripCast removes a leading cgo pointer cast so the underlying ident can
// be compared for self-append detection.
func stripCast(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "unsafe.Pointer("); i >= 0 {
		s = s[i+len("unsafe.Pointer("):]
		s = strings.TrimRight(s, ") ")
	}
	return strings.TrimSpace(s)
}
