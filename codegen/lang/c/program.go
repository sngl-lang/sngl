package c

import (
	"fmt"
	"io"
	"strings"

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
