package lower

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passEffect turns each `effect` node into the calls that run its bracket.
//
// Placed before passReactivity, which is the whole reason the box a fetch
// answers into needs no machinery of its own: a mount handler that writes state
// becomes an ordinary assignment in the owner's body, so the ordinary updater
// injection sees it and patches whatever reads it. Lowered after that pass the
// assignment would be invisible and every backend would need telling that the
// value had changed.
//
// Always on. An effect is not a construct a platform can consume -- no codegen
// answers for the node -- so there is no target that wants it left standing.
var passEffect = pass{
	name:    "Effect",
	enabled: func(Caps) bool { return true },
	apply:   lowerEffects,
}

// TeardownFunc is the name of the handler a platform calls when the program is
// going away. Nothing guarantees it runs -- a killed process and a closed tab
// both skip it -- so what belongs in it is what a healthy exit should release,
// never what correctness depends on.
const TeardownFunc = "__snglTeardown"

func lowerEffects(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &effectState{pkg: pkg, reactive: collectReactiveVars(pkg)}
	for _, o := range ir.Owners(pkg) {
		if err := st.owner(o); err != nil {
			return err
		}
	}
	return st.finishTeardown()
}

type effectState struct {
	pkg      *ir.Package
	reactive map[*ir.Var]bool
	counter  int
	// teardown is every unmount call the program should make on a healthy
	// exit, in mount order. Reversed when the function is built.
	teardown []ownedStmt
	// rekeyByVar is the re-key work each state var triggers: writing it ends
	// the lifetimes keyed on it and begins the next.
	rekeyByVar map[*ir.Var][]rekey
}

// ownedStmt is a statement and the scope it has to be emitted in.
type ownedStmt struct {
	owner *ir.Owner
	stmt  ir.Stmt
}

// rekey is one effect's calls, injected after a write to a cell its key reads.
type rekey struct {
	unmount *ir.Func
	mount   *ir.Func
	keyVar  *ir.Var
	key     ir.Expr
}

func (st *effectState) owner(o ir.Owner) error {
	body, err := st.stmts(o.Stmts, &o, false)
	if err != nil {
		return err
	}
	switch {
	case o.Comp != nil:
		o.Comp.Body = body
	case o.Win != nil:
		o.Win.Body = body
	default:
		st.pkg.Body = body
	}
	return nil
}

// stmts rewrites one statement list, replacing effects with their calls.
//
// inReactive says the list will become a slot render func: passReactivity
// rebuilds such a body wholesale on every change its condition depends on, so a
// bracket spliced into one would tear down and set up on changes that were
// nothing to do with its key. That is the opposite of what `on` means, so it is
// refused rather than lowered wrongly -- the slot has to reconcile its brackets
// the way the interpreter does, and does not yet.
func (st *effectState) stmts(stmts []ir.Stmt, o *ir.Owner, inReactive bool) ([]ir.Stmt, error) {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if !isEffectNode(n) {
				kids, err := st.stmts(n.Children, o, inReactive)
				if err != nil {
					return nil, err
				}
				n.Children = kids
				out = append(out, n)
				continue
			}
			if inReactive {
				return nil, fmt.Errorf("%s: an effect inside reactive control flow is not lowered yet; move it to the component body", nodePos(n))
			}
			calls, err := st.lowerOne(n, o)
			if err != nil {
				return nil, err
			}
			out = append(out, calls...)
		case *ir.If:
			nested := inReactive || dependsOnReactiveVar(n.Cond, st.reactive)
			b, err := st.stmts(n.Body, o, nested)
			if err != nil {
				return nil, err
			}
			e, err := st.stmts(n.Else, o, nested)
			if err != nil {
				return nil, err
			}
			n.Body, n.Else = b, e
			out = append(out, n)
		case *ir.For:
			nested := inReactive || dependsOnReactiveVar(n.Iter, st.reactive)
			b, err := st.stmts(n.Body, o, nested)
			if err != nil {
				return nil, err
			}
			e, err := st.stmts(n.Else, o, nested)
			if err != nil {
				return nil, err
			}
			n.Body, n.Else = b, e
			out = append(out, n)
		case *ir.SlotInst:
			kids, err := st.stmts(n.Children, o, inReactive)
			if err != nil {
				return nil, err
			}
			n.Children = kids
			out = append(out, n)
		case *ir.ErrorBoundary:
			kids, err := st.stmts(n.Children, o, inReactive)
			if err != nil {
				return nil, err
			}
			n.Children = kids
			out = append(out, n)
		default:
			out = append(out, s)
		}
	}
	return out, nil
}

