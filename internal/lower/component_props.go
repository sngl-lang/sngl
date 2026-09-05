package lower

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passComponentProps gives a component that survives inlining a settable prop.
//
// A prop is a parameter, and a parameter cannot be assigned. That was never a
// problem while every instance was inlined: the inliner substitutes a prop's
// expression into the body, so a reactive prop crossing the boundary becomes an
// ordinary leaf updater in the caller and has worked all along. An instance
// that cannot be inlined -- one under a dynamic `for`, or in a recursive cycle
// -- keeps its parameters, and nothing can write them, so the prop reached
// nothing at all.
//
// Promoting each prop to a var of the component is the whole of the fix, and
// deliberately the whole of this pass. passReactivity then treats it as the
// reactive cell it now is: the setter's assignment gets that prop's leaf
// updaters spliced in after it by the ordinary injection, and a prop feeding a
// condition inside the body gets a slot re-fire by the same route. Neither is
// written here, and neither is a rebuild of the instance -- which would throw
// away the subtree whose state the instance was retained to keep.
//
// The exception is a #[construct] prop, which is skipped: a cell nothing reads
// after construction is what made such a prop's new value vanish silently. The
// render rebuilds the instance for it instead (see reuseOrCreate).
var passComponentProps = pass{
	name:    "ComponentProps",
	enabled: hasInstanceRuntime,
	apply:   lowerComponentProps,
}

func lowerComponentProps(pkg *ir.Package, _ Caps, opts Options) error {
	if pkg == nil {
		return nil
	}
	// The root has no caller, so nothing sets its props: it is the entry
	// point, and what it declares as props is bound once by the host.
	root := rootComponent(pkg, opts)
	for _, c := range pkg.Components {
		// RuntimeInstance is the mark the inliner leaves on a declaration it
		// met an instantiation of that it could not flatten, and a cell is
		// only ever reached through such an instance's setter. A declaration
		// without the mark is still on the list -- a recursive component is
		// its own caller, so it survives even when every call site of it
		// unrolled at build time -- and promoting its props there is not
		// merely wasted: the cells make passReactivity treat the component's
		// own control flow as a render slot, and the page ends up carrying a
		// slot renderer that reads cells only a factory declares, for a
		// factory no backend emits.
		if c == root || !c.RuntimeInstance {
			continue
		}
		promoteProps(c)
	}
	return nil
}

// promoteProps turns each of c's props into a var initialised from the
// parameter, rewrites the body to read the var, and gives each one a setter.
func promoteProps(c *ir.Component) {
	if c == nil || len(c.Props) == 0 {
		return
	}
	symRenames := make(map[ir.Symbol]ir.Symbol, len(c.Props))
	renames := make(map[ir.Symbol]string, len(c.Props))
	vars := make([]*ir.Var, 0, len(c.Props))
	setters := make([]*ir.Func, 0, len(c.Props))

	for _, p := range c.Props {
		if p == nil || p.Sym == nil {
			continue
		}
		// A #[construct] prop is read while the instance is built and never
		// again, so it gets neither cell nor setter: the parameter it already
		// is says exactly that. componentAbsorbs then reports it as
		// unwritable, and the render rebuilds the instance instead.
		if p.Construct {
			continue
		}
		v := &ir.Var{
			Name: propVarName(p.Name),
			Type: p.Type,
			// The parameter still carries the value in: promoting the prop
			// gives it somewhere writable to live, not a second way to arrive.
			Init:        &ir.Ident{Name: p.Name, Type: p.Type, Sym: p.Sym},
			Synthesized: true,
		}
		vars = append(vars, v)
		symRenames[ir.Symbol(p.Sym)] = v
		renames[ir.Symbol(p.Sym)] = v.Name
		setters = append(setters, propSetter(p, v))
	}
	if len(vars) == 0 {
		return
	}

	// Every read of the parameter becomes a read of the var -- except the
	// initializers just built, which are the one place the parameter is still
	// meant to be read.
	c.Body = renameIdents(c.Body, renames, symRenames)
	for _, f := range c.Funcs {
		f.Block = renameIdents(f.Block, renames, symRenames)
	}
	for _, v := range c.Vars {
		v.Init = renameInExpr(v.Init, renames, symRenames)
		for _, h := range v.Handlers {
			if h.Func != nil {
				h.Func.Block = renameIdents(h.Func.Block, renames, symRenames)
			}
		}
	}
	for _, t := range c.Timers {
		t.Interval = renameInExpr(t.Interval, renames, symRenames)
		t.Enabled = renameInExpr(t.Enabled, renames, symRenames)
		if t.Handler != nil {
			t.Handler.Block = renameIdents(t.Handler.Block, renames, symRenames)
		}
	}

	// Ahead of the component's own vars, because a var initializer may read a
	// prop and every backend initialises a record's cells in this order. A
	// promoted prop reads only its parameter, so nothing of the component's
	// can precede it.
	c.Vars = append(vars, c.Vars...)
	c.Funcs = append(c.Funcs, setters...)
}

// propSetter is the function UpdateComponent reaches for one prop: it writes
// the cell, and passReactivity appends whatever reads that cell.
func propSetter(p *ir.Prop, v *ir.Var) *ir.Func {
	param := &ir.Param{Name: "__v", Type: p.Type}
	return &ir.Func{
		Name:   ir.ComponentSetter(p.Name),
		Params: []*ir.Param{param},
		Return: ir.TypVoid,
		Purity: ir.PurityMutates,
		Block: []ir.Stmt{&ir.Assign{
			Target: &ir.Ident{Name: v.Name, Type: v.Type, Sym: v, Synthesized: true},
			Op:     ast.AssignSet,
			Value:  &ir.Ident{Name: param.Name, Type: param.Type, Sym: param},
		}},
	}
}

// propVarName is the cell a prop is promoted to. Prefixed rather than shadowing
// the parameter, because the parameter is still what carries the value in and
// one name for both would make the initializer read itself.
func propVarName(prop string) string { return "__prop_" + prop }
