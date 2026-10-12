package html

import (
	"fmt"
	"iter"
	"slices"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// Which documents a program is, on a target whose view is markup: the answer
// is html's, and what it is answered with is the build's fold (codegen.Fold),
// which knows the values a document is written with and nothing about pages.
//
// A document is written from a surface: the first node of a primitive marked
// #[gen.renders(surface)] -- this platform's Window -- that no `if` over state
// and no `for` can take away, which the lowering decides and marks
// (documentSurface). The package body around it is
// written into every document, the other surfaces included, which this
// platform shows inside it as dialogs.
//
// A surface holding a nav.stack is one document per page instead, the page
// standing where the stack was (surfaceDocuments): a static site writes each
// at its href, and route mode serves each at its route.

// documentsOf yields every document pkg renders, one at a time, folded with
// what fold knows. static is a site written out at build time rather than one
// a language serves per request, which decides whether a page's params cell
// is its own params or the request's.
func documentsOf(pkg *ir.Package, fold codegen.Fold, static bool) iter.Seq2[*codegen.Document, error] {
	return func(yield func(*codegen.Document, error) bool) {
		// A document that asked for no call in a round that finished was folded
		// whole, and answers to other documents' calls cannot change it, so the
		// next round probes only the documents that asked. A round that failed
		// may not have reached every document, or known them all.
		var askers map[int]bool
		if err := fold.SettleNatives(func(probe codegen.Fold) error {
			doc, _ := documentSurface(pkg)
			if doc == nil || !probe.HoldsNativeCall(doc) {
				return nil
			}
			want := func(i int) bool { return askers == nil || askers[i] }
			asked, before := map[int]bool{}, probe.NativesAsked()
			_, err := surfaceDocuments(doc, probe, static, want, func(i int, _ *codegen.Document, dfold codegen.Fold) bool {
				if n := dfold.NativesAsked(); n > before {
					asked[i], before = true, n
				}
				return true
			})
			askers = asked
			if err != nil {
				askers = nil
			}
			return err
		}); err != nil {
			yield(nil, err)
			return
		}

		if doc, body := documentSurface(pkg); doc != nil {
			before, after := bodyAround(body, doc)
			_, err := surfaceDocuments(doc, fold, static, nil, func(_ int, d *codegen.Document, dfold codegen.Fold) bool {
				if len(before)+len(after) > 0 {
					d.Body = slices.Concat(dfold.Stmts(dfold.Clone(before)), d.Body, dfold.Stmts(dfold.Clone(after)))
				}
				return yieldDocument(yield, d, dfold, static)
			})
			if err != nil {
				yield(nil, err)
			}
			return
		}
		root := pkg.RootDecl()
		if root == nil {
			return
		}
		// A harness renders its root component in place of a surface.
		stack, err := documentStack(root.Stmts, fold)
		if err != nil {
			yield(nil, err)
			return
		}
		if stack == nil {
			dfold := fold.Child()
			body := dfold.Stmts(dfold.Clone(root.Stmts))
			yieldDocument(yield, &codegen.Document{Body: body}, dfold, static)
			return
		}
		for _, pc := range documentCopies(stack, fold) {
			dfold := fold.Child()
			body := cloneWithOnly(stack, pc, func() []ir.Stmt { return dfold.Clone(root.Stmts) })
			page := pageDocument(&body, pc, dfold, true)
			body = dfold.Stmts(body)
			if !yieldDocument(yield, &codegen.Document{Body: body, Page: page.node}, dfold, static) {
				return
			}
		}
	}
}

func yieldDocument(yield func(*codegen.Document, error) bool, doc *codegen.Document, fold codegen.Fold, static bool) bool {
	if err := fold.Err(); err != nil {
		yield(nil, err)
		return false
	}
	// The build left a call only it can answer in the view for the fold to
	// answer per document; one still standing has nothing left to answer it.
	if err := codegen.RefuseUnfoldedBuildCalls(doc.Body); err != nil {
		yield(nil, err)
		return false
	}
	// A served page renders a loop on the server, over what the request
	// brought; only a static one has nowhere to run it.
	if static {
		if err := refuseUnrolledViewLoops(doc.Body); err != nil {
			yield(nil, err)
			return false
		}
	}
	doc.FileAssets = append(doc.FileAssets, fold.TakeFileAssets()...)
	return yield(doc, nil)
}

// documentSurface is the surface the document is written from and the
// statement list holding it, nil where it stands under an `if` or a boundary
// rather than in the list itself. Which surface that is was decided by the
// lowering, before inlining, where navigation is decided per surface
// (lower's findSurfaces): the first that no `if` over state and no `for` can
// take away. It is marked (ir.NodeInst.Document), and read here rather than
// decided a second time. Nil for a package that renders none, which a harness
// isolating a component is.
func documentSurface(pkg *ir.Package) (*ir.NodeInst, []ir.Stmt) {
	var walk func(stmts []ir.Stmt) (*ir.NodeInst, []ir.Stmt)
	walk = func(stmts []ir.Stmt) (*ir.NodeInst, []ir.Stmt) {
		for _, s := range stmts {
			if n, ok := s.(*ir.NodeInst); ok && n.Document && ir.IsSurface(n.Component) {
				return n, stmts
			}
			for _, b := range ir.TransparentBlocks(s) {
				if d, _ := walk(*b); d != nil {
					return d, nil
				}
			}
		}
		return nil, nil
	}
	if d, body := walk(pkg.Body); d != nil {
		return d, body
	}
	if root := pkg.RootDecl(); root != nil {
		return walk(root.Stmts)
	}
	return nil, nil
}

// bodyAround is what body renders on either side of doc, the surface standing
// in it: the other surfaces this platform shows inside its document, placed
// where they were written, and the render slots of those under an `if`. Every
// document doc is written as carries them, a page's included.
func bodyAround(body []ir.Stmt, doc *ir.NodeInst) (before, after []ir.Stmt) {
	i := slices.IndexFunc(body, func(s ir.Stmt) bool { return s == ir.Stmt(doc) })
	if i < 0 {
		return nil, nil
	}
	keep := func(stmts []ir.Stmt) []ir.Stmt {
		var out []ir.Stmt
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				out = append(out, n)
			case *ir.CallStmt:
				if n.Call != nil && n.Call.Func != nil && n.Call.Func.Lowered.SlotRender {
					out = append(out, n)
				}
			}
		}
		return out
	}
	return keep(body[:i]), keep(body[i+1:])
}

