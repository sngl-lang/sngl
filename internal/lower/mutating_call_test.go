package lower

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ir"
)

// mutatingID is declared here rather than reusing `list.push` because the
// registry holds only what a check has loaded, and this package loads no
// library. The claim is about the MutatesReceiver flag, not about which three
// declarations carry it today.
const mutatingID = "lowertest.mutate"

func init() {
	ir.RegisterIntrinsic(ir.IntrinsicDef{
		Name:            mutatingID,
		Pkg:             "internal/lower",
		DeclaredAs:      "mutate",
		Return:          ir.TypVoid,
		MutatesReceiver: true,
	})
}

// mutatingCall is `xs.mutate(1)` as the checker builds a mutating method call: an
// intrinsic whose first argument is the receiver it writes through.
func mutatingCall() *ir.Call {
	xs := &ir.Var{Name: "xs", Type: ir.ListOf(ir.TypInt)}
	return &ir.Call{
		Func: &ir.Func{Name: "mutate", Intrinsic: mutatingID},
		Type: ir.TypVoid,
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: "xs", Sym: xs, Type: ir.ListOf(ir.TypInt)}},
			{Value: &ir.Literal{Type: ir.TypInt, Value: "1"}},
		},
	}
}

func pkgWithBody(body []ir.Stmt) *ir.Package {
	return &ir.Package{Funcs: []*ir.Func{{Name: "f", Block: body}}}
}

// The front-end guards refuse every source shape that would produce this, which
// is why the IR is built by hand: a backstop reachable only through a guard
// that already caught everything is a backstop nothing tests. The guards are
// the diagnostic; this is the guarantee that a position nobody enumerated
// cannot reach a backend silently.
func TestMutatingCallInExpressionPositionIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []ir.Stmt
	}{
		{"returned", []ir.Stmt{&ir.Return{Value: mutatingCall()}}},
		{"indexed", []ir.Stmt{&ir.Return{Value: &ir.Index{
			Type:    ir.TypInt,
			Operand: mutatingCall(),
			Idx:     &ir.Literal{Type: ir.TypInt, Value: "0"},
		}}}},
		{"list element", []ir.Stmt{&ir.Return{Value: &ir.ListLit{
			Type:  ir.ListOf(ir.TypVoid),
			Elems: []ir.Expr{mutatingCall()},
		}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyMutationsAreStatements(pkgWithBody(tc.body))
			if err == nil {
				t.Fatal("a mutating call in expression position was accepted; " +
					"every backend emits one as a statement and would write source that does not compile")
			}
			if !strings.Contains(err.Error(), mutatingID) {
				t.Errorf("error should name the intrinsic, got %q", err)
			}
		})
	}
}

// The statement form is the whole API, and the check must not refuse it.
func TestMutatingCallAsAStatementIsAccepted(t *testing.T) {
	body := []ir.Stmt{&ir.CallStmt{Call: mutatingCall()}}
	if err := verifyMutationsAreStatements(pkgWithBody(body)); err != nil {
		t.Fatalf("the statement form was refused: %v", err)
	}
}
