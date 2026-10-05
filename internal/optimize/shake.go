package optimize

import (
	"fmt"
	"log/slog"
	"slices"

	"git.duckfam.us/jonathan/sngl/ir"
)

// shakeUnused removes consts, vars, functions, structs, and enums that are
// not reachable from roots (components, windows, timers, outputs, tests).
//
// documents says the target writes each window out one document at a time
// (Documents), unrolling its view loops against the consts they walk. A const
// only such a loop reads is then a value the build needs and the output does
// not, and it moves to pkg.BuildConsts: kept as a declaration, it held the
// struct it was a list of alive with it, and every page wrote a constructor
// for a type nothing built.
func shakeUnused(pkg *ir.Package, documents bool) error {
	used, build := collectUsedSymbols(pkg, documents)

	var consts, buildConsts []*ir.Var
	for _, c := range slices.Concat(pkg.Consts, pkg.BuildConsts) {
		switch {
		case used[c]:
			consts = append(consts, c)
		case build[c]:
			buildConsts = append(buildConsts, c)
		default:
			slog.Debug("shaken: var", "name", c.Name, "const", true)
		}
	}
	pkg.Consts, pkg.BuildConsts = consts, buildConsts
	pkg.Vars = filterVars(pkg.Vars, used)
	pkg.Funcs = filterFuncs(pkg.Funcs, used)
	dropDetachedHandlers(pkg)
	pkg.Structs = filterStructs(pkg.Structs, used)
	pkg.Enums = filterEnums(pkg.Enums, used)
	return promoteForeignStructs(pkg)
}

// dropDetachedHandlers removes each handler passDeclarative promoted to a
// func whose node a later fold removed: the AttachHandler naming it went with
// the branch, so nothing attaches it, and emitted it names a widget no build
// creates. A branch decided only once components are inlined is how that
// happens -- `sngl:ui/markup`'s listItem renders its checkbox under
// `if task == Task.none`, whose operands meet as constants after the splice.
//
// Asked of the package's funcs and every component's, because a root
// component keeps its own, and only of promoted handlers: every other
// synthesized func is reached through scaffolding this walk cannot read.
func dropDetachedHandlers(pkg *ir.Package) {
	named := map[*ir.Func]bool{}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok {
			if f, ok := id.Sym.(*ir.Func); ok {
				named[f] = true
			}
		}
		return nil
	})
	keep := func(fs []*ir.Func) []*ir.Func {
		return slices.DeleteFunc(fs, func(f *ir.Func) bool {
			return f != nil && f.LoweredFromNode != "" && !named[f]
		})
	}
	pkg.Funcs = keep(pkg.Funcs)
	for _, c := range pkg.Components {
		if c != nil {
			c.Funcs = keep(c.Funcs)
		}
	}
}

