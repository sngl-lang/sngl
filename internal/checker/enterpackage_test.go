package checker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

// A lib package loads lazily and runs the same pass1 a program's package does.
// pass1 opens by resetting the per-package registration state, so every field
// it resets has to be saved and restored around that nested run -- otherwise
// the program's own state is gone for the remainder of its pass1.
//
// Restoring only c.docs was the bug this guards. It is unreachable today
// because every lib package is loaded before a program registers its first
// declaration, which is exactly why a test that runs a program cannot catch
// it: the next field added to pass1, or the next thing that makes loading
// lazier, brings it back with nothing to notice.
//
// So this asserts the relationship directly. It reads pass1 for the fields it
// assigns and enterPackage for the fields it restores, and fails naming any
// that pass1 clears and enterPackage does not put back.
func TestEnterPackageRestoresWhatPass1Resets(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "checker.go", nil, 0)
	if err != nil {
		t.Fatalf("parse checker.go: %v", err)
	}

	reset := checkerFieldsAssignedIn(f, "pass1")
	if len(reset) == 0 {
		t.Fatal("found no c.<field> assignments in pass1; this test has stopped testing anything")
	}
	restored := checkerFieldsAssignedIn(f, "enterPackage")
	if len(restored) == 0 {
		t.Fatal("found no c.<field> assignments in enterPackage")
	}

	var missing []string
	for name := range reset {
		if !restored[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		t.Errorf("pass1 resets c.%s and enterPackage does not restore it\n"+
			"\tA lib package loads from inside the program's own pass1 and runs\n"+
			"\tpass1 itself, so anything pass1 resets is lost for the rest of the\n"+
			"\tprogram's run unless enterPackage saves and restores it.", name)
	}
}

// checkerFieldsAssignedIn returns the set of names X for every `c.X = …` in the
// named method of the checker.
func checkerFieldsAssignedIn(f *ast.File, method string) map[string]bool {
	out := map[string]bool{}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != method || fd.Recv == nil || fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range as.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "c" {
					out[sel.Sel.Name] = true
				}
			}
			return true
		})
	}
	return out
}
