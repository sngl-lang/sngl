package lower

import (
	"fmt"
	"slices"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// AsyncSpawnIntrinsic runs its one argument, a no-argument closure, without
// waiting for it. The host language answers it -- in Go a `go func(){…}()`.
//
// AsyncPostIntrinsic runs its one argument back on the thread the target
// draws on. The *platform* answers that one, because there is no such thread
// in general and no two toolkits reach theirs the same way: fyne queues onto
// the driver, gtk4 onto a GLib idle tick, Bubble Tea through a message into
// the loop it already owns.
//
// Neither is declared anywhere a program can name, for the reason ir.NodeOps
// are not: a declaration would describe nobody's contract. They exist between
// this pass and the two emitters that answer it.
const (
	AsyncSpawnIntrinsic = "async.spawn"
	AsyncPostIntrinsic  = "async.post"
)

// passAsyncOffload moves a blocking call off the thread the target draws on.
//
// It runs when the host language cannot suspend a function (Go), so a call
// that waits waits wherever it was written -- and on a UI target that is the
// goroutine painting the screen. The rewrite splits one body in three:
//
//	loading = true                 // before the first blocking call: stays put
//	spawn(func() {
//	    __async0 := fetch(url)     // the blocking calls: their own goroutine
//	    post(func() {
//	        result = __async0      // everything after: back on the UI thread
//	        loading = false
//	    })
//	})
//
// The two closures nest, so a value the answer is bound to crosses the split
// by being captured, and nothing has to be plumbed through a channel or a
// callback parameter. That is also why a blocking call keeps its ordinary
// shape in the IR: `x = fetch(u)` is still an assignment, and it is the
// *body* that is rewritten, not the call.
//
// It runs after passReactivity on purpose. By then a state write is followed
// by the updater statements that patch what reads it, and those are exactly
// what has to happen on the UI thread -- they land in the posted closure
// because they sit after the assignment, with no rule of their own.
var passAsyncOffload = pass{
	name:    "NoAsyncCalls",
	enabled: func(c Caps) bool { return c.NoAsyncCalls },
	apply:   applyAsyncOffload,
}

func applyAsyncOffload(pkg *ir.Package, caps Caps, opts Options) error {
	if pkg == nil {
		return nil
	}
	recolourAsync(pkg)
	if !caps.AsyncPost {
		return refuseAsyncOffload(pkg, opts)
	}
	st := &offloadState{}
	for _, fn := range offloadEntryPoints(pkg) {
		block, err := st.transform(fn.Block)
		if err != nil {
			return err
		}
		fn.Block = block
	}
	return nil
}

// refuseAsyncOffload reports the first blocking call in a build whose target
// cannot get back to the thread it draws on.
//
// Silence is the wrong answer twice over: the call would run on that thread
// and freeze the interface, which is the whole thing the mark exists to
// prevent, and the program would look like it worked. The refusal names the
// platform because that is what has to change.
func refuseAsyncOffload(pkg *ir.Package, opts Options) error {
	for _, block := range allBlocks(pkg) {
		for _, s := range *block {
			if !stmtWaits(s) {
				continue
			}
			return fmt.Errorf("%s: this call blocks and %s has no way to run anything back on the thread it draws on, so there is nowhere for the answer to land -- the call would freeze the interface", ir.StmtPos(s), platformOrThis(opts.Platform))
		}
	}
	return nil
}

func platformOrThis(name string) string {
	if name == "" {
		return "this platform"
	}
	return name
}

// recolourAsync runs the checker's fixpoint again over the functions lowering
// has since created.
//
// An event handler reaches the checker as an ir.EventHandler and becomes an
// ir.Func only in passInstanceEvents and passDeclarative, so the colouring that
// ran over the program's declarations never saw it -- the same gap that let a
// JS effect's @mount emit an `await` inside a function nobody wrote `async` on.
// A pass that synthesizes a body has to colour what it synthesized, and this is
// the last pass that could.
func recolourAsync(pkg *ir.Package) {
	funcs := offloadableFuncs(pkg)
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if l, ok := n.(*ir.Lambda); ok && l.Func != nil {
			funcs = append(funcs, l.Func)
		}
		return nil
	})
	for {
		changed := false
		for _, fn := range funcs {
			if !fn.IsAsync && ir.BlockHasAsyncCall(fn.Block) {
				fn.IsAsync = true
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

// offloadEntryPoints returns the functions whose bodies get a goroutine: an
// async one the target itself invokes -- an event handler, a timer body, an
// effect's settle, the kicker a reactive fetch lowers to.
//
// What disqualifies a candidate is being called from another candidate. Two
// nested goroutines would each post their own tail, and the outer one's would
// run the moment the inner goroutine started rather than after it finished --
// so the inner call stays an ordinary blocking call, made from a goroutine
// that is already off the UI thread.
func offloadEntryPoints(pkg *ir.Package) []*ir.Func {
	var cands []*ir.Func
	for _, fn := range offloadableFuncs(pkg) {
		// A bodyless declaration is the blocking function itself, not a place
		// one is called from.
		if fn.IsAsync && returnsNothing(fn) && len(fn.Block) > 0 {
			cands = append(cands, fn)
		}
	}
	cand := make(map[*ir.Func]bool, len(cands))
	for _, fn := range cands {
		cand[fn] = true
	}
	// A candidate reached from another candidate's body is not an entry point.
	// Repeat until nothing changes: dropping one can make a third reachable
	// only from it, which is then an entry point in its own right.
	for {
		called := map[*ir.Func]bool{}
		for _, fn := range cands {
			if cand[fn] {
				collectCallees(fn.Block, called)
			}
		}
		changed := false
		for _, fn := range cands {
			if cand[fn] && called[fn] {
				cand[fn] = false
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	var out []*ir.Func
	for _, fn := range cands {
		if cand[fn] {
			out = append(out, fn)
		}
	}
	return out
}

func lowerAllFuncs(pkg *ir.Package) []*ir.Func {
	out := append([]*ir.Func(nil), pkg.Funcs...)
	for _, comp := range pkg.Components {
		out = append(out, comp.Funcs...)
	}
	for _, w := range pkg.Windows {
		out = append(out, w.Funcs...)
	}
	return out
}

// offloadableFuncs is every body the target may enter from its own thread: the
// declared functions, plus the handlers hanging off nodes, vars and timers.
//
// The handlers are not optional. Which of the two a handler is by the time this
// pass runs depends on the platform and on where the node sits -- fyne with a
// top-level window lifts it into a Model method, and fyne with a window inside
// a component leaves it an ir.EventHandler that the generator names itself. A
// list of Funcs alone offloaded the first and silently blocked the second.
//
// A lambda is deliberately not here. One is a value, and what calls it is the
// code it was handed to: `xs.map(f)` with a blocking f wants the blocking f,
// not a goroutine per element.
func offloadableFuncs(pkg *ir.Package) []*ir.Func {
	out := lowerAllFuncs(pkg)
	seen := make(map[*ir.Func]bool, len(out))
	for _, fn := range out {
		seen[fn] = true
	}
	add := func(fn *ir.Func) {
		if fn != nil && !seen[fn] {
			seen[fn] = true
			out = append(out, fn)
		}
	}
	for _, t := range pkg.Timers {
		add(t.Handler)
	}
	collectHandlerFuncs(pkg.Body, add)
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			for _, h := range v.Handlers {
				add(h.Func)
			}
		}
		for _, t := range comp.Timers {
			add(t.Handler)
		}
		collectHandlerFuncs(comp.Body, add)
	}
	for _, w := range pkg.Windows {
		for _, v := range w.Vars {
			for _, h := range v.Handlers {
				add(h.Func)
			}
		}
		if w.ErrorHandler != nil {
			add(w.ErrorHandler.Func)
		}
		collectHandlerFuncs(w.Body, add)
	}
	return out
}

// collectHandlerFuncs walks a view body for the handlers hanging off its nodes.
// It mirrors blockCollector.viewIn, which answers the same question in block
// pointers rather than functions.
func collectHandlerFuncs(stmts []ir.Stmt, add func(*ir.Func)) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			for _, h := range n.Handlers {
				add(h.Func)
			}
			// A handler that crossed into an instance is a func-typed prop by
			// now, not an ir.EventHandler: passInstanceEvents turns the event
			// the call site subscribed to into a prop whose value is the
			// lambda. Which of the two shapes a click arrives in depends on
			// the platform and on whether the node is an instance, so a pass
			// reading only Handlers offloads some programs and silently
			// blocks others.
			for _, p := range n.Props {
				if lam, ok := p.Value.(*ir.Lambda); ok {
					add(lam.Func)
				}
			}
			collectHandlerFuncs(n.Children, add)
			for _, sc := range n.Slots {
				if sc != nil {
					collectHandlerFuncs(sc.Body, add)
				}
			}
		case *ir.If:
			collectHandlerFuncs(n.Body, add)
			collectHandlerFuncs(n.Else, add)
		case *ir.For:
			collectHandlerFuncs(n.Body, add)
			collectHandlerFuncs(n.Else, add)
		case *ir.SlotInst:
			collectHandlerFuncs(n.Children, add)
		case *ir.ErrorBoundary:
			if n.Handler != nil {
				add(n.Handler.Func)
			}
			collectHandlerFuncs(n.Children, add)
		case *ir.Window:
			// A window written inside a component is a statement in that
			// component's body rather than an entry in pkg.Windows, and it
			// owns funcs of its own -- which is where a flattened handler
			// lands on a platform that lowers the declarative tree. Reading
			// pkg.Windows alone found the handler in a top-level `window` and
			// missed the identical one written a level in.
			for _, f := range n.Funcs {
				add(f)
			}
			for _, v := range n.Vars {
				for _, h := range v.Handlers {
					add(h.Func)
				}
			}
			if n.ErrorHandler != nil {
				add(n.ErrorHandler.Func)
			}
			collectHandlerFuncs(n.Body, add)
		}
	}
}

func returnsNothing(fn *ir.Func) bool {
	return fn.Return == nil || fn.Return.Kind == ir.TypeVoid
}

func collectCallees(stmts []ir.Stmt, out map[*ir.Func]bool) {
	_ = ir.Walk(stmts, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && c.Func != nil {
			out[c.Func] = true
		}
		return nil
	})
}

