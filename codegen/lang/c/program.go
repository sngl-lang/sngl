package c

import (
	"fmt"
	"io"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// WriteProgram writes pkg into fe whole: a program passPlacement left plain,
// whose every node operation is already a call of its platform's protocol
// functions, so nothing here knows a platform.
//
// Each package var and const is a static of the file, set by sngl_init,
// since a test sets the program's state afresh before it builds; each
// function the program declares, and each a closure reaches that none
// declares -- a lambda lifted out of a component the inliner spliced away --
// is written as a function of the file; and the package body is sngl_build.
// The platform writes the entry that calls the two, in a turn.
func WriteProgram(fe *FileEmitter, pkg *ir.Package) error {
	ctx := fe.Context()
	vars := append(append([]*ir.Var(nil), pkg.Consts...), pkg.Vars...)
	var statics strings.Builder
	for _, v := range vars {
		fmt.Fprintf(&statics, "static %s %s;\n", ctx.TypeName(v.Type), Decl(&v.DeclInfo))
	}
	fe.Prepend("\n" + statics.String())

	written := map[*ir.Func]bool{}
	var write func(fn *ir.Func)
	write = func(fn *ir.Func) {
		if written[fn] || fn.IsTest || Bodiless(fn) || fn.Foreign.Name != "" || fn.Intrinsic != "" {
			return
		}
		written[fn] = true
		fe.Func(fn)
		strayClosures(fn.Block, write)
	}
	for _, fn := range pkg.Funcs {
		write(fn)
	}
	strayClosures(pkg.Body, write)
	for _, v := range vars {
		strayClosures(v.Init, write)
	}

	var b strings.Builder
	b.WriteString("\nstatic void sngl_init(void) {\n")
	for _, v := range vars {
		if v.Init == nil {
			continue
		}
		// A null is the zero of the var's type, which is a struct for an
		// option, a list, a map and a function.
		value := ctx.Keep(v.Type, ctx.EvalExpr(v.Init))
		if ir.IsNullLiteral(v.Init) {
			value = "(" + ctx.TypeName(v.Type) + "){0}"
		}
		fmt.Fprintf(&b, "\t%s = %s;\n", Decl(&v.DeclInfo), value)
	}
	b.WriteString("}\n\nstatic void sngl_build(void) {\n")
	for _, l := range ctx.EvalBlock(pkg.Body) {
		b.WriteString("\t" + l + "\n")
	}
	b.WriteString("}\n")
	if _, err := io.WriteString(fe, b.String()); err != nil {
		return err
	}
	return fe.Err()
}

// strayClosures hands write the function of each closure root makes.
func strayClosures(root any, write func(*ir.Func)) {
	if root == nil {
		return
	}
	_ = ir.Walk(root, func(n ir.Node) error {
		if cl, ok := n.(*ir.Closure); ok && cl.Func != nil {
			write(cl.Func)
		}
		return nil
	})
}

// WriteTests writes pkg's tests into fe as the agent's, after WriteProgram:
// a function each, and sngl_tests, the table sngl_test_serve is handed. A
// test builds the program where it mounts the component it takes, or first
// where it takes none, each from state set afresh. step is the call that
// lets the program's loop run a moment, which a test's wait makes between
// asking its predicate; a placed test's every other call is the program's
// own (passPlacement), so nothing here knows a platform.
func WriteTests(fe *FileEmitter, pkg *ir.Package, step string) error {
	ctx := fe.Context()
	var tName string
	prev := ctx.Native
	ctx.Native = func(c *Context, n *ir.Call) (string, bool) {
		if out, ok := testCall(c, n, tName, step); ok {
			return out, true
		}
		if prev != nil {
			return prev(c, n)
		}
		return "", false
	}
	defer func() { ctx.Native = prev }()
	teardown := ""
	if pkg.Teardown != nil {
		teardown = "\t\t" + Decl(&pkg.Teardown.DeclInfo) + "();\n"
	}
	var b strings.Builder
	b.WriteString("\nstatic void sngl_reset(void) {\n\tstatic bool run;\n\tif (run) {\n" + teardown + "\t}\n\trun = true;\n\tsngl_init();\n}\n")
	var table []string
	for _, fn := range pkg.Funcs {
		if !fn.IsTest {
			continue
		}
		tName = "t"
		if len(fn.Params) > 0 {
			tName = fn.Params[0].Name
		}
		name := "sngl_" + fn.Name
		fmt.Fprintf(&b, "\nstatic void %s(sngl_t *%s) {\n\tsngl_turn_enter();\n\tsngl_reset();\n", name, Local(tName))
		if !mounts(fn) {
			b.WriteString("\tsngl_build();\n")
		}
		block := SetContexts(fn.Block, func(name string) *ir.Var {
			for _, v := range pkg.Vars {
				if v.Name == name {
					return v
				}
			}
			return nil
		})
		for _, l := range ctx.EvalBlock(block) {
			b.WriteString("\t" + l + "\n")
		}
		b.WriteString("\tsngl_turn_leave();\n}\n")
		table = append(table, fmt.Sprintf("\t{%q, %s},", strings.TrimPrefix(fn.Name, "test"), name))
	}
	if len(table) == 0 {
		b.WriteString("\nstatic const sngl_test *sngl_tests = NULL;\nstatic const size_t sngl_ntests = 0;\n")
	} else {
		b.WriteString("\nstatic const sngl_test sngl_tests[] = {\n" + strings.Join(table, "\n") + "\n};\nstatic const size_t sngl_ntests = sizeof sngl_tests / sizeof sngl_tests[0];\n")
	}
	if _, err := io.WriteString(fe, b.String()); err != nil {
		return err
	}
	return fe.Err()
}

// mounts reports whether fn mounts the component it takes, which builds the
// program where it does (passTestMount).
func mounts(fn *ir.Func) bool {
	found := false
	_ = ir.Walk(fn.Block, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && ir.IsTestCall(c, ir.TestMountID) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// testCall is n as the test being written makes it, or false where n is no
// call only a test makes.
func testCall(ctx *Context, n *ir.Call, tName, step string) (string, bool) {
	switch {
	case ir.IsTestCall(n, ir.TestMountID):
		return "sngl_build()", true
	case ir.IsTestCall(n, ir.TestAssertID), ir.IsTestCall(n, ir.TestMustID):
		cond := ctx.EvalExpr(n.Args[1].Value)
		fail, word := "sngl_test_fail", "assert"
		if ir.IsTestCall(n, ir.TestMustID) {
			fail, word = "sngl_test_fail_now", "must"
		}
		return "((" + cond + ") ? (void)0 : " + fail + "(" + Local(tName) + ", " + StringLiteral(word+" failed: "+cond) + "))", true
	case ir.IsTestCall(n, ir.TestWaitID):
		pred := n.Args[1].Value
		fnT := ctx.TypeName(pred.ExprType())
		name := fnT + "_wait"
		ctx.Helper(name, "static void "+name+"("+fnT+" p, int64_t ms) {\n\tlong long end = sngl_test_now_ms() + ms;\n\twhile (!p.fn(p.env) && sngl_test_now_ms() < end) {\n\t\t"+step+";\n\t}\n}\n")
		return name + "(" + ctx.EvalExpr(pred) + ", " + ctx.EvalExpr(n.Args[2].Value) + ")", true
	case ir.IsTestCall(n, ir.TestSnapshotID):
		return "sngl_test_snapshot(" + Local(tName) + ", " + ctx.EvalExpr(n.Args[1].Value) + ")", true
	case ir.IsTestMethodCall(n):
		ctx.fail("a test's t.%s", n.Func.Name)
		return "(void)0", true
	}
	return "", false
}

// SetContexts is stmts with each `t.setContext(name, v)` the write of v to the
// var the program holds the context in, which held names (`__ctx_<name>`), as
// Go's agent writes the Model's field: what the program reads of it after,
// it reads there, and every call the test makes after it passContext has
// already handed v. A context no var of the program's holds is passed only,
// so its setContext writes nothing.
func SetContexts(stmts []ir.Stmt, held func(name string) *ir.Var) []ir.Stmt {
	return ir.RewriteBlocks(stmts, func(block []ir.Stmt) []ir.Stmt {
		out := block[:0:0]
		for _, s := range block {
			cs, ok := s.(*ir.CallStmt)
			if !ok || cs.Call == nil {
				out = append(out, s)
				continue
			}
			name, value, ok := codegen.TestSetContext(cs.Call)
			if !ok {
				out = append(out, s)
				continue
			}
			v := held("__ctx_" + name)
			if v == nil {
				continue
			}
			var target ir.Expr = &ir.Ident{Name: v.Name, Sym: v, Type: v.Type, Synthesized: true}
			if sd, ok := v.Type.Decl.(*ir.StructDef); ok && v.Type.Kind == ir.TypeStruct && sd.RefBox {
				target = &ir.Select{Operand: target, Field: "value", Type: sd.Fields[0].Type}
			}
			out = append(out, &ir.Assign{Target: target, Op: ast.AssignSet, Value: value})
		}
		return out
	})
}