// promoteForeignStructs adds a library package's struct to this package's list
// when a surviving function's signature names it.
//
// Every backend emits its type declarations from pkg.Structs, which holds the
// program's own -- so a struct another package declares has no declaration in
// the output, and `shapes.Point` in a promoted helper's signature arrived in
// the generated Go as the undefined type `Point`.
//
// Here rather than in the lowering that promotes the functions, because only
// after the filters above is it known which of them survive: a helper that was
// inlined into its caller and then shaken leaves a signature nobody writes,
// and promoting its types added a declaration nothing referenced.
//
// Signatures only. A struct a body merely *builds* needs no declaration in a
// language whose literals carry none, and reaching wider pulled in `ui.Style`
// -- named by every program -- which html then emitted a constructor for.
//
// A foreign struct is skipped: it *is* a host type, and a declaration for it
// would shadow the thing it names.
func promoteForeignStructs(pkg *ir.Package) error {
	have := map[*ir.StructDef]bool{}
	byName := map[string]*ir.StructDef{}
	for _, sd := range pkg.Structs {
		have[sd] = true
		byName[sd.Name] = sd
	}
	var added []*ir.StructDef
	haveEnum := map[*ir.EnumDef]bool{}
	for _, ed := range pkg.Enums {
		haveEnum[ed] = true
	}
	var addedEnums []*ir.EnumDef
	want := func(t *ir.Type) {
		if t != nil && t.Kind == ir.TypeEnum {
			// Kotlin emits an enum as a class of its own, so a helper taking
			// `markup.Token` named a type the file never declared.
			if ed, ok := t.Decl.(*ir.EnumDef); ok && ed != nil && !haveEnum[ed] && ed.Pkg != "" && ed.Foreign.Name == "" {
				haveEnum[ed] = true
				addedEnums = append(addedEnums, ed)
			}
			return
		}
		if t == nil || t.Kind != ir.TypeStruct {
			return
		}
		sd, ok := t.Decl.(*ir.StructDef)
		if !ok || sd == nil || have[sd] || sd.Pkg == "" || sd.Foreign.Name != "" {
			return
		}
		have[sd] = true
		added = append(added, sd)
	}
	for _, fn := range pkg.Funcs {
		if fn == nil || fn.Pkg == "" {
			continue
		}
		for _, p := range fn.Params {
			want(p.Type)
		}
		want(fn.Return)
	}
	for _, t := range handlerPayloadTypes(pkg) {
		// sngl:builtin's `error`, an @error handler's, is each backend's own
		// to spell.
		if sd, ok := t.Decl.(*ir.StructDef); ok && sd != nil && sd.Builtin == ir.BuiltinNone && sd.Pkg != "sngl:builtin" {
			want(t)
		}
	}
	// Promotion is keyed by declaration and every backend emits by name, so a
	// promoted library struct whose name the program also declares would be a
	// second `type Point struct` in one file -- Go refuses it, and JS takes
	// whichever came last. Renaming is the fix this wants, and it is not a
	// rename this pass can make: the declaration is shared with the memoized
	// library package, so writing to it would reach every other build in the
	// process, and the types that point at it are shared too.
	//
	// So it is reported. A build that stops naming both declarations is worth
	// more than emitted code that does not compile, or silently uses the wrong
	// type.
	for _, sd := range added {
		if other, clash := byName[sd.Name]; clash {
			return fmt.Errorf("%s declares struct %q and so does %s, which a function this build emits names in its signature; "+
				"every target emits a struct by its name, so the two cannot both be declared -- rename the local one",
				pkgLabel(other.Pkg), sd.Name, pkgLabel(sd.Pkg))
		}
		byName[sd.Name] = sd
	}
	pkg.Structs = append(pkg.Structs, added...)
	pkg.Enums = append(pkg.Enums, addedEnums...)
	return nil
}

// handlerPayloadTypes is the payload type of every event handler the program
// writes and of every payload a test hands an event trigger. A backend that
// keeps the handler's parameter declares it by that type, and a test's
// `c.box.change({checked=true})` constructs one.
func handlerPayloadTypes(pkg *ir.Package) []*ir.Type {
	var out []*ir.Type
	visit := func(n ir.Node) error {
		switch v := n.(type) {
		case *ir.NodeInst:
			// A node a `#id` names gets a test invoker per event it
			// declares, whether or not the program subscribes; and a node
			// with a two-way prop, bound or not, has its value written back
			// from the event's payload with no handler written anywhere. The
			// emitters prune what nothing reads.
			if v.Component != nil && (v.Handle != nil || hasTwoWayProp(v.Component)) {
				for _, e := range v.Component.Events {
					if t := e.Payload(); t != nil {
						out = append(out, t)
					}
				}
			}
			for _, h := range v.Handlers {
				if h.Func != nil && !h.Func.Synthesized {
					for _, p := range h.Func.Params {
						out = append(out, p.Type)
					}
				}
			}
		case *ir.Lambda:
			// A closure's parameter is in its signature, which a Go func
			// type spells: a handler lifted out of a component built at run
			// time travels in as one.
			if v.Func != nil {
				for _, p := range v.Func.Params {
					out = append(out, p.Type)
				}
			}
		case *ir.Call:
			if v.Event != "" {
				for _, a := range v.Args {
					out = append(out, a.Value.ExprType())
				}
			}
		}
		return nil
	}
	for _, o := range ir.Owners(pkg) {
		_ = ir.Walk(*o.Body, visit)
	}
	// And an event a component the package holds declares, since an
	// instance record's constructor and setters name its type.
	for _, c := range pkg.Components {
		if c == nil {
			continue
		}
		for _, e := range c.Events {
			for _, p := range e.Params {
				out = append(out, p.Type)
			}
		}
	}
	for _, fn := range pkg.Funcs {
		if fn != nil && fn.IsTest {
			_ = ir.Walk(fn.Block, visit)
		}
	}
	return out
}

