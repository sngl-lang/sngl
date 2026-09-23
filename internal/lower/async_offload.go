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
// the driver and gtk4 onto a GLib idle tick. Those two are the whole list --
// Bubble Tea owns its loop and lets nothing outside Update touch the model,
// which is the same reason it cannot take an effect timer.
//
// Both are declared in sngl:async, which is what separates them from
// ir.NodeOps: those name nobody's contract, and these name a platform
// package's. fyne's timer hands its tick to a host scheduler and has to reach
// the drawing thread again before it touches a widget.
//
// So this pass is no longer the only writer of either, and passAsyncCapable
// runs ahead of it to refuse a program that names one its target cannot emit.
// The calls this pass goes on to synthesize are past that check by then, which
// is deliberate: it may write a post for a target whose own program never
// could.
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
	enabled: func(c Features) bool { return !c.AsyncCalls },
	apply:   applyAsyncOffload,
}

func applyAsyncOffload(pkg *ir.Package, caps Features, opts Options) error {
	if pkg == nil {
		return nil
	}
	recolourAsync(pkg)
	st := &offloadState{pts: pkg.PointsTo}
	if !caps.AsyncPost {
		return refuseAsyncOffload(pkg, opts, st)
	}
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
func refuseAsyncOffload(pkg *ir.Package, opts Options, st *offloadState) error {
	for _, block := range allBlocks(pkg) {
		for _, s := range *block {
			if !st.stmtWaits(s) {
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
			if !fn.IsAsync && ir.BlockHasFuncvarAsyncCall(fn.Block, pkg.PointsTo) {
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

// offloadableFuncs is every body the target may enter from its own thread: the
// declared functions, plus the handlers hanging off nodes, vars and timers.
//
// The handlers are not optional. Which of the two a handler is by the time this
// pass runs depends on the platform and on where the node sits -- fyne with a
// top-level window lifts it into a Model method, and fyne with a window inside
// a component leaves it an ir.EventHandler that the generator names itself. A
// list of Funcs alone offloaded the first and silently blocked the second.
//
// A lambda is deliberately not here, with one exception. One is a value, and
// what calls it is the code it was handed to: `xs.map(f)` with a blocking f
// wants the blocking f, not a goroutine per element. A callee that *says* it
// schedules is that exception -- nativeCallbackFuncs below.
func offloadableFuncs(pkg *ir.Package) []*ir.Func {
	var out []*ir.Func
	seen := map[*ir.Func]bool{}
	add := func(fn *ir.Func) {
		if fn != nil && !seen[fn] {
			seen[fn] = true
			out = append(out, fn)
		}
	}
	// Declared funcs first and handlers after, across all owners, rather than
	// both per owner: passAsyncOffload numbers its goroutine helpers
	// __async_offN off this order, and interleaving them renamed helpers in a
	// program whose second owner also offloads.
	owners := ir.Owners(pkg)
	for _, o := range owners {
		for _, fn := range o.Funcs {
			add(fn)
		}
	}
	for _, o := range owners {
		for _, v := range o.Vars {
			for _, h := range v.Handlers {
				add(h.Func)
			}
		}
		for _, h := range o.Handlers {
			add(h.Func)
		}
		collectHandlerFuncs(o.Stmts(), add)
	}
	// Last, for the numbering reason above: a callback is reached through an
	// expression rather than through an owner, so it has no place in that walk
	// and appending keeps every other helper's number where it was.
	nativeCallbackFuncs(pkg, add)
	return out
}

// nativeCallbackFuncs is every lambda handed to a call that says it schedules.
//
// It is the exception to the rule above, and the `schedules` flag is what makes
// it one: what calls an ordinary lambda is code this pass can read, and a
// native has no body here to read at all. A host scheduler calls its callback
// from the loop it owns, which is the thread the target draws on --
// `time.AfterFunc` and `gtk4rt.Every` behind `time.timer` are that -- so a
// blocking call in a tick is exactly the work this pass exists to move off it.
//
// The flag is asked for rather than inferred from the call's shape, which is
// the rule the whole of #[foreign] follows: a foreign declaration describes an
// identifier and does not implement it, so nothing here can find out when the
// host runs an argument. Inferring it from "the callee is a native" would also
// be wrong the other way -- a native that runs its callback inline would get a
// goroutine nobody asked for.
//
// A scheduler whose declaration forgets the flag gets the old bug rather than a
// new one: the callback is not an entry point, so a blocking call in it stays
// on the drawing thread exactly as it did before any of this existed. That is
// silent, which is why testdata/timer_tick_async_offload.txtar denies it.
func nativeCallbackFuncs(pkg *ir.Package, add func(*ir.Func)) {
	_ = ir.Walk(pkg, func(n ir.Node) error {
		c, ok := n.(*ir.Call)
		if !ok || c.Func == nil || !schedulesItsCallback(c.Func) {
			return nil
		}
		for _, a := range c.Args {
			if fn := callbackFunc(a.Value); fn != nil {
				add(fn)
			}
		}
		return nil
	})
}

// schedulesItsCallback answers the question the flag exists for, and
// AsyncPostIntrinsic is the second thing it is true of: a post runs its closure
// from the loop the platform owns, which is the same thread a native scheduler
// calls back on.
//
// Named here rather than flagged, because the id has no one declaration to flag
// -- this pass synthesizes calls to it, and a platform package may declare its
// own (fyne.sngl does, to post a self-rearming timer's tick). Only the calls
// standing before this pass runs are read, so the posts it goes on to generate
// are not candidates.
func schedulesItsCallback(fn *ir.Func) bool {
	return fn.NativeSchedules || fn.Intrinsic == AsyncPostIntrinsic
}

// callbackFunc is the body behind a callback argument, through whatever the
// checker wrapped it in.
//
// Matching a bare *ir.Lambda is what this did, and the whole failure mode of
// missing one is silent: the tick stops being an entry point and a blocking
// call in it goes back onto the drawing thread with the generated code still
// compiling. Nothing wraps one today -- Go sets neither NoLambda nor a
// conversion at an argument position -- so this is the guard rather than a fix.
//
// A callback passed *by name* is the case this does not cover, and the flag
// then protects nothing: an *ir.Ident bound to a func var is none of the three.
// gtk4's `every(int(d), tick)` is covered only because inlining has made `tick`
// a literal lambda by the time this runs, and fyne's `after(d, rearm)` is not
// covered at all -- its tick reaches the pass through the AsyncPostIntrinsic
// arm instead. Resolving an ident through pkg.PointsTo is what closing it
// would take.
func callbackFunc(e ir.Expr) *ir.Func {
	for {
		switch x := e.(type) {
		case *ir.Lambda:
			return x.Func
		case *ir.Closure:
			return x.Func
		case *ir.Conversion:
			e = x.Operand
		default:
			return nil
		}
	}
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
			for _, name := range ir.SlotNames(n.Slots) {
				collectHandlerFuncs(n.Slots[name].Body, add)
			}
		case *ir.ErrorBoundary:
			if n.Handler != nil {
				add(n.Handler.Func)
			}
			collectHandlerFuncs(n.Children, add)
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

// offloadState carries the points-to information alongside the counter,
// because deciding whether a statement blocks is not a question about the
// statement alone: a call through a funcvar names no ir.Func, and the only
// record of what it may reach is the analysis the checker already ran.
type offloadState struct {
	n   int
	pts *ir.PointsToInfo
}

func (st *offloadState) fresh() string {
	name := "__async_off" + strconv.Itoa(st.n)
	st.n++
	return name
}

func (st *offloadState) transform(block []ir.Stmt) ([]ir.Stmt, error) {
	first, last, count := -1, -1, 0
	for i, s := range block {
		if st.stmtWaits(s) {
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
func (st *offloadState) stmtWaits(s ir.Stmt) bool {
	switch n := s.(type) {
	case *ir.CallStmt:
		return st.exprWaits(n.Call)
	case *ir.Assign:
		return st.exprWaits(n.Value)
	case *ir.LocalVar:
		return st.exprWaits(n.Init)
	case *ir.Return:
		return st.exprWaits(n.Value)
	case *ir.If:
		return st.exprWaits(n.Cond) || st.blockWaits(n.Body) || st.blockWaits(n.Else)
	case *ir.For:
		return st.exprWaits(n.Iter) || st.blockWaits(n.Body) || st.blockWaits(n.Else)
	}
	return false
}

func (st *offloadState) blockWaits(stmts []ir.Stmt) bool {
	return slices.ContainsFunc(stmts, st.stmtWaits)
}

// exprWaits recurses by hand rather than through ir.Walk because it must stop
// at a lambda: a blocking call written inside one runs when that lambda is
// called, which is not here.
func (st *offloadState) exprWaits(e ir.Expr) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *ir.Lambda, *ir.Closure:
		return false
	case *ir.Call:
		if x.Func != nil && x.Func.IsAsync {
			return true
		}
		// A call through a funcvar names no ir.Func at all -- the callee is
		// the variable -- so the flag is not there to read, and the answer is
		// the slot colour the checker's points-to analysis left behind. Asking
		// only for the flag left `greeting = handler()` on the drawing thread
		// with no goroutine and no diagnostic, which is the exact failure the
		// mark exists to prevent.
		if x.Func == nil && x.Callee != nil && st.pts != nil {
			if k, ok := ir.CalleeSlotKey(x.Callee); ok {
				if colour, present := st.pts.SlotColor[k]; present {
					if colour == ir.ColorAsync {
						return true
					}
				} else {
					for _, fn := range st.pts.Candidates(k) {
						if fn.IsAsync {
							return true
						}
					}
				}
			}
		}
		if st.exprWaits(x.Receiver) || st.exprWaits(x.Callee) {
			return true
		}
		for _, a := range x.Args {
			if st.exprWaits(a.Value) {
				return true
			}
		}
	case *ir.Binary:
		return st.exprWaits(x.Left) || st.exprWaits(x.Right)
	case *ir.Unary:
		return st.exprWaits(x.Operand)
	case *ir.Ternary:
		return st.exprWaits(x.Cond) || st.exprWaits(x.Then) || st.exprWaits(x.Else)
	case *ir.Conversion:
		return st.exprWaits(x.Operand)
	case *ir.Select:
		return st.exprWaits(x.Operand)
	case *ir.Index:
		return st.exprWaits(x.Operand) || st.exprWaits(x.Idx)
	case *ir.Spread:
		return st.exprWaits(x.Operand)
	case *ir.ListLit:
		if slices.ContainsFunc(x.Elems, st.exprWaits) {
			return true
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			if st.exprWaits(f.Value) {
				return true
			}
		}
	case *ir.MapLitIR:
		for _, ent := range x.Entries {
			if st.exprWaits(ent.Key) || st.exprWaits(ent.Value) {
				return true
			}
		}
	}
	return false
}

func offloadPos(s ir.Stmt) ast.Pos { return ir.StmtPos(s) }
