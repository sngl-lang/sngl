package lower

import (
	"duckfam.us/sngl/ir"
)

// passTestMount says where a compiled test mounts the component it takes: at
// the first statement that needs it (ir.TestSetup), or at the end of a body
// that has none. What the statements ahead of it set -- a context the mount
// reads, a prop or var's starting value -- is named in the mount call, whose
// initializers leave each as the test set it, and the redraws and settles
// lowering put after each of those writes go: nothing is drawn yet, and the
// mount draws and settles once.
//
// The interpreter mounts on the first read instead (testrunner), which a
// compiled test, reading a Model's field straight off it, has no moment for.
// Runs late, after passReactivity's redraws and passEffect's settles.
var passTestMount = pass{
	name:    "TestMount",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, _ Features, opts Options) error {
		if pkg == nil || !opts.Tests {
			return nil
		}
		for _, fn := range pkg.Funcs {
			if fn == nil || !fn.IsTest {
				continue
			}
			if comp := testComponentParam(fn); comp != nil {
				fn.Block = mountWhereNeeded(fn.Block, comp, pkg)
			}
		}
		return nil
	},
}

// testComponentParam is the parameter a test takes its component as, or nil.
func testComponentParam(fn *ir.Func) *ir.Param {
	for _, p := range fn.Params {
		if p.Type != nil && p.Type.Kind == ir.TypeComponent {
			return p
		}
	}
	return nil
}

// mountWhereNeeded is block with its component mounted ahead of the first
// statement that needs it.
func mountWhereNeeded(block []ir.Stmt, comp *ir.Param, pkg *ir.Package) []ir.Stmt {
	var out []ir.Stmt
	var preset []string
	for i, s := range block {
		if p, ok := ir.TestSetup(s, comp, pkg); ok {
			out = append(out, s)
			if p.Context != "" {
				preset = append(preset, contextVarPrefix+p.Context)
			} else {
				preset = append(preset, p.Field.SymName())
			}
			continue
		}
		if synthesizedCall(s) {
			continue
		}
		out = append(out, testMountCall(comp, preset))
		return append(out, block[i:]...)
	}
	return append(out, testMountCall(comp, preset))
}

// synthesizedCall reports whether s calls what lowering wrote: a write's
// redraw, a mark or a settle.
func synthesizedCall(s ir.Stmt) bool {
	cs, ok := s.(*ir.CallStmt)
	return ok && cs.Call != nil && cs.Call.Func != nil && cs.Call.Func.Synthesized && cs.Call.Func.Intrinsic == ""
}

// testMountCall mounts comp's instance with the names in preset left as the
// test set them.
func testMountCall(comp *ir.Param, preset []string) ir.Stmt {
	call := &ir.Call{
		Type: ir.TypVoid,
		Func: &ir.Func{Name: "mount", Intrinsic: ir.TestMountID, Return: ir.TypVoid, Synthesized: true},
		Args: []ir.CallArg{{Value: &ir.Ident{Name: comp.Name, Type: comp.Type, Sym: comp}}},
	}
	for _, name := range preset {
		call.Args = append(call.Args, ir.CallArg{Value: &ir.Literal{Type: ir.TypString, Value: name}})
	}
	return &ir.CallStmt{Call: call}
}

// contextVarPrefix begins the name of the var passContext gives a context a
// body reads, `__ctx_<name>`.
const contextVarPrefix = "__ctx_"