func hasTwoWayProp(c *ir.Component) bool {
	for _, p := range c.Props {
		if p.Bidirectional {
			return true
		}
	}
	return false
}

// pkgLabel names a package in a diagnostic. `StructDef.Pkg` already carries
// the `sngl:` prefix for a library declaration and is empty for the program's
// own.
func pkgLabel(pkg string) string {
	if pkg == "" {
		return "this program"
	}
	return pkg
}

func filterVars(vars []*ir.Var, used map[ir.Symbol]bool) []*ir.Var {
	var out []*ir.Var
	for _, v := range vars {
		if used[v] {
			out = append(out, v)
		} else {
			slog.Debug("shaken: var", "name", v.Name, "const", v.IsConst)
		}
	}
	return out
}

func filterFuncs(funcs []*ir.Func, used map[ir.Symbol]bool) []*ir.Func {
	var out []*ir.Func
	for _, f := range funcs {
		// A synthesized func is the lowering's, and what calls it is usually
		// the platform's own scaffolding rather than IR this walk can read --
		// a focus helper called from the Model's key handling, a slot render
		// called from a factory. It used to be rooted by sitting in a window's
		// Funcs; the window owns nothing now, so the flag is what says it.
		if used[f] || f.IsTest || f.Synthesized {
			out = append(out, f)
		} else {
			slog.Debug("shaken: func", "name", f.Name)
		}
	}
	return out
}

func filterStructs(structs []*ir.StructDef, used map[ir.Symbol]bool) []*ir.StructDef {
	var out []*ir.StructDef
	for _, s := range structs {
		if used[s] {
			out = append(out, s)
		} else {
			slog.Debug("shaken: struct", "name", s.Name)
		}
	}
	return out
}

func filterEnums(enums []*ir.EnumDef, used map[ir.Symbol]bool) []*ir.EnumDef {
	var out []*ir.EnumDef
	for _, e := range enums {
		if used[e] {
			out = append(out, e)
		} else {
			slog.Debug("shaken: enum", "name", e.Name)
		}
	}
	return out
}