type offloadState struct{ n int }

func (st *offloadState) fresh() string {
	name := "__async_off" + strconv.Itoa(st.n)
	st.n++
	return name
}

func (st *offloadState) transform(block []ir.Stmt) ([]ir.Stmt, error) {
	first, last, count := -1, -1, 0
	for i, s := range block {
		if stmtWaits(s) {
			if first < 0 {
				first = i
			}
			last = i
			count++
		}
	}
	if count == 0 {
		return block, nil
	}
	if last-first+1 != count {
		return nil, fmt.Errorf("%s: a statement between two blocking calls has to run somewhere, and neither side is right: the goroutine is off the drawing thread and the answer has not arrived yet. Move it before the first call or after the last", offloadPos(block[first+1]))
	}

	var bg, post []ir.Stmt
	for _, s := range block[first : last+1] {
		b, p, err := st.split(s)
		if err != nil {
			return nil, err
		}
		bg = append(bg, b...)
		post = append(post, p...)
	}
	post = append(post, block[last+1:]...)

	if len(post) > 0 {
		bg = append(bg, callStmt(AsyncPostIntrinsic, closure(post)))
	}
	out := append([]ir.Stmt(nil), block[:first]...)
	return append(out, callStmt(AsyncSpawnIntrinsic, closure(bg))), nil
}