// lowerOne replaces one effect with the calls that run it: its setup where it
// was written, its teardown on the program's way out, and the pair again after
// every write to a cell its key reads.
//
// The bodies become funcs rather than being spliced, because a bracket keyed on
// state has more than one call site and inlining it would copy the body to each.
//
// A synthesized var holds the value the running lifetime is keyed on. It is what
// lets a handler be passed the key ITS OWN lifetime had: by the time a re-key
// runs, the cell the key reads already holds the next value, so an unmount
// reading it would see what it is not tearing down. It is also what makes an
// unchanged write do nothing -- the guard compares the two, so assigning a var
// the value it already held ends no lifetime.
func (st *effectState) lowerOne(n *ir.NodeInst, o *ir.Owner) ([]ir.Stmt, error) {
	id := st.counter
	st.counter++

	key := effectKeyExpr(n)
	var keyVar *ir.Var
	if key != nil {
		keyVar = &ir.Var{
			Name: "__effect" + strconv.Itoa(id) + "_key",
			Type: key.ExprType(),
			Init: key,
		}
	}

	mount := st.handlerFunc(n, "mount", "__effect"+strconv.Itoa(id)+"_mount", o, keyVar)
	unmount := st.handlerFunc(n, "unmount", "__effect"+strconv.Itoa(id)+"_unmount", o, keyVar)
	if mount == nil && unmount == nil {
		return nil, nil
	}
	if keyVar != nil {
		st.addVar(o, keyVar)
	}

	var out []ir.Stmt
	if mount != nil {
		out = append(out, callOf(mount))
	}
	if unmount != nil {
		st.teardown = append(st.teardown, ownedStmt{owner: o, stmt: callOf(unmount)})
	}

	// The key's own dependencies, so a write to any of them re-keys. A key
	// reading nothing reactive is one value forever, which is what an effect
	// with no `on` already is.
	if keyVar != nil {
		for _, v := range st.keyVars(key) {
			if st.rekeyByVar == nil {
				st.rekeyByVar = map[*ir.Var][]rekey{}
			}
			st.rekeyByVar[v] = append(st.rekeyByVar[v], rekey{
				unmount: unmount, mount: mount, keyVar: keyVar, key: key,
			})
		}
	}
	return out, nil
}

// handlerFunc lifts one side of the bracket into a func on the owner, or nil
// when the call site wrote none.
//
// The declared parameter is substituted away rather than passed. The key var
// already holds the value each call site wants -- the running key at setup and
// at teardown, the outgoing one at a re-key's unmount because the var has not
// been advanced yet -- so a parameter would carry what is already in scope, and
// every backend would have to agree on how to spell the argument.
func (st *effectState) handlerFunc(n *ir.NodeInst, event, name string, o *ir.Owner, keyVar *ir.Var) *ir.Func {
	for _, h := range n.Handlers {
		if h.Name != event || h.Func == nil {
			continue
		}
		block := h.Func.Block
		if keyVar != nil && len(h.Func.Params) > 0 {
			p := h.Func.Params[0]
			block = newExprWalker(func(e ir.Expr) ir.Expr {
				if id, isIdent := e.(*ir.Ident); isIdent && id.Sym == ir.Symbol(p) {
					return &ir.Ident{Name: keyVar.Name, Type: keyVar.Type, Sym: keyVar}
				}
				return e
			}).stmts(block)
		}
		fn := &ir.Func{
			Name:   name,
			Block:  block,
			Return: ir.TypVoid,
			Purity: ir.PurityMutates,
		}
		st.addFunc(o, fn)
		return fn
	}
	return nil
}

func (st *effectState) addFunc(o *ir.Owner, fn *ir.Func) {
	switch {
	case o.Comp != nil:
		o.Comp.Funcs = append(o.Comp.Funcs, fn)
	case o.Win != nil:
		o.Win.Funcs = append(o.Win.Funcs, fn)
	default:
		st.pkg.Funcs = append(st.pkg.Funcs, fn)
	}
}

func (st *effectState) addVar(o *ir.Owner, v *ir.Var) {
	switch {
	case o.Comp != nil:
		o.Comp.Vars = append(o.Comp.Vars, v)
	case o.Win != nil:
		o.Win.Vars = append(o.Win.Vars, v)
	default:
		st.pkg.Vars = append(st.pkg.Vars, v)
	}
}

// keyVars is the state a key reads, which is what re-keys it when written.
func (st *effectState) keyVars(key ir.Expr) []*ir.Var {
	var out []*ir.Var
	seen := map[*ir.Var]bool{}
	_ = ir.WalkExprs(key, func(e ir.Expr) error {
		id, isIdent := e.(*ir.Ident)
		if !isIdent {
			return nil
		}
		v, isVar := id.Sym.(*ir.Var)
		if !isVar || seen[v] || !st.reactive[v] {
			return nil
		}
		seen[v] = true
		out = append(out, v)
		return nil
	})
	return out
}

// finishTeardown builds the exit handler and injects the re-key calls.
func (st *effectState) finishTeardown() error {
	st.injectRekeys()
	if len(st.teardown) == 0 {
		return nil
	}
	// Every unmount has to reach one function a platform can call, so they all
	// have to be in one scope -- and after inlining they are, because a
	// component's effects were spliced into the root. A non-inlined owner, which
	// is a recursive component, would need a teardown of its own wired wherever
	// its instances are torn down: the same seam an instance's prop update
	// needs, and not built.
	owner := st.teardown[0].owner
	for _, t := range st.teardown[1:] {
		if t.owner != owner {
			return fmt.Errorf("Effect: an @unmount in a component that is not inlined has nowhere to hang its teardown")
		}
	}
	// Reverse mount order: an effect set up later may hold something an earlier
	// one handed it, so releasing in acquisition order can release a thing still
	// in use.
	body := make([]ir.Stmt, 0, len(st.teardown))
	for i := len(st.teardown) - 1; i >= 0; i-- {
		body = append(body, st.teardown[i].stmt)
	}
	fn := &ir.Func{
		Name:   TeardownFunc,
		Return: ir.TypVoid,
		Purity: ir.PurityMutates,
		Block:  body,
	}
	st.addFunc(owner, fn)
	st.pkg.Teardown = fn
	return nil
}