// surfaceDocuments hands each document w, a surface, is written as to yield:
// w itself when it holds no stack, and one per page copy when it holds one.
// False when yield stopped the walk. want, when not nil, picks the documents
// to write by their index in that order, and yield is handed the index; the
// order is the same on every call that finds the same pages.
//
// A surface holding a nav.stack is one document per page: the whole surface,
// with the stack standing for that page alone. A page is the node carrying a
// page's value (ir.NodeInst.Record), which this platform's Page primitive is
// handed as it stands in for the node the program wrote; its stack is the node
// holding it.
//
// A page may be written under a `for` over a constant and an `if` the build
// decides, so a document is a *copy* of a page: the page node and, for each
// loop around it, the iteration it is. The surface is cloned holding that one
// page, the loops' variables bound for the iteration, so a site of a thousand
// pages holds one page's tree at a time.
func surfaceDocuments(w *ir.NodeInst, wfold codegen.Fold, static bool, want func(int) bool, yield func(int, *codegen.Document, codegen.Fold) bool) (bool, error) {
	stack, err := documentStack(w.Children, wfold)
	if err != nil {
		return false, err
	}
	if stack == nil {
		if want != nil && !want(0) {
			return true, nil
		}
		dfold := wfold.Child()
		dfold.TakeFileAssets() // a document carries what its own fold resolved
		doc := dfold.Clone([]ir.Stmt{w})[0].(*ir.NodeInst)
		foldSurface(doc, dfold)
		return yield(0, &codegen.Document{Surface: doc, Body: doc.Children}, dfold), nil
	}
	copies := documentCopies(stack, wfold)
	if err := wfold.Err(); err != nil {
		return false, err
	}
	for i, pc := range copies {
		if want != nil && !want(i) {
			continue
		}
		dfold := wfold.Child()
		dfold.TakeFileAssets()
		doc := cloneWithOnly(stack, pc, func() []ir.Stmt { return dfold.Clone([]ir.Stmt{w}) })[0].(*ir.NodeInst)
		page := pageDocument(&doc.Children, pc, dfold, static)
		if !page.static {
			doc.Params = page.param
		}
		foldSurface(doc, dfold)
		if err := dfold.Err(); err != nil {
			return false, err
		}
		if !yield(i, &codegen.Document{Surface: doc, Body: doc.Children, Page: page.node}, dfold) {
			return false, nil
		}
	}
	return true, nil
}