// collectUsedSymbols finds all symbols transitively reachable from roots, and
// with documents the consts and structs reachable only from a view loop's
// head or a view `if`'s condition, which Documents folds away.
func collectUsedSymbols(pkg *ir.Package, documents bool) (used, build map[ir.Symbol]bool) {
	used = make(map[ir.Symbol]bool)
	build = make(map[ir.Symbol]bool)
	var walk, buildWalk func(ir.Symbol)
	view := func(stmts []ir.Stmt) {
		walkStmts(stmts, used, walk)
	}
	if documents {
		view = func(stmts []ir.Stmt) {
			walkViewStmts(stmts, walk, buildWalk)
		}
	}
	// Only a const and the types it is built of: a func or a var a loop head
	// names may still be called when the loop is not one Documents can fold,
	// and lowering turns a loop over state into a render func that reads it.
	buildWalk = func(sym ir.Symbol) {
		if sym == nil || used[sym] || build[sym] {
			return
		}
		switch s := sym.(type) {
		case *ir.Var:
			if !s.IsConst {
				walk(sym)
				return
			}
			build[sym] = true
			walkType(s.Type, used, buildWalk)
			walkExpr(s.Init, used, buildWalk)
		case *ir.StructDef:
			build[sym] = true
			for _, f := range s.Fields {
				walkType(f.Type, used, buildWalk)
				walkExpr(f.Default, used, buildWalk)
			}
		default:
			walk(sym)
		}
	}

	walk = func(sym ir.Symbol) {
		if sym == nil || used[sym] {
			return
		}
		used[sym] = true

		switch s := sym.(type) {
		case *ir.Var:
			// Walk the declared type: a struct/enum referenced only as a
			// type (e.g. `var x option<User> = null`) must stay reachable,
			// or the shaker prunes its definition while codegen still emits
			// the type reference → "undefined: User".
			walkType(s.Type, used, walk)
			walkExpr(s.Init, used, walk)
			for _, h := range s.Handlers {
				walkFunc(h.Func, used, walk)
			}
		case *ir.Func:
			walkStmts(s.Block, used, walk)
			for _, p := range s.Params {
				walkType(p.Type, used, walk)
				if p.Default != nil {
					walkExpr(p.Default, used, walk)
				}
			}
			walkType(s.Return, used, walk)
		case *ir.StructDef:
			for _, f := range s.Fields {
				walkType(f.Type, used, walk)
				if f.Default != nil {
					walkExpr(f.Default, used, walk)
				}
			}
		case *ir.EnumDef:
			for _, m := range s.Members {
				if m.Value != nil {
					walkExpr(m.Value, used, walk)
				}
			}
		case *ir.Context:
			// A read of the context is a read of its default wherever nothing
			// provides one, and the lowering writes the default there after
			// this walk has run: unwalked, `context #t(initial)` left `initial`
			// shaken and every Go target and android naming it undeclared.
			walkType(s.Typ, used, walk)
			walkExpr(s.Default, used, walk)
		case *ir.Component:
			for _, p := range s.Props {
				walkType(p.Type, used, walk)
				if p.Default != nil {
					walkExpr(p.Default, used, walk)
				}
			}
			for _, v := range s.Vars {
				walk(v)
			}
			for _, f := range s.Funcs {
				walk(f)
			}
			if s.Name == pkg.RootComponent {
				view(s.Body)
			} else {
				walkStmts(s.Body, used, walk)
			}
		}
	}

	// Roots: the package body, components, windows, timers, outputs, test
	// functions. The package body is a root like a window's: it is rendered,
	// so what it reads is used. Left out, a top-level `var` looked unread and
	// was shaken away while the body kept referring to it.
	view(pkg.Body)
	for _, comp := range pkg.Components {
		walk(comp)
	}
	// Test functions are roots, and so is every synthesized func, which
	// filterFuncs keeps whether or not anything names it: kept and not
	// walked, a focus helper outlived the __focusID it reads.
	for _, f := range pkg.Funcs {
		if f.IsTest || f.Synthesized {
			walk(f)
		}
	}
	// Vars with handlers are roots (reactive state).
	for _, v := range pkg.Vars {
		if len(v.Handlers) > 0 {
			walk(v)
		}
	}
	// The entry points the lowering left for a platform to call. Nothing in
	// the IR calls any of them -- each platform emits the call from its own
	// scaffolding, off these same fields -- so the reference the package
	// holds is the only one there is, and walking it is what keeps what the
	// body calls alive too: the per-effect `__effectN_teardown`, the updaters
	// a settle re-runs.
	//
	// They reach this walk at all only because Optimize runs a second time
	// after Lower, which is where they are synthesized; before that they
	// survived by sitting in a window's Funcs, and in pkg.Funcs they are
	// filtered like anything else nothing names. Teardown was rooted on its
	// own and the other two were not, so `__remoteSettled` was shaken while
	// fyne's `OnSettle` subscription still named it.
	//
	// Guarded rather than handed straight to walk: a nil *ir.Func in an
	// ir.Symbol is not a nil interface, so the `sym == nil` gate at the top
	// lets it through to the *ir.Func arm and the field read panics.
	for _, fn := range pkg.EntryPoints() {
		walk(fn)
	}

	return used, build
}

func walkFunc(f *ir.Func, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	if f == nil {
		return
	}
	walkStmts(f.Block, used, walk)
}

func walkStmts(stmts []ir.Stmt, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	for _, s := range stmts {
		walkStmt(s, used, walk)
	}
}