// injectRekeys puts a guarded unmount/mount pair after every write to a cell
// some key reads.
func (st *effectState) injectRekeys() {
	if len(st.rekeyByVar) == 0 {
		return
	}
	var rewrite func([]ir.Stmt) []ir.Stmt
	rewrite = func(stmts []ir.Stmt) []ir.Stmt {
		out := make([]ir.Stmt, 0, len(stmts))
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				n.Children = rewrite(n.Children)
				for i := range n.Handlers {
					if n.Handlers[i].Func != nil {
						n.Handlers[i].Func.Block = rewrite(n.Handlers[i].Func.Block)
					}
				}
			case *ir.If:
				n.Body, n.Else = rewrite(n.Body), rewrite(n.Else)
			case *ir.For:
				n.Body, n.Else = rewrite(n.Body), rewrite(n.Else)
			case *ir.SlotInst:
				n.Children = rewrite(n.Children)
			case *ir.ErrorBoundary:
				n.Children = rewrite(n.Children)
			}
			out = append(out, s)
			v := writtenVar(s)
			if v == nil {
				continue
			}
			for _, rk := range st.rekeyByVar[v] {
				out = append(out, rk.stmt())
			}
		}
		return out
	}
	for _, o := range ir.Owners(st.pkg) {
		switch {
		case o.Comp != nil:
			o.Comp.Body = rewrite(o.Comp.Body)
		case o.Win != nil:
			o.Win.Body = rewrite(o.Win.Body)
		default:
			st.pkg.Body = rewrite(st.pkg.Body)
		}
		for _, f := range o.Funcs {
			// Not into a handler this pass synthesized: a mount body that
			// writes the cell its own key reads would re-enter itself.
			if f != nil && !isEffectHelper(f) {
				f.Block = rewrite(f.Block)
			}
		}
	}
}

// stmt is the guarded pair one re-key emits:
//
//	if __effectN_key != <key> {
//	    __effectN_unmount(__effectN_key)
//	    __effectN_key = <key>
//	    __effectN_mount(__effectN_key)
//	}
func (rk rekey) stmt() ir.Stmt {
	keyRef := func() ir.Expr {
		return &ir.Ident{Name: rk.keyVar.Name, Type: rk.keyVar.Type, Sym: rk.keyVar}
	}
	var body []ir.Stmt
	if rk.unmount != nil {
		body = append(body, callOf(rk.unmount))
	}
	body = append(body, &ir.Assign{Target: keyRef(), Value: deepCloneExpr(rk.key)})
	if rk.mount != nil {
		body = append(body, callOf(rk.mount))
	}
	return &ir.If{
		Cond: &ir.Binary{
			Op:    ast.BinNeq,
			Left:  keyRef(),
			Right: deepCloneExpr(rk.key),
			Type:  ir.TypBool,
		},
		Body: body,
	}
}

// isEffectHelper reports whether fn is one this pass synthesized.
func isEffectHelper(fn *ir.Func) bool {
	return fn != nil && (strings.HasPrefix(fn.Name, "__effect") || fn.Name == TeardownFunc)
}

// writtenVar names the state a statement assigns to, or nil.
func writtenVar(s ir.Stmt) *ir.Var {
	var target ir.Expr
	switch n := s.(type) {
	case *ir.Assign:
		target = n.Target
	case *ir.Toggle:
		target = n.Target
	default:
		return nil
	}
	id, isIdent := target.(*ir.Ident)
	if !isIdent {
		return nil
	}
	v, isVar := id.Sym.(*ir.Var)
	if !isVar {
		return nil
	}
	return v
}

func callOf(fn *ir.Func) ir.Stmt {
	return &ir.CallStmt{Call: &ir.Call{Type: ir.TypVoid, Func: fn}}
}

// isEffectNode reports whether a node is a lifetime bracket, by the kind its
// declaration carries.
func isEffectNode(n *ir.NodeInst) bool {
	return n != nil && n.Component != nil && n.Component.Builtin == ir.BuiltinEffect
}

// effectKeyExpr is the `on` argument, or nil when the call site wrote none.
func effectKeyExpr(n *ir.NodeInst) ir.Expr {
	for _, arg := range n.Props {
		if arg.Name == "on" {
			return arg.Value
		}
	}
	return nil
}

// nodePos names a node for a diagnostic.
func nodePos(n *ir.NodeInst) string {
	if p := ir.StmtPos(n); p.IsValid() {
		return p.String()
	}
	return "effect"
}