// split says which half of the rewrite one statement's work belongs to. A
// blocking call's *result* is the only thing that has to cross: the call runs
// on the goroutine, and whatever the answer is bound to is written back.
func (st *offloadState) split(s ir.Stmt) (bg, post []ir.Stmt, err error) {
	switch n := s.(type) {
	case *ir.CallStmt:
		// Nothing is bound, so nothing crosses.
		return []ir.Stmt{n}, nil, nil
	case *ir.LocalVar:
		// A local declared here is read only by what follows, and what
		// follows is nested inside this closure. Lexical capture carries it.
		return []ir.Stmt{n}, nil, nil
	case *ir.Assign:
		name := st.fresh()
		t := n.Value.ExprType()
		sym := &ir.Var{Name: name, Type: t, Synthesized: true}
		tmp := &ir.LocalVar{Name: name, Type: t, Init: n.Value, Sym: sym}
		back := &ir.Assign{
			AST:    n.AST,
			Target: n.Target,
			Op:     n.Op,
			Value:  &ir.Ident{Name: name, Type: t, Sym: sym, Synthesized: true},
		}
		return []ir.Stmt{tmp}, []ir.Stmt{back}, nil
	case *ir.If, *ir.For:
		// The split is over one flat run of statements, and the branch a
		// blocking call sits in is not that run: whether it happens at all is
		// decided on the drawing thread, and hoisting the whole `if` onto the
		// goroutine would move its condition -- and anything else in it -- off
		// that thread with it. Saying so is the honest answer; saying "no
		// answer to hand back" named the wrong thing entirely.
		return nil, nil, fmt.Errorf("%s: this blocking call is inside an if or for, and only a call written directly in the body can be moved off the drawing thread -- lift it out, or put it in a function of its own marked as blocking", offloadPos(s))
	// An ir.Return is deliberately not here. An entry point returns nothing --
	// that is what makes it one -- so a `return` in its body carries no value,
	// and a case for one would be a branch no program can reach.
	default:
		return nil, nil, fmt.Errorf("%s: a blocking call here has no answer to hand back -- it is only supported in a call statement or on the right of an assignment", offloadPos(s))
	}
}