func walkStmt(s ir.Stmt, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	if s == nil {
		return
	}
	switch n := s.(type) {
	case *ir.NodeInst:
		walkNode(n, used, walk)
		walkStmts(n.Children, used, walk)
		walkSlots(n.Slots, used, walk)
	case *ir.CallStmt:
		if n.Call != nil {
			walkCallExpr(n.Call, used, walk)
		}
	case *ir.SlotInst:
		for _, a := range n.Args {
			walkExpr(a, used, walk)
		}
		walkStmts(n.Children, used, walk)
		walkSlots(n.Slots, used, walk)
	case *ir.Assign:
		walkExpr(n.Target, used, walk)
		walkExpr(n.Value, used, walk)
	case *ir.Toggle:
		walkExpr(n.Target, used, walk)
	case *ir.Emit:
		for _, a := range n.Args {
			walkExpr(a.Value, used, walk)
		}
	case *ir.LocalVar:
		walkType(n.Type, used, walk)
		walkExpr(n.Init, used, walk)
		// A flattened canvas carries the statements that paint it on the node
		// it replaced. They call what the platform package's shapes call --
		// fyne's applyStyle, gtk4's paint -- and reaching them is what keeps
		// those declarations from being shaken as unreferenced.
		if n.CanvasNode != nil {
			walkStmts(n.CanvasNode.Children, used, walk)
		}
	case *ir.Return:
		walkExpr(n.Value, used, walk)
	case *ir.If:
		walkExpr(n.Cond, used, walk)
		walkStmts(n.Body, used, walk)
		walkStmts(n.Else, used, walk)
		// A catch block renders its handler where it stands, so what the handler
		// names is used here whether or not its owner survived.
		if n.Catch != nil && n.Catch.Func != nil {
			walkStmts(n.Catch.Func.Block, used, walk)
		}
	case *ir.For:
		walkExpr(n.Iter, used, walk)
		walkStmts(n.Body, used, walk)
		walkStmts(n.Else, used, walk)
	case *ir.ContextProvider:
		walkExpr(n.Value, used, walk)
		walkStmts(n.Children, used, walk)
	case *ir.ErrorBoundary:
		if n.Handler != nil && n.Handler.Func != nil {
			walkFunc(n.Handler.Func, used, walk)
		}
		walkStmts(n.Children, used, walk)
		// The fallback renders once a raise reaches the boundary, and what it
		// names is as used as what the content names.
		walkStmts(n.Failed, used, walk)
	case *ir.CanvasRedrawStmt:
		// Carries only NodeInst/Func pointers already tracked by other walk paths.
	case *ir.Break, *ir.Continue:
		// A loop escape names no declaration.
	default:
		panic(fmt.Sprintf("walkStmt: unhandled stmt %T", n))
	}
}

func walkExpr(e ir.Expr, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Ident:
		if x.Sym != nil {
			walk(x.Sym)
		}
	case *ir.Binary:
		walkExpr(x.Left, used, walk)
		walkExpr(x.Right, used, walk)
	case *ir.Unary:
		walkExpr(x.Operand, used, walk)
	case *ir.Ternary:
		walkExpr(x.Cond, used, walk)
		walkExpr(x.Then, used, walk)
		walkExpr(x.Else, used, walk)
	case *ir.Call:
		walkCallExpr(x, used, walk)
	case *ir.Conversion:
		walkExpr(x.Operand, used, walk)
	case *ir.Select:
		walkExpr(x.Operand, used, walk)
	case *ir.Index:
		walkExpr(x.Operand, used, walk)
		walkExpr(x.Idx, used, walk)
	case *ir.ListLit:
		for _, el := range x.Elems {
			walkExpr(el, used, walk)
		}
	case *ir.StructLit:
		if x.Def != nil {
			walk(x.Def)
		}
		for _, f := range x.Fields {
			walkExpr(f.Value, used, walk)
		}
	case *ir.Spread:
		walkExpr(x.Operand, used, walk)
	case *ir.Lambda:
		if x.Func != nil {
			walkFunc(x.Func, used, walk)
		}
	case *ir.Closure:
		if x.Func != nil {
			walkFunc(x.Func, used, walk)
		}
		if x.State != nil {
			walkExpr(x.State, used, walk)
		}
	case *ir.MapLitIR:
		for _, kv := range x.Entries {
			walkExpr(kv.Key, used, walk)
			walkExpr(kv.Value, used, walk)
		}
	case *ir.ContextRead:
		if x.Ref != nil {
			walk(x.Ref)
		}
	case *ir.Literal:
		// No subexpressions or referenced symbols.
	default:
		panic(fmt.Sprintf("walkExpr: unhandled expr %T", x))
	}
}

