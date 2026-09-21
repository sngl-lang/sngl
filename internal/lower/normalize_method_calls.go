package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passNoImplicitRecv normalizes method calls so every *ir.Call whose
// Func has a non-empty Receiver carries the receiver value in Args[0].
//
// Two source patterns currently reach IR:
//
//	Pattern A (implicit recv):
//	  ir.Call{Func: T.method (with receiver param), Args: []}
//	Pattern B (explicit recv):
//	  ir.Call{Func: T.method, Args: [recv, ...]}
//
// Pattern A arises from bare-name component-method refs (T8 elision
// followed by implicitCall) where the source has no receiver expression
// to write. After this pass, every Call is Pattern B; codegen drops its
// "is Args empty?" branching.
//
// For component receivers, the synthesized Args[0] is an *ir.Ident
// whose Sym is the *ir.Component. Each codegen target translates that
// ident to its per-instance state expression (`state` for JS, `m` for Go).
var passNoImplicitRecv = pass{
	name:    "NoImplicitRecv",
	enabled: func(c Caps) bool { return c.NoImplicitRecv },
	apply:   lowerNoImplicitRecv,
}

func lowerNoImplicitRecv(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	// Walk the same selective root set as before: component bodies/funcs/timer
	// handlers, package funcs, window bodies/funcs (not var/const inits). A
	// callback's error (an injection failure) bubbles straight out of Inspect.
	walk := func(stmts []ir.Stmt) error { return ir.Walk(stmts, recvVisit) }
	for _, comp := range pkg.Components {
		if err := walk(comp.Body); err != nil {
			return err
		}
		for _, fn := range comp.Funcs {
			if err := walk(fn.Block); err != nil {
				return err
			}
		}
	}
	for _, fn := range pkg.Funcs {
		if err := walk(fn.Block); err != nil {
			return err
		}
	}
	if err := walk(pkg.Body); err != nil {
		return err
	}
	for _, w := range pkg.Windows {
		if err := walk(w.Body); err != nil {
			return err
		}
	}
	return nil
}

// recvVisit is the shared-visitor callback. It injects the implicit receiver
// into every method Call (the walker descends into args and receiver for us) and
// returns any injection error to bubble out. It preserves the original quirk
// that an Assign's target is not visited — only its value.
func recvVisit(n ir.Node) error {
	switch x := n.(type) {
	case *ir.Assign:
		if err := ir.Walk(x.Value, recvVisit); err != nil {
			return err
		}
		return ir.SkipDir
	case *ir.Call:
		return injectRecv(x)
	}
	return nil
}

// injectRecv prepends the synthesized receiver to a Pattern-A method call. A
// call that already carries its receiver (Args covers the params), a non-method
// call, or a receiverless func is left unchanged.
func injectRecv(c *ir.Call) error {
	if c.Func == nil || c.Func.Receiver == "" {
		return nil
	}
	if len(c.Args) >= len(c.Func.Params) {
		return nil
	}
	if len(c.Func.Params) == 0 {
		return nil
	}
	recvType := c.Func.Params[0].Type
	if recvType == nil {
		return nil
	}
	recv, err := synthRecv(recvType, c.Func.Receiver)
	if err != nil {
		return err
	}
	c.Args = append([]ir.CallArg{{Value: recv}}, c.Args...)
	return nil
}

// synthRecv returns an expression representing the implicit receiver
// for a method call whose Args[0] was elided by the checker. For
// component receivers, returns an *ir.Ident whose Sym is the
// *ir.Component (codegen translates this to per-target state).
//
// Struct and enum receivers should never reach this path.
func synthRecv(recvType *ir.Type, receiverName string) (ir.Expr, error) {
	switch d := recvType.Decl.(type) {
	case *ir.Component:
		return &ir.Ident{
			Name: d.Name,
			Type: recvType,
			Sym:  d,
		}, nil
	case *ir.StructDef:
		return nil, fmt.Errorf("noImplicitRecv: struct method %s.%s called without explicit receiver", d.Name, receiverName)
	case *ir.EnumDef:
		return nil, fmt.Errorf("noImplicitRecv: enum method %s.%s called without explicit receiver", d.Name, receiverName)
	}
	return nil, fmt.Errorf("noImplicitRecv: unsupported receiver type kind %v for %s", recvType.Kind, receiverName)
}
