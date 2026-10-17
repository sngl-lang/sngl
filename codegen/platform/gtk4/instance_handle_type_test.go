package gtk4

import (
	"regexp"
	"strings"
	"testing"
)

// recursiveTree is testdata/test_recursive_component.sngl without the ids its
// tests reach the nested levels through: a user #id on an instance inside a
// render slot is emitted as a record field in one half of the create and a
// local in the other, which is its own defect.
const recursiveTree = `
import . "sngl:ui"

struct TreeNode {
    value int = 0
    left option[TreeNode] = null
    right option[TreeNode] = null
}

component tree_view(node TreeNode) node {
    vbox {
        text #val(value=string(node.value))
        if node.left != null {
            tree_view(node=node.left)
        }
        if node.right != null {
            tree_view(node=node.right)
        }
    }
}

component main node {
    var leaf1 = TreeNode{value=1}
    var node3 = TreeNode{value=3, left=leaf1}
    var node8 = TreeNode{value=8}
    var root = TreeNode{value=5, left=node3, right=node8}

    tree_view #top(node=root)
}

window {
    main
}
`

// TestAnInstanceHandleFieldIsTypedAsTheRecord compiles the Go emitted for
// recursiveTree, whose recursive `tree_view` the build renders as an instance
// record.
//
// The ctor and the record were right; the Model field holding one was not.
// compilation.widgetFieldSink built the Go type itself -- gtk4rt.Handle in
// wrapped mode, *C.<class> otherwise -- where the other three sinks go through
// widgetFieldGoType, which is what knows that a component instance's handle
// arrives already spelled as Go and is not a widget in either mode. The two
// disagreed and the field lost, so `m.__n0 gtk4rt.Handle` was assigned a
// *Tree_viewInstance.
func TestAnInstanceHandleFieldIsTypedAsTheRecord(t *testing.T) {
	model := generateGTK4ModelBuilt(t, recursiveTree)

	// The field declaration is matched as a pattern because gofmt aligns a
	// struct's types to its widest field name: a literal run of spaces asserts
	// what else is in the Model rather than what this field's type is.
	for _, want := range []string{
		`top\s+\*Tree_viewInstance`,
		`m\.top = newTree_viewInstance\(`,
		// A record does carry a Root, so this is also the positive half of
		// what OnComponentRoot decides.
		`m\.top__el = m\.top\.Root`,
	} {
		if !regexp.MustCompile(want).MatchString(model) {
			t.Errorf("emitted Go missing %s\n--- model.go ---\n%s", want, model)
		}
	}
	buildGeneratedGo(t, "gtk4-handle-type-", model)
}

// The optimizer unrolls test_html_recursive_component.sngl's recursion, so
// nothing the program renders instantiates `tree`.
func TestAnUnrenderedRecursionIsNotEmitted(t *testing.T) {
	model := generateGTK4ModelBuilt(t, fixtureSource(t, "test_html_recursive_component.sngl"))
	if strings.Contains(model, "renderTree") || strings.Contains(model, "TreeInstance") {
		t.Errorf("the unrolled recursion is still emitted\n--- model.go ---\n%s", model)
	}
	buildGeneratedGo(t, "gtk4-unrendered-recursion-", model)
}