func walkCallExpr(call *ir.Call, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	if call.Func != nil {
		walk(call.Func)
	}
	walkExpr(call.Receiver, used, walk)
	// Callee is where a call to something other than a declaration keeps its
	// target: `h.run()` on a func-valued struct field is a Select on `h`, and
	// Func is nil. Missed here, nothing reached `h` and the var was shaken
	// while the handler that calls it kept naming it -- `await h__inst0.run()`
	// against a `state` object with no such field, in emitted JS that a golden
	// records as passing because nothing runs it.
	walkExpr(call.Callee, used, walk)
	for _, a := range call.Args {
		walkExpr(a.Value, used, walk)
	}
	// A call's own @error is rendered at the call (catchAtCall), so what it
	// names is used there. Missed, a var only the handler wrote was shaken
	// while the handler kept writing it: `problem__inst2 = …` against a Model
	// with no such field.
	if call.ErrorHandler != nil {
		walkFunc(call.ErrorHandler.Func, used, walk)
	}
}

func walkType(t *ir.Type, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	if t == nil {
		return
	}
	if t.Decl != nil {
		walk(t.Decl)
	}
	for _, elem := range t.Elems {
		walkType(elem, used, walk)
	}
	if t.Sig != nil {
		for _, p := range t.Sig.Params {
			walkType(p.Type, used, walk)
		}
		walkType(t.Sig.Return, used, walk)
	}
}

// walkViewStmts is walkStmts over a view Documents will unroll: a loop's head
// and a condition are folded against the loop variables there, so the consts
// they read are the build's rather than the page's.
func walkViewStmts(stmts []ir.Stmt, walk, buildWalk func(ir.Symbol)) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			walkNode(n, nil, walk)
			walkViewStmts(n.Children, walk, buildWalk)
			walkSlots(n.Slots, nil, walk)
		case *ir.If:
			walkExpr(n.Cond, nil, buildWalk)
			walkViewStmts(n.Body, walk, buildWalk)
			walkViewStmts(n.Else, walk, buildWalk)
		case *ir.For:
			walkExpr(n.Iter, nil, buildWalk)
			walkViewStmts(n.Body, walk, buildWalk)
			walkViewStmts(n.Else, walk, buildWalk)
		case *ir.SlotInst:
			// What an insertion hands its population, as walkStmt reads it.
			for _, a := range n.Args {
				walkExpr(a, nil, walk)
			}
			walkViewStmts(n.Children, walk, buildWalk)
			walkSlots(n.Slots, nil, walk)
		case *ir.ContextProvider:
			walkExpr(n.Value, nil, walk)
			walkViewStmts(n.Children, walk, buildWalk)
		case *ir.ErrorBoundary:
			if n.Handler != nil {
				walkFunc(n.Handler.Func, nil, walk)
			}
			walkViewStmts(n.Children, walk, buildWalk)
			walkViewStmts(n.Failed, walk, buildWalk)
		default:
			walkStmt(s, nil, walk)
		}
	}
}

func walkSlots(slots map[string]*ir.SlotContent, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	for _, name := range ir.SlotNames(slots) {
		walkStmts(slots[name].Body, used, walk)
	}
}

// walkNode walks what a node names itself: its component, props, handlers
// and route parameters, and not its children.
func walkNode(n *ir.NodeInst, used map[ir.Symbol]bool, walk func(ir.Symbol)) {
	if n.Component != nil {
		walk(n.Component)
	}
	for _, p := range n.Props {
		walkExpr(p.Value, used, walk)
	}
	for _, h := range n.Handlers {
		walkFunc(h.Func, used, walk)
	}
	// A window's route parameters name a struct the program may declare
	// and never construct: the request fills the cell, and a target with
	// no request renders its zero. Nothing else reaches that declaration,
	// so without this the page read `v.pkg` off a type no file declared.
	if n.Params != nil {
		walkType(n.Params.Type, used, walk)
	}
}
