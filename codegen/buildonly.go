package codegen

import (
	"fmt"

	"duckfam.us/sngl/ir"
)

// BuildOnlyDiagnostic refuses call, which only the build can answer and which
// was left for the program to make, positioned at site -- the node around it
// the program wrote -- where there is one.
func BuildOnlyDiagnostic(call *ir.Call, site *ir.NodeInst, memo map[*ir.Func]bool) ir.Diagnostic {
	d := ir.Diagnostic{Severity: ir.Error, Msg: fmt.Sprintf("%s is answered only while the program builds, and this call was left for the program to make: call it with arguments known at build time", ir.BuildIntrinsicOf(call.Func, memo))}
	switch {
	case site != nil:
		d.Pos = ir.NodePos(site)
	case call.AST != nil:
		d.Pos = call.AST.Pos
	}
	return d
}

// RefuseUnfoldedBuildCalls is the half of the build's refusal a target that
// folds its documents (Fold) answers: a call only the build can answer, in a
// view the document's fold has finished with, is one nothing will make.
func RefuseUnfoldedBuildCalls(stmts []ir.Stmt) error {
	memo := map[*ir.Func]bool{}
	var err error
	ir.ViewCalls(stmts, func(c *ir.Call, site *ir.NodeInst) {
		if err == nil && ir.IsBuildCall(c.Func, memo) {
			err = BuildOnlyDiagnostic(c, site, memo)
		}
	})
	return err
}
