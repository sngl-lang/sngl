package optimize

import (
	"fmt"
	"iter"
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
// A `#id` on a window in a loop names the list of those windows, and a window
// anywhere may read it -- an index page listing the pages after it. So every
// such list is bound before the first document is yielded, from the windows'
// props alone.
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

		lists := map[*ir.Var][]any{}
		bindLists := func(w *ir.Window, wctx *evalCtx, loops []*ir.For) bool {
			if w.ID == "" || len(loops) == 0 {
				return true
			}
			for _, fs := range loops {
				for _, v := range fs.HoistedWindowIDs {
					if v.Name == w.ID {
						props := cloneStmt(w).(*ir.NodeInst)
						for i := range props.Props {
							props.Props[i].Value = foldExpr(props.Props[i].Value, wctx)
						}
						lists[v] = append(lists[v], windowStructValue(props))
					}
				}
			}
			return true
		}
		if err := eachWindowRoot(pkg, ctx, bindLists); err != nil {
			yield(nil, err)
			return
		}
		for v, vals := range lists {
			ctx.values[v] = vals
		}

		yielded := false
		emit := func(w *ir.Window, wctx *evalCtx, _ []*ir.For) bool {
			yielded = true
			n, err := documentPages(w.Children, wctx)
			if err == nil && n > 0 && w.Prop(ir.WindowHref) != nil {
				err = fmt.Errorf("%s: a window holding a nav.stack is served at its pages' hrefs, and this one has an href of its own", ir.NodePos(w))
			}
			if err != nil {
				yield(nil, err)
				return false
			}
			if n == 0 {
				doc := cloneStmt(w).(*ir.NodeInst)
				dctx := wctx.child()
				dctx.fileAssets = nil
				foldWindow(doc, dctx)
				return yieldDocument(yield, &codegen.Document{Window: doc, Body: doc.Children}, dctx)
			}
			for i := range n {
				doc := cloneStmt(w).(*ir.NodeInst)
				dctx := wctx.child()
				dctx.fileAssets = nil
				page := pageDocument(&doc.Children, i, dctx, cfg.Language == "none")
				if !page.static {
					doc.Params = page.param
				}
				foldWindow(doc, dctx)
				if !yieldDocument(yield, &codegen.Document{Window: doc, Body: doc.Children, Page: page.node}, dctx) {
					return false
				}
			}
			return true
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
			n, err := documentPages(c.Body, ctx)
			if err != nil {
				yield(nil, err)
				return
			}
			if n == 0 {
				dctx := ctx.child()
				body := foldStmts(cloneStmts(c.Body), dctx)
				yieldDocument(yield, &codegen.Document{Body: body}, dctx)
				return
			}
			for i := range n {
				dctx := ctx.child()
				body := cloneStmts(c.Body)
				page := pageDocument(&body, i, dctx, true)
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
			doc := cloneStmt(w).(*ir.NodeInst)
			dctx := wctx.child()
			foldWindow(doc, dctx)
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
// documentPages counts the pages of the window's one stack and refuses what
// cannot be decided per document: a second stack, which would make the
// documents a product of the two, and a stack under an `if` the build cannot
// fold, whose pages a document can neither hold nor leave out.
func documentPages(stmts []ir.Stmt, ctx *evalCtx) (int, error) {
	var stack *ir.NodeInst
	var walk func(stmts []ir.Stmt) error
	walk = func(stmts []ir.Stmt) error {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				if ir.IsWindowNode(n) {
					continue
				}
				if pageCount(n) > 0 {
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
		return 0, err
	}
	if stack == nil {
		return 0, nil
	}
	return pageCount(stack), nil
}

func pageCount(n *ir.NodeInst) int {
	count := 0
	for _, s := range n.Children {
		if p, ok := s.(*ir.NodeInst); ok && p.Record != nil {
			count++
		}
	}
	return count
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

// pageDocument rewrites stmts, a clone, so that the stack holding the pages
// stands for its i'th page alone: the page's content, in the stack's place.
// `pages.current` in the document is that page's record.
func pageDocument(stmts *[]ir.Stmt, i int, ctx *evalCtx, static bool) documentPage {
	var out documentPage
	var walk func(stmts []ir.Stmt) []ir.Stmt
	walk = func(stmts []ir.Stmt) []ir.Stmt {
		var res []ir.Stmt
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				if !ir.IsWindowNode(n) && pageCount(n) > 0 {
					res = append(res, out.take(n, i, ctx, static)...)
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

func (out *documentPage) take(stack *ir.NodeInst, i int, ctx *evalCtx, static bool) []ir.Stmt {
	k := 0
	for _, s := range stack.Children {
		p, ok := s.(*ir.NodeInst)
		if !ok || p.Record == nil {
			continue
		}
		if k != i {
			k++
			continue
		}
		out.node, out.static = p, static
		if v, ok := evalExpr(p.Record, ctx); ok {
			ctx.navCurrent = v
		}
		body := p.Children
		if out.param = p.Params; out.param != nil && static {
			if v, ok := evalExpr(p.Prop("params"), ctx); ok {
				ctx.values[out.param] = v
			}
		}
		return body
	}
	return nil
}
