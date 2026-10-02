package optimize

import (
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Documents yields every window pkg renders, one at a time, for a target that
// writes each window out as markup. It runs after lowering, on the package the
// second Optimize left, and is where a loop is unrolled at all. Each is a clone
// of the lowered window with the variables of the loops around it bound for
// its iteration and every constant loop in its view unrolled.
//
// One at a time because a site is a loop over its pages, and unrolling that
// loop in the IR held every page's expanded tree live at once: 3.9 GB of heap
// for the docs site, most of it pages already written. A document is cloned
// from the window it came from, so nothing yielded is reachable from pkg and
// each is garbage once its caller has written it.
//
// A window holding a nav.stack is one document per page instead, the page
// standing where the stack was (windowDocuments).
func Documents(pkg *ir.Package, cfg *Config) iter.Seq2[*codegen.Document, error] {
	return func(yield func(*codegen.Document, error) bool) {
		if cfg.Cache == nil {
			cfg.Cache = NewEvalCache()
		}
		run := &optimizerRun{
			cfg:        cfg,
			done:       map[*ir.Package]bool{},
			root:       pkg,
			writes:     newWritesAnalysis(cfg.Platform, cfg.Language),
			interpEnvs: map[*ir.Package]*interp.Env{},
		}
		ctx := run.newCtx(pkg)
		ctx.unroll, ctx.spliced = true, true
		// Built here so every child shares the one map rather than each building
		// its own.
		ctx.getNativeImports()
		for _, c := range slices.Concat(pkg.Consts, pkg.BuildConsts) {
			if c.Init != nil {
				if val, ok := evalExpr(c.Init, ctx); ok {
					ctx.values[c] = val
				}
			}
		}

		if hasPureNativeFuncs(pkg, cfg) {
			if err := evalDocumentNatives(pkg, cfg, ctx); err != nil {
				yield(nil, err)
				return
			}
		}

		yielded := false
		emit := func(w *ir.Window, wctx *evalCtx, _ []*ir.For) bool {
			yielded = true
			ok, err := windowDocuments(w, wctx, cfg.Language == "none", func(doc *codegen.Document, dctx *evalCtx) bool {
				return yieldDocument(yield, doc, dctx)
			})
			if err != nil {
				yield(nil, err)
				return false
			}
			return ok
		}
		if err := eachWindowRoot(pkg, ctx, emit); err != nil {
			yield(nil, err)
			return
		}
		if yielded || pkg.RootComponent == "" {
			return
		}
		// A harness renders its root component in place of a window.
		for _, c := range pkg.Components {
			if c.Name != pkg.RootComponent {
				continue
			}
			stack, err := documentStack(c.Body, ctx)
			if err != nil {
				yield(nil, err)
				return
			}
			if stack == nil {
				dctx := ctx.child()
				body := foldStmts(cloneStmts(c.Body), dctx)
				yieldDocument(yield, &codegen.Document{Body: body}, dctx)
				return
			}
			for _, pc := range documentCopies(stack, ctx) {
				dctx := ctx.child()
				body := cloneWithOnly(stack, pc, func() []ir.Stmt { return cloneStmts(c.Body) })
				page := pageDocument(&body, pc, dctx, true)
				body = foldStmts(body, dctx)
				if !yieldDocument(yield, &codegen.Document{Body: body, Page: page.node}, dctx) {
					return
				}
			}
			return
		}
	}
}

// windowVisit is handed each window with the context its enclosing loops bound
// and those loops, outermost first. Returning false stops the walk.
type windowVisit func(w *ir.Window, ctx *evalCtx, loops []*ir.For) bool

// eachWindowRoot visits the windows ir.AllWindows reports, in its order: the
// package's own, then those the package body renders, then a component's.
func eachWindowRoot(pkg *ir.Package, ctx *evalCtx, visit windowVisit) error {
	seen := map[*ir.Window]bool{}
	w := &windowWalk{visit: visit, seen: seen}
	for _, win := range pkg.Windows {
		if !w.window(win, ctx) {
			return w.err
		}
	}
	w.stmts(pkg.Body, ctx)
	for _, c := range pkg.Components {
		if w.stopped || c == nil {
			break
		}
		w.stmts(c.Body, ctx)
	}
	return w.err
}

type windowWalk struct {
	visit   windowVisit
	seen    map[*ir.Window]bool
	loops   []*ir.For
	inLoop  []*ir.Window
	stopped bool
	err     error
}

func (w *windowWalk) window(win *ir.Window, ctx *evalCtx) bool {
	// A window reached twice through two roots is one window, and one in a
	// loop is one per iteration: seen is filled as the outermost loop ends.
	if win == nil || w.seen[win] {
		return true
	}
	if len(w.loops) == 0 {
		w.seen[win] = true
	} else {
		w.inLoop = append(w.inLoop, win)
	}
	if !w.visit(win, ctx, w.loops) {
		w.stopped = true
	}
	return !w.stopped
}

func (w *windowWalk) stmts(stmts []ir.Stmt, ctx *evalCtx) {
	for _, s := range stmts {
		if w.stopped {
			return
		}
		w.stmt(s, ctx)
	}
}

func (w *windowWalk) stmt(s ir.Stmt, ctx *evalCtx) {
	switch n := s.(type) {
	case *ir.NodeInst:
		if ir.IsWindowNode(n) {
			w.window(n, ctx)
			return
		}
		w.stmts(n.Children, ctx)
	case *ir.For:
		if !holdsWindow(n) {
			return
		}
		w.loop(n, ctx)
	case *ir.If:
		if lit, ok := foldExpr(cloneExpr(n.Cond), ctx).(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeBool {
			if lit.Value == "true" {
				w.stmts(n.Body, ctx)
			} else {
				w.stmts(n.Else, ctx)
			}
			return
		}
		w.stmts(n.Body, ctx)
		w.stmts(n.Else, ctx)
	case *ir.ErrorBoundary:
		w.stmts(n.Children, ctx)
	case *ir.SlotInst:
		w.stmts(n.Children, ctx)
		for _, name := range ir.SlotNames(n.Slots) {
			w.stmts(n.Slots[name].Body, ctx)
		}
	}
}

// loop visits a window loop's body once per element, with the loop's
// variables bound. A loop whose iterable does not fold is walked once unbound,
// which leaves the windows in it naming their variable -- what a target that
// cannot write a dynamic page already reports.
func (w *windowWalk) loop(fs *ir.For, ctx *evalCtx) {
	items, ok := loopItems(fs, ctx)
	if !ok {
		if ctx.err != nil {
			w.err, w.stopped = ctx.err, true
			return
		}
		w.stmts(fs.Body, ctx)
		return
	}
	if len(items) == 0 {
		w.stmts(fs.Else, ctx)
		return
	}
	w.loops = append(w.loops, fs)
	defer func() {
		w.loops = w.loops[:len(w.loops)-1]
		if len(w.loops) == 0 {
			for _, win := range w.inLoop {
				w.seen[win] = true
			}
			w.inLoop = w.inLoop[:0]
		}
	}()
	keyVar, valueVar := loopVars(fs)
	for i, item := range items {
		if w.stopped {
			return
		}
		w.stmts(fs.Body, bindLoopVars(fs, ctx, keyVar, valueVar, i, item))
	}
}

func holdsWindow(fs *ir.For) bool {
	found := false
	_ = ir.WalkStmts(append(append([]ir.Stmt{}, fs.Body...), fs.Else...), func(s ir.Stmt) error {
		if n, ok := s.(*ir.NodeInst); ok && ir.IsWindowNode(n) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// loopItems evaluates a loop's iterable to the elements an unroll writes out.
// A count past maxStaticUnroll is an error on ctx rather than a list.
func loopItems(fs *ir.For, ctx *evalCtx) ([]any, bool) {
	// A condition and a headless loop walk nothing, so there is no iterable
	// to evaluate: both are refused in a view body, and a nil Iter reaching
	// evalExpr would read as an unevaluable one rather than as another kind of
	// loop.
	if fs.Iter == nil {
		return nil, false
	}
	if t := fs.Iter.ExprType(); t != nil && t.Kind == ir.TypeBool {
		return nil, false
	}
	head := fs.Iter
	// A list walked as a sequence: the first Optimize folds a short sngl:seq
	// into its literal, which the checker's conversion then wraps. Unwrapped
	// here rather than in evalConversion, where every other fold of one would
	// replace the target's own sequence with a list.
	if conv, ok := head.(*ir.Conversion); ok && conv.Type != nil && conv.Type.Kind == ir.TypeIter {
		head = conv.Operand
	}
	val, ok := evalExpr(head, ctx)
	if !ok {
		return nil, false
	}
	items, ok := val.([]any)
	if !ok {
		// A nil is an absent list, not an unevaluable one: a pure native call
		// returning an empty (or nil) slice iterates zero times.
		if val != nil {
			return nil, false
		}
		items = nil
	}
	if len(items) > maxStaticUnroll {
		if ctx.err == nil {
			pos := ""
			if fs.AST != nil {
				pos = fs.AST.Pos.String() + ": "
			}
			ctx.err = fmt.Errorf("%sthis loop repeats %d times, and this target writes every iteration into its output (limit %d): give it a smaller count, or build for a target that can run the loop",
				pos, len(items), maxStaticUnroll)
		}
		return nil, false
	}
	return items, true
}

// bindLoopVars is a child of ctx with one iteration's variables bound, typed
// the way the checker types them: in `for var k, v = xs` the key is the index
// and the value the element, and in `for var x = xs` the one variable is the
// element. The form is the syntactic one -- findLoopVar is nil for a variable
// nothing reads, so keying off it would bind an unused `x`'s index to `i`.
func bindLoopVars(fs *ir.For, ctx *evalCtx, keyVar, valueVar *ir.LoopVar, i int, item any) *evalCtx {
	child := ctx.child()
	if fs.Value != "" {
		if keyVar != nil {
			child.values[keyVar] = i
		}
		if valueVar != nil {
			child.values[valueVar] = item
		}
	} else if keyVar != nil {
		child.values[keyVar] = item
	}
	return child
}

func yieldDocument(yield func(*codegen.Document, error) bool, doc *codegen.Document, ctx *evalCtx) bool {
	if ctx.err != nil {
		yield(nil, ctx.err)
		return false
	}
	for _, fa := range ctx.fileAssets {
		doc.FileAssets = append(doc.FileAssets, codegen.FileAsset{SrcPath: fa.SrcPath, OutPath: fa.OutPath, Data: fa.Data})
	}
	return yield(doc, nil)
}

// evalDocumentNatives is evalNativeRounds for the calls a loop variable makes
// constant, which the first Optimize could not see: `lookup.PackageIndex(p.path)`
// in a page per package. Each round folds every document that holds a pure
// native call and discards it, and hands the calls it could not answer to one
// generated program -- answered one at a time instead, a site of a hundred
// such pages built a hundred programs.
func evalDocumentNatives(pkg *ir.Package, cfg *Config, ctx *evalCtx) error {
	var pending []*nativeRequest
	for range maxEvalRounds {
		ne := &nativeEval{}
		probe := ctx.child()
		probe.native = ne
		err := eachWindowRoot(pkg, probe, func(w *ir.Window, wctx *evalCtx, _ []*ir.For) bool {
			if !holdsNativeCall(w, wctx) {
				return true
			}
			_, err := windowDocuments(w, wctx, cfg.Language == "none", func(*codegen.Document, *evalCtx) bool { return true })
			if err != nil && probe.err == nil {
				probe.err = err
			}
			return true
		})
		if err != nil {
			return err
		}
		if len(ne.order) == 0 {
			return nil
		}
		pending = ne.order
		if errs := runNativeRequests(cfg.Cache, cfg.Dir, ir.IndexNativeDecls(pkg), pending); len(errs) > 0 {
			cfg.nativeErr, ctx.nativeErr = errs, errs
			return nil
		}
	}
	names := make([]string, 0, len(pending))
	for _, r := range pending {
		names = append(names, r.nativeType)
	}
	return fmt.Errorf("compile-time evaluation did not settle after %d rounds; still pending: %s",
		maxEvalRounds, strings.Join(names, ", "))
}

func holdsNativeCall(w *ir.Window, ctx *evalCtx) bool {
	found := false
	scanStmts([]ir.Stmt{w}, func(e ir.Expr) {
		if call, ok := e.(*ir.Call); ok && !found && isEvaluableNativeCall(call, ctx) {
			found = true
		}
	})
	return found
}

// A window holding a nav.stack is one document per page on a target whose view
// is markup: the whole window, with the stack standing for that page alone.
// A page is the node carrying a page's value (ir.NodeInst.Record), which is
// what the target's own primitive for nav.page is handed as it stands in for
// the node the program wrote; its stack is the node holding it.
//
// A page may be written under a `for` over a constant and an `if` the build
// decides, so a document is a *copy* of a page: the page node and, for each
// loop around it, the iteration it is. The window is cloned holding that one
// page, the loops' variables bound for the iteration, so a site of a thousand
// pages holds one page's tree at a time.

// windowDocuments hands each document w is written as to yield: w itself when
// it holds no stack, and one per page copy when it holds one. False when
// yield stopped the walk.
func windowDocuments(w *ir.Window, wctx *evalCtx, static bool, yield func(*codegen.Document, *evalCtx) bool) (bool, error) {
	stack, err := documentStack(w.Children, wctx)
	if err != nil {
		return false, err
	}
	if stack == nil {
		doc := cloneStmt(w).(*ir.NodeInst)
		dctx := wctx.child()
		dctx.fileAssets = nil
		foldWindow(doc, dctx)
		return yield(&codegen.Document{Window: doc, Body: doc.Children}, dctx), nil
	}
	copies := documentCopies(stack, wctx)
	if wctx.err != nil {
		return false, wctx.err
	}
	for _, pc := range copies {
		dctx := wctx.child()
		dctx.fileAssets = nil
		doc := cloneWithOnly(stack, pc, func() []ir.Stmt { return []ir.Stmt{cloneStmt(w)} })[0].(*ir.NodeInst)
		page := pageDocument(&doc.Children, pc, dctx, static)
		if !page.static {
			doc.Params = page.param
		}
		foldWindow(doc, dctx)
		if dctx.err != nil {
			return false, dctx.err
		}
		if !yield(&codegen.Document{Window: doc, Body: doc.Children, Page: page.node}, dctx) {
			return false, nil
		}
	}
	return true, nil
}

// documentStack is the one stack stmts render, nil for none, refusing what
// cannot be decided per document: a second stack, which would make the
// documents a product of the two, and a stack under an `if` the build cannot
// fold, whose pages a document can neither hold nor leave out.
func documentStack(stmts []ir.Stmt, ctx *evalCtx) (*ir.NodeInst, error) {
	var stack *ir.NodeInst
	var walk func(stmts []ir.Stmt) error
	walk = func(stmts []ir.Stmt) error {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				if ir.IsWindowNode(n) {
					continue
				}
				if holdsPages(n.Children) {
					if stack != nil {
						return fmt.Errorf("%s: a window's documents are its pages, and this is a second nav.stack in it; the first is at %s", ir.NodePos(n), ir.NodePos(stack))
					}
					stack = n
					continue
				}
				if err := walk(n.Children); err != nil {
					return err
				}
				for _, name := range ir.SlotNames(n.Slots) {
					if err := walk(n.Slots[name].Body); err != nil {
						return err
					}
				}
			case *ir.If:
				before := stack
				if err := walk(n.Body); err != nil {
					return err
				}
				if err := walk(n.Else); err != nil {
					return err
				}
				if stack != before {
					if _, ok := foldExpr(cloneExpr(n.Cond), ctx).(*ir.Literal); !ok {
						return fmt.Errorf("%s: a nav.stack under an `if` that reads state cannot be decided per document; write the `if` inside a page", ir.NodePos(stack))
					}
				}
			case *ir.For:
				if err := walk(n.Body); err != nil {
					return err
				}
			case *ir.ErrorBoundary:
				if err := walk(n.Children); err != nil {
					return err
				}
			case *ir.ContextProvider:
				if err := walk(n.Children); err != nil {
					return err
				}
			case *ir.SlotInst:
				if err := walk(n.Children); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(stmts); err != nil {
		return nil, err
	}
	return stack, nil
}

// holdsPages reports whether stmts hold a page, directly or under the `for`
// and `if` a stack's pages may be written in.
func holdsPages(stmts []ir.Stmt) bool {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n.Record != nil {
				return true
			}
		case *ir.For:
			if holdsPages(n.Body) {
				return true
			}
		case *ir.If:
			if holdsPages(n.Body) || holdsPages(n.Else) {
				return true
			}
		}
	}
	return false
}

// pageStep is one level of the way from a stack to a page copy: the index of
// the statement in the list, and for an `if` the branch taken (0 its body, 1
// its else) or for a `for` the iteration.
type pageStep struct {
	idx, sub int
}

// pageCopy is one page a document is written for, by the way to it from its
// stack, so that the same way can be followed in a clone.
type pageCopy struct {
	path []pageStep
}

// documentCopies is every page copy the stack holds, in order: an `if` the
// build decides contributes the branch it takes, and a `for` one copy per
// element.
func documentCopies(stack *ir.NodeInst, ctx *evalCtx) []pageCopy {
	var out []pageCopy
	var walk func(stmts []ir.Stmt, ctx *evalCtx, path []pageStep)
	walk = func(stmts []ir.Stmt, ctx *evalCtx, path []pageStep) {
		for i, s := range stmts {
			at := func(sub int) []pageStep {
				return append(slices.Clip(path), pageStep{i, sub})
			}
			switch n := s.(type) {
			case *ir.NodeInst:
				if n.Record != nil {
					out = append(out, pageCopy{path: at(0)})
				}
			case *ir.If:
				lit, ok := foldExpr(cloneExpr(n.Cond), ctx).(*ir.Literal)
				if !ok || lit.Type == nil || lit.Type.Kind != ir.TypeBool {
					if ctx.err == nil {
						ctx.err = fmt.Errorf("%s: a page under an `if` the build cannot decide is in no document and every one at once; decide it from constants", ir.StmtPos(n))
					}
					continue
				}
				if lit.Value == "true" {
					walk(n.Body, ctx, at(0))
				} else {
					walk(n.Else, ctx, at(1))
				}
			case *ir.For:
				items, ok := loopItems(n, ctx)
				if !ok {
					if ctx.err == nil {
						ctx.err = fmt.Errorf("%s: pages under a `for` whose iterable the build cannot evaluate have no documents; iterate a constant", ir.StmtPos(n))
					}
					continue
				}
				keyVar, valueVar := loopVars(n)
				for k, item := range items {
					walk(n.Body, bindLoopVars(n, ctx, keyVar, valueVar, k, item), at(k))
				}
			}
		}
	}
	walk(stack.Children, ctx, nil)
	return out
}

// cloneWithOnly clones what clone copies while stack holds only the
// statement pc's way starts at, so a document's clone holds its one page and
// not every page beside it. The stack is restored before it returns.
func cloneWithOnly(stack *ir.NodeInst, pc pageCopy, clone func() []ir.Stmt) []ir.Stmt {
	kids := stack.Children
	stack.Children = []ir.Stmt{kids[pc.path[0].idx]}
	defer func() { stack.Children = kids }()
	return clone()
}

// documentPage is the page a document is written for.
type documentPage struct {
	node *ir.NodeInst
	// param is what the page's content is handed, the page's params cell
	// (NodeInst.Params): bound to the page's own params on a static site, and
	// on a target that serves the page per request left standing, for the
	// route to bind.
	param  *ir.Param
	static bool
}

// pageDocument rewrites stmts, a clone made by cloneWithOnly, so that the
// stack holding the pages stands for the copy pc names alone: the page's
// content, in the stack's place, with the variables of the loops around it
// bound in ctx. `pages.current` in the document is that copy's record.
func pageDocument(stmts *[]ir.Stmt, pc pageCopy, ctx *evalCtx, static bool) documentPage {
	var out documentPage
	var walk func(stmts []ir.Stmt) []ir.Stmt
	walk = func(stmts []ir.Stmt) []ir.Stmt {
		var res []ir.Stmt
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				if !ir.IsWindowNode(n) && holdsPages(n.Children) {
					res = append(res, out.take(n, pc, ctx, static)...)
					continue
				}
				n.Children = walk(n.Children)
				for _, name := range ir.SlotNames(n.Slots) {
					n.Slots[name].Body = walk(n.Slots[name].Body)
				}
			case *ir.If:
				n.Body, n.Else = walk(n.Body), walk(n.Else)
			case *ir.ErrorBoundary:
				n.Children = walk(n.Children)
			case *ir.ContextProvider:
				n.Children = walk(n.Children)
			case *ir.SlotInst:
				n.Children = walk(n.Children)
			}
			res = append(res, s)
		}
		return res
	}
	*stmts = walk(*stmts)
	return out
}

// take follows pc's way through the stack's one statement to the page,
// binding each loop's variables for the iteration on the way, and returns the
// page's content.
func (out *documentPage) take(stack *ir.NodeInst, pc pageCopy, ctx *evalCtx, static bool) []ir.Stmt {
	stmts := stack.Children
	for depth, step := range pc.path {
		idx := step.idx
		if depth == 0 {
			idx = 0 // the clone holds only this statement
		}
		if idx >= len(stmts) {
			return nil
		}
		switch n := stmts[idx].(type) {
		case *ir.If:
			stmts = n.Body
			if step.sub == 1 {
				stmts = n.Else
			}
		case *ir.For:
			items, ok := loopItems(n, ctx)
			if !ok || step.sub >= len(items) {
				return nil
			}
			keyVar, valueVar := loopVars(n)
			bound := bindLoopVars(n, ctx, keyVar, valueVar, step.sub, items[step.sub])
			maps.Copy(ctx.values, bound.values)
			stmts = n.Body
		case *ir.NodeInst:
			out.node, out.static = n, static
			// The record is the copy's: under a `for` it reads the loop's
			// variables, which ctx now binds.
			n.Record = foldExpr(n.Record, ctx)
			if v, ok := evalExpr(n.Record, ctx); ok {
				ctx.navCurrent = v
			}
			if out.param = n.Params; out.param != nil && static {
				if v, ok := evalExpr(n.Prop("params"), ctx); ok {
					ctx.values[out.param] = v
				}
			}
			return n.Children
		}
	}
	return nil
}