// documentStack is the one stack stmts render, nil for none, refusing what
// cannot be decided per document: a second stack, which would make the
// documents a product of the two, and a stack under an `if` the build cannot
// fold, whose pages a document can neither hold nor leave out.
func documentStack(stmts []ir.Stmt, fold codegen.Fold) (*ir.NodeInst, error) {
	var stack *ir.NodeInst
	var walk func(stmts []ir.Stmt) error
	walk = func(stmts []ir.Stmt) error {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				// A surface shown inside the document navigates in place.
				if ir.IsSurface(n.Component) {
					continue
				}
				if holdsPages(n.Children) {
					if stack != nil {
						return fmt.Errorf("%s: a window's documents are its pages, and this is a second nav.stack in it; the first is at %s", ir.NodePos(n), ir.NodePos(stack))
					}
					stack = n
					continue
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
					if _, ok := fold.Decide(n.Cond); !ok {
						return fmt.Errorf("%s: a nav.stack under an `if` that reads state cannot be decided per document; write the `if` inside a page", ir.NodePos(stack))
					}
				}
				continue
			}
			for _, b := range ir.ViewBlocks(s) {
				if err := walk(*b); err != nil {
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
func documentCopies(stack *ir.NodeInst, fold codegen.Fold) []pageCopy {
	var out []pageCopy
	var walk func(stmts []ir.Stmt, fold codegen.Fold, path []pageStep)
	walk = func(stmts []ir.Stmt, fold codegen.Fold, path []pageStep) {
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
				v, ok := fold.Decide(n.Cond)
				if !ok {
					fold.Fail(fmt.Errorf("%s: a page under an `if` the build cannot decide is in no document and every one at once; decide it from constants", ir.StmtPos(n)))
					continue
				}
				if v {
					walk(n.Body, fold, at(0))
				} else {
					walk(n.Else, fold, at(1))
				}
			case *ir.For:
				items, ok := fold.LoopItems(n)
				if !ok {
					fold.Fail(fmt.Errorf("%s: pages under a `for` whose iterable the build cannot evaluate have no documents; iterate a constant", ir.StmtPos(n)))
					continue
				}
				for k, item := range items {
					child := fold.Child()
					child.BindLoop(n, k, item)
					walk(n.Body, child, at(k))
					if err := child.Err(); err != nil {
						fold.Fail(err)
					}
				}
			}
		}
	}
	walk(stack.Children, fold, nil)
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
	// in route mode left standing, for the route to bind.
	param  *ir.Param
	static bool
}

// pageDocument rewrites stmts, a clone made by cloneWithOnly, so that the
// stack holding the pages stands for the copy pc names alone: the page's
// content, in the stack's place, with the variables of the loops around it
// bound in fold. `pages.current` in the document is that copy's record.
func pageDocument(stmts *[]ir.Stmt, pc pageCopy, fold codegen.Fold, static bool) documentPage {
	var out documentPage
	var walk func(stmts []ir.Stmt) []ir.Stmt
	walk = func(stmts []ir.Stmt) []ir.Stmt {
		var res []ir.Stmt
		for _, s := range stmts {
			if n, ok := s.(*ir.NodeInst); ok && holdsPages(n.Children) {
				res = append(res, out.take(n, pc, fold, static)...)
				continue
			}
			if _, ok := s.(*ir.For); !ok {
				for _, b := range ir.ViewBlocks(s) {
					*b = walk(*b)
				}
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
func (out *documentPage) take(stack *ir.NodeInst, pc pageCopy, fold codegen.Fold, static bool) []ir.Stmt {
	stmts := stack.Children
	// The fold's copy shares a loop's body, so a page reached through one is
	// the program's own until it is copied here.
	shared := false
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
			items, ok := fold.LoopItems(n)
			if !ok || step.sub >= len(items) {
				return nil
			}
			fold.BindLoop(n, step.sub, items[step.sub])
			stmts, shared = n.Body, true
		case *ir.NodeInst:
			if shared {
				cp := *n
				cp.Record = ir.CloneExprSharingDecls(n.Record)
				cp.Children = fold.Clone(n.Children)
				n = &cp
			}
			out.node, out.static = n, static
			// The record is the copy's: under a `for` it reads the loop's
			// variables, which fold now binds.
			n.Record = fold.Expr(n.Record)
			if v, ok := fold.Eval(n.Record); ok {
				fold.Answer(ir.NavCurrentID, v)
			}
			if out.param = n.Params; out.param != nil && static {
				if v, ok := fold.Eval(n.Prop(ir.NavPageParams)); ok {
					fold.Bind(out.param, v)
				}
			}
			return n.Children
		}
	}
	return nil
}

// foldSurface folds what a surface carries: its props, its handlers and what
// it renders.
func foldSurface(n *ir.NodeInst, fold codegen.Fold) {
	for i := range n.Props {
		if n.Props[i].Value != nil {
			n.Props[i].Value = fold.Expr(n.Props[i].Value)
		}
	}
	for i := range n.Handlers {
		if f := n.Handlers[i].Func; f != nil {
			f.Block = fold.Stmts(f.Block)
		}
	}
	n.Children = fold.Stmts(n.Children)
}

// refuseUnrolledViewLoops reports a `for` left standing in a document's view.
// A loop over state is a render slot by now, so one still here is a loop the
// page writes into its markup, and the fold could not work out what it
// iterates: written as it stands, its body came out once with its variable
// bound to nothing -- an empty row and a handler naming a variable nothing
// declared. Only a loop that writes markup is asked: a canvas's children are
// the statements that paint it by now, and a loop among them is drawing code
// the page runs. A loop an override's body wrote is reported at the node the
// program wrote, since the override's is no position the program can act on.
func refuseUnrolledViewLoops(stmts []ir.Stmt) error {
	var err error
	var walk func(stmts []ir.Stmt, site ir.Stmt)
	walk = func(stmts []ir.Stmt, site ir.Stmt) {
		for _, s := range stmts {
			if err != nil {
				return
			}
			if fs, ok := s.(*ir.For); ok && rendersNodes(fs.Body) {
				pos := ir.StmtPos(fs)
				if n, ok := site.(*ir.NodeInst); ok && n.Site != nil {
					if at := ir.NodePos(n); at.File != pos.File {
						pos = at
					}
				}
				err = fmt.Errorf("%s: this loop is written into the page, and what it iterates cannot be worked out while the page is built: iterate a constant, or state, which the page's script renders", pos)
				return
			}
			inner := site
			if n, ok := s.(*ir.NodeInst); ok && n.Site != nil {
				inner = n
			}
			for _, b := range ir.ViewBlocks(s) {
				walk(*b, inner)
			}
		}
	}
	walk(stmts, nil)
	return err
}

// rendersNodes reports whether stmts put a node in the tree.
func rendersNodes(stmts []ir.Stmt) bool {
	found := false
	ir.WalkView(stmts, func(s ir.Stmt) bool {
		if _, ok := s.(*ir.NodeInst); ok {
			found = true
		}
		return !found
	})
	return found
}
