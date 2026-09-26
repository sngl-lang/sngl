package lower

import (
	"slices"
	"strings"

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
// The exception is a #[construct] prop, which gets the cell and no setter: the
// render rebuilds the instance for it instead (componentAbsorbs asks after the
// setter, and reuseOrCreate destroys and rebuilds when there is none). The cell
// is still needed, because "read while the instance is built" is not the same
// as "read from the constructor" -- a `@mount` handler, or the position an
// effect is written at, is a *method* of the record, and a parameter is not in
// scope there. Skipped entirely, `time.timer`'s two came out of the effect
// lowering as bare `interval`/`enabled` on every target that schedules with an
// effect (testdata/timer_in_loop.txtar).
var passComponentProps = pass{
	name:    "ComponentProps",
	enabled: hasInstanceRuntime,
	apply:   lowerComponentProps,
}

func lowerComponentProps(pkg *ir.Package, _ Features, opts Options) error {
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
		setters := promoteProps(c)
		settleOnSet(c, setters, pkg.Mounts)
	}
	return nil
}

// settleOnSet has a prop's setter settle the brackets whose keys read it.
// passEffect settles after every write to a var a key reads, and ran while
// the prop was still a parameter nothing could write.
func settleOnSet(c *ir.Component, setters, mounts []*ir.Func) {
	var settles []*ir.Func
	for _, f := range c.Funcs {
		if slices.Contains(mounts, f) {
			settles = append(settles, f)
		}
	}
	for _, setter := range setters {
		assign, ok := setter.Block[0].(*ir.Assign)
		if !ok {
			continue
		}
		id, ok := assign.Target.(*ir.Ident)
		if !ok {
			continue
		}
		cell, ok := id.Sym.(*ir.Var)
		if !ok {
			continue
		}
		for _, settle := range settles {
			if readsThroughCalls(settle.Block, cell) {
				setter.Block = append(setter.Block, callOf(settle))
			}
		}
	}
}

// readsThroughCalls reports whether stmts, or a func they call, read v.
func readsThroughCalls(stmts []ir.Stmt, v *ir.Var) bool {
	seen := map[*ir.Func]bool{}
	var reads func(root any) bool
	reads = func(root any) bool {
		found := false
		_ = ir.Walk(root, func(n ir.Node) error {
			switch x := n.(type) {
			case *ir.Ident:
				if x.Sym == ir.Symbol(v) {
					found = true
				}
			case *ir.Call:
				if f := x.Func; f != nil && !seen[f] {
					seen[f] = true
					found = found || reads(f.Block)
				}
			}
			if found {
				return ir.SkipAll
			}
			return nil
		})
		return found
	}
	return reads(stmts)
}

// contextProps turns each hidden context var of c into a prop defaulting to
// the context's default. Run on a component built at run time, which nothing
// splices a provider's value into.
//
// passContext threads that value to an instance as an argument named for the
// var, and as a var the instance never took it: html's factory declared it
// from the default, so every instance read the default under any provider;
// one reading state was refused for want of a setter; and bubbletea, whose
// render function takes an instance's props as parameters, read a Model field
// that does not exist. As a prop it is a parameter on bubbletea and, through
// promoteProps, a cell with a setter on the instance runtimes.
func contextProps(c *ir.Component) {
	renames := map[ir.Symbol]string{}
	symRenames := map[ir.Symbol]ir.Symbol{}
	kept := c.Vars[:0]
	for _, v := range c.Vars {
		if !v.Synthesized || !strings.HasPrefix(v.Name, "__ctx_") {
			kept = append(kept, v)
			continue
		}
		param := &ir.Param{Name: v.Name, Type: v.Type}
		c.Props = append(c.Props, &ir.Prop{Name: v.Name, Type: v.Type, Default: v.Init, Sym: param})
		renames[v] = param.Name
		symRenames[v] = param
	}
	c.Vars = kept
	if len(renames) == 0 {
		return
	}
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
}

// promoteProps turns each of c's props into a var initialised from the
// parameter, rewrites the body to read the var, and gives each one a setter.
func promoteProps(c *ir.Component) []*ir.Func {
	if c == nil || len(c.Props) == 0 {
		return nil
	}
	symRenames := make(map[ir.Symbol]ir.Symbol, len(c.Props))
	renames := make(map[ir.Symbol]string, len(c.Props))
	vars := make([]*ir.Var, 0, len(c.Props))
	setters := make([]*ir.Func, 0, len(c.Props))

	for _, p := range c.Props {
		if p == nil || p.Sym == nil {
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
		// No setter for a #[construct] prop: its absence is what
		// componentAbsorbs reads to say the instance cannot take a new value
		// and has to be rebuilt. Nor for a const one, whose value never
		// changes after the instance is built.
		if !p.Construct && !p.Const {
			setters = append(setters, propSetter(p, v))
		}
	}
	if len(vars) == 0 {
		return nil
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

	// Ahead of the component's own vars, because a var initializer may read a
	// prop and every backend initialises a record's cells in this order. A
	// promoted prop reads only its parameter, so nothing of the component's
	// can precede it.
	c.Vars = append(vars, c.Vars...)
	c.Funcs = append(c.Funcs, setters...)
	return setters
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
