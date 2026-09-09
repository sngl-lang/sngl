package parser_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// `else if` chains parse as a nested IfStmt inside the else block, reusing the
// existing IfStmt.Else StmtBlock (no new AST shape).
func TestElseIfChain(t *testing.T) {
	doc := mustParse(t, `
component main node {
    var n = 0
    func classify() int {
        if n == 0 {
            return 0
        } else if n == 1 {
            return 1
        } else {
            return 2
        }
    }
    text(value="{classify()}")
}
`)
	// Find the outer IfStmt inside classify's body.
	ifStmt := findFirstIf(t, doc)
	if ifStmt.Else.Stmts == nil || len(ifStmt.Else.Stmts) != 1 {
		t.Fatalf("outer else should hold exactly one statement (the else-if), got %d", len(ifStmt.Else.Stmts))
	}
	inner, ok := ifStmt.Else.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("else branch statement is %T, want *ast.IfStmt (the `else if`)", ifStmt.Else.Stmts[0])
	}
	if !inner.Else.IsDefined() || len(inner.Else.Stmts) != 1 {
		t.Fatalf("inner (else-if) should have a final else block with one statement")
	}
}

// The formatter prints an else-if chain as `else if`, not `else { if ... }`.
func TestElseIfFormatting(t *testing.T) {
	doc := mustParse(t, `component main node {
    var n = 0
    func f() int {
        if n == 0 {
            return 0
        } else if n == 1 {
            return 1
        } else {
            return 2
        }
    }
    text(value="{f()}")
}`)
	out := parser.Format(doc)
	if !strings.Contains(out, "} else if ") {
		t.Errorf("formatter did not emit `else if`; got:\n%s", out)
	}
	if strings.Contains(out, "else {\n") && strings.Contains(out, "if n == 1") {
		// The middle branch must not be printed as `else { if ... }`.
		if strings.Contains(out, "else {\n            if n == 1") {
			t.Errorf("else-if was printed as nested else block:\n%s", out)
		}
	}
}

func findFirstIf(t *testing.T, doc *ast.Document) *ast.IfStmt {
	t.Helper()
	var found *ast.IfStmt
	var walk func(stmts []ast.Stmt)
	walk = func(stmts []ast.Stmt) {
		for _, s := range stmts {
			if found != nil {
				return
			}
			switch n := s.(type) {
			case *ast.IfStmt:
				found = n
				return
			case *ast.FuncDef:
				if n.Block.IsDefined() {
					walk(n.Block.Stmts)
				}
			case *ast.ComponentDecl:
				walk(n.Body.Stmts)
			case *ast.VisualNode:
				walk(n.Block.Stmts)
			}
		}
	}
	walk(doc.Stmts)
	if found == nil {
		t.Fatal("no IfStmt found")
	}
	return found
}