func closure(body []ir.Stmt) *ir.Lambda {
	return &ir.Lambda{Func: &ir.Func{Block: body}}
}

func callStmt(id string, arg ir.Expr) *ir.CallStmt {
	return &ir.CallStmt{Call: &ir.Call{
		Func: &ir.Func{Name: id, Intrinsic: id, Return: ir.TypVoid},
		Args: []ir.CallArg{{Value: arg}},
		Type: ir.TypVoid,
	}}
}

// stmtWaits reports whether s makes a call that does not complete now. It does
// not descend into a lambda: a blocking call inside one runs when that lambda
// is called, which is not here.
func stmtWaits(s ir.Stmt) bool {
	switch n := s.(type) {
	case *ir.CallStmt:
		return exprWaits(n.Call)
	case *ir.Assign:
		return exprWaits(n.Value)
	case *ir.LocalVar:
		return exprWaits(n.Init)
	case *ir.Return:
		return exprWaits(n.Value)
	case *ir.If:
		return exprWaits(n.Cond) || blockWaits(n.Body) || blockWaits(n.Else)
	case *ir.For:
		return exprWaits(n.Iter) || blockWaits(n.Body) || blockWaits(n.Else)
	}
	return false
}

func blockWaits(stmts []ir.Stmt) bool {
	return slices.ContainsFunc(stmts, stmtWaits)
}

// exprWaits recurses by hand rather than through ir.Walk because it must stop
// at a lambda: a blocking call written inside one runs when that lambda is
// called, which is not here.
func exprWaits(e ir.Expr) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *ir.Lambda, *ir.Closure:
		return false
	case *ir.Call:
		if x.Func != nil && x.Func.IsAsync {
			return true
		}
		if exprWaits(x.Receiver) || exprWaits(x.Callee) {
			return true
		}
		for _, a := range x.Args {
			if exprWaits(a.Value) {
				return true
			}
		}
	case *ir.Binary:
		return exprWaits(x.Left) || exprWaits(x.Right)
	case *ir.Unary:
		return exprWaits(x.Operand)
	case *ir.Ternary:
		return exprWaits(x.Cond) || exprWaits(x.Then) || exprWaits(x.Else)
	case *ir.Conversion:
		return exprWaits(x.Operand)
	case *ir.Select:
		return exprWaits(x.Operand)
	case *ir.Index:
		return exprWaits(x.Operand) || exprWaits(x.Idx)
	case *ir.Spread:
		return exprWaits(x.Operand)
	case *ir.ListLit:
		if slices.ContainsFunc(x.Elems, exprWaits) {
			return true
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			if exprWaits(f.Value) {
				return true
			}
		}
	case *ir.MapLitIR:
		for _, ent := range x.Entries {
			if exprWaits(ent.Key) || exprWaits(ent.Value) {
				return true
			}
		}
	}
	return false
}

func offloadPos(s ir.Stmt) ast.Pos { return ir.StmtPos(s) }
