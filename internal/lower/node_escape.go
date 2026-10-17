package lower

import (
	"strings"

	"duckfam.us/sngl/ir"
)

// passNodeEscape runs only for MutationModel platforms (those that flatten
// the declarative tree — NoDeclarative). After passDeclarative has emitted
// the flat `var __nN = lower.CreateNode(...)` + `#__nN` ref sequence, this
// pass performs escape analysis on the synthesized widget refs.
//
// A widget ref needs to live as a shared Model field ONLY when it is
// referenced from a scope other than the one that creates it (a reactive
// updater func, an event handler, a slot/render func that mutates it after
// creation). A ref created and used only within a single emitted scope —
// the common case for a recursive component render method's internal temps
// and for many main-tree leaves — can be a function-LOCAL variable. Making
// it local means each recursion frame gets its own copy, so a recursive
// component can no longer overwrite a parent frame's widget temp and append
// a box to itself (the gtk4 infinite-layout hang).
//
// The pass records, per creating scope, the set of NON-escaping ("local")
// ref ids on the scope's IR node (Component.LocalRefs / Func.Lowered.LocalRefs /
// Window.LocalRefs). The platform translators consult these sets to decide
// whether to emit a Model field or a bare local. Escaping refs keep the
// existing Model-field behavior, so reactive updates are unaffected.
var passNodeEscape = pass{
	name:    "NodeEscape",
	enabled: func(c Features) bool { return !c.Declarative },
	apply:   lowerNodeEscape,
}

// scopeKind identifies which IR node owns a scope, so the result map can be
// stored back on the right node.
type scopeRefInfo struct {
	created map[string]bool // ref ids whose CreateNode/CreateComponent LocalVar lives here
	used    map[string]bool // every name read anywhere in this scope
	setSink *map[string]bool
	// nested collects scopes for inline closure/lambda bodies discovered
	// while walking this scope. Each emitted lambda is its own function, so
	// a ref created in the enclosing scope but used in a nested lambda body
	// crosses an emitted-function boundary and must escape. Collected here
	// and appended to the global scope list so the cross-scope use registers.
	nested []*scopeRefInfo
}

func newScopeRefInfo(sink *map[string]bool) *scopeRefInfo {
	return &scopeRefInfo{
		created: map[string]bool{},
		used:    map[string]bool{},
		setSink: sink,
	}
}

func lowerNodeEscape(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}

	var scopes []*scopeRefInfo
	// flatten appends s and every nested (inline lambda/closure) scope it
	// discovered, recursively, to the global scope list.
	var flatten func(s *scopeRefInfo)
	flatten = func(s *scopeRefInfo) {
		scopes = append(scopes, s)
		for _, n := range s.nested {
			flatten(n)
		}
	}
	// A component built at run time is a record of its own: the nodes it
	// creates are its fields or its locals, and nothing outside the record
	// names them. A scope outside it using a node of the same name -- the
	// page's, a spliced copy of the same component's -- is using that one.
	var group *ir.Component
	groupOf := map[*scopeRefInfo]*ir.Component{}
	addScope := func(stmts []ir.Stmt, sink *map[string]bool) {
		s := newScopeRefInfo(sink)
		collectScopeRefs(stmts, s)
		start := len(scopes)
		flatten(s)
		for _, sc := range scopes[start:] {
			groupOf[sc] = group
		}
	}

	// Top-level funcs (handlers promoted by passDeclarative, reactive
	// updaters/slot funcs, computeds, user funcs).
	for _, f := range pkg.Funcs {
		addScope(f.Block, &f.Lowered.LocalRefs)
	}
	for _, comp := range pkg.Components {
		group = nil
		if comp.RuntimeInstance {
			group = comp
		}
		addScope(comp.Stmts, &comp.LocalRefs)
		for _, f := range comp.Funcs {
			addScope(f.Block, &f.Lowered.LocalRefs)
		}
		group = nil
		for _, v := range comp.Vars {
			addVarHandlerScopes(v, &scopes)
		}
	}
	for _, v := range pkg.Vars {
		addVarHandlerScopes(v, &scopes)
	}

	// A created id ESCAPES if a scope that does not create it uses it. Two
	// scopes may each create one -- two components each naming an instance
	// `#b` -- and each is then its own scope's local.
	type groupID struct {
		group *ir.Component
		id    string
	}
	escaped := map[groupID]bool{}
	for _, s := range scopes {
		for id := range s.used {
			if !s.created[id] {
				escaped[groupID{groupOf[s], id}] = true
			}
		}
	}

	// For each scope, record its created-but-non-escaping ids as local.
	for _, s := range scopes {
		local := map[string]bool{}
		for id := range s.created {
			if !escaped[groupID{groupOf[s], id}] {
				local[id] = true
			}
		}
		if len(local) > 0 {
			*s.setSink = local
		}
	}
	return nil
}

func appendNested(scopes *[]*scopeRefInfo, s *scopeRefInfo) {
	*scopes = append(*scopes, s)
	for _, n := range s.nested {
		appendNested(scopes, n)
	}
}

func addVarHandlerScopes(v *ir.Var, scopes *[]*scopeRefInfo) {
}

// isSynthNodeID reports whether id is a synthesized widget ref name (__nN).
func isSynthNodeID(id string) bool { return strings.HasPrefix(id, "__n") }

// createdNodeID returns the widget ref id a LocalVar declares, else "": any
// synthesized `__nN`, whatever initialises it -- a reactive slot's reconcile
// opens each position with `var __nN <Comp> = null` -- and a reconcile's
// locals under the id the program gave the instance (reconcileLocal).
func createdNodeID(lv *ir.LocalVar) string {
	if lv == nil {
		return ""
	}
	if isSynthNodeID(lv.Name) || reconcileLocal(lv) {
		return lv.Name
	}
	return ""
}

// reconcileLocal reports whether lv is one of the locals a slot's instance
// reconcile declares under the id the program gave the instance: its
// position, the instance it builds, and the root it binds.
func reconcileLocal(lv *ir.LocalVar) bool {
	if c, ok := lv.Init.(*ir.Call); ok && c.Func != nil {
		return c.Func.Intrinsic == ir.NodeOpCreateComponent || c.Func.Intrinsic == ir.NodeOpComponentRoot
	}
	if lv.Sym == nil || !lv.Sym.Synthesized || lv.Type == nil || lv.Type.Kind != ir.TypeInstance {
		return false
	}
	return lv.Init == nil || isNullLiteral(lv.Init)
}

func isNullLiteral(e ir.Expr) bool {
	lit, ok := e.(*ir.Literal)
	return ok && lit.Type != nil && lit.Type.Kind == ir.TypeNull
}

// scopeFromFuncBody builds a scope for a nested inline closure/lambda's Func
// body, sinking its local-ref result onto the lambda Func's LocalRefs.
func scopeFromFuncBody(f *ir.Func) *scopeRefInfo {
	s := newScopeRefInfo(&f.Lowered.LocalRefs)
	collectScopeRefs(f.Block, s)
	return s
}

// collectScopeRefs walks one scope's statements (NOT descending into nested
// Lambda/Closure func bodies — those are their own emitted scopes) and
// records created and used widget ref ids into info.
func collectScopeRefs(stmts []ir.Stmt, info *scopeRefInfo) {
	for _, s := range stmts {
		collectScopeRefsStmt(s, info)
	}
}

func collectScopeRefsStmt(s ir.Stmt, info *scopeRefInfo) {
	switch n := s.(type) {
	case *ir.LocalVar:
		if id := createdNodeID(n); id != "" {
			info.created[id] = true
		}
		collectScopeRefsExpr(n.Init, info)
	case *ir.Assign:
		collectScopeRefsExpr(n.Target, info)
		collectScopeRefsExpr(n.Value, info)
	case *ir.Return:
		collectScopeRefsExpr(n.Value, info)
	case *ir.If:
		collectScopeRefsExpr(n.Cond, info)
		collectScopeRefs(n.Body, info)
		collectScopeRefs(n.Else, info)
	case *ir.Match:
		collectScopeRefsExpr(n.Subject, info)
		for _, arm := range n.Arms {
			for _, p := range arm.Patterns {
				collectScopeRefsExpr(p, info)
			}
			collectScopeRefs(arm.Body, info)
		}
	case *ir.For:
		collectScopeRefsExpr(n.Iter, info)
		collectScopeRefs(n.Body, info)
		collectScopeRefs(n.Else, info)
	case *ir.NodeInst:
		// A surviving NodeInst — its props/children may reference refs.
		// Children are still tree-shaped here only in pre-declarative passes;
		// after passDeclarative they are flattened. Walk defensively.
		for i := range n.Props {
			collectScopeRefsExpr(n.Props[i].Value, info)
		}
		collectScopeRefsExpr(n.Key, info)
		collectScopeRefsExpr(n.Ref, info)
		collectScopeRefs(ir.WidgetChildren(n), info)
	case *ir.SlotInst:
		collectScopeRefs(n.Children, info)
	case *ir.ErrorBoundary:
		collectScopeRefs(n.Children, info)
	case *ir.Emit:
		for i := range n.Args {
			collectScopeRefsExpr(n.Args[i].Value, info)
		}
	case *ir.CallStmt:
		collectScopeRefsExpr(n.Call, info)
	case *ir.Toggle:
		collectScopeRefsExpr(n.Target, info)
	case *ir.ContextProvider:
		collectScopeRefsExpr(n.Value, info)
		collectScopeRefs(n.Children, info)
	case *ir.CanvasRedrawStmt:
		// No widget ref operands.
	default:
		// Unknown stmt — ignore rather than panic; the pass is advisory and
		// a missed ref only forces a (still-correct) Model field.
	}
}

func collectScopeRefsExpr(e ir.Expr, info *scopeRefInfo) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Ident:
		// Any read of the name counts, element ref or not: an instance handle
		// is reached through a plain ident, and a use that crosses an emitted
		// function is an escape whichever flag the ident happens to carry.
		// Every name is noted, since a reconcile's are the program's ids.
		info.used[x.Name] = true
	case *ir.Binary:
		collectScopeRefsExpr(x.Left, info)
		collectScopeRefsExpr(x.Right, info)
	case *ir.Unary:
		collectScopeRefsExpr(x.Operand, info)
	case *ir.Ternary:
		collectScopeRefsExpr(x.Cond, info)
		collectScopeRefsExpr(x.Then, info)
		collectScopeRefsExpr(x.Else, info)
	case *ir.MatchExpr:
		collectScopeRefsExpr(x.Subject, info)
		for _, arm := range x.Arms {
			for _, p := range arm.Patterns {
				collectScopeRefsExpr(p, info)
			}
			collectScopeRefsExpr(arm.Value, info)
		}
	case *ir.Call:
		collectScopeRefsExpr(x.Receiver, info)
		collectScopeRefsExpr(x.Callee, info)
		for i := range x.Args {
			collectScopeRefsExpr(x.Args[i].Value, info)
		}
	case *ir.Conversion:
		collectScopeRefsExpr(x.Operand, info)
	case *ir.Select:
		collectScopeRefsExpr(x.Operand, info)
	case *ir.Index:
		collectScopeRefsExpr(x.Operand, info)
		collectScopeRefsExpr(x.Idx, info)
	case *ir.ListLit:
		for _, el := range x.Elems {
			collectScopeRefsExpr(el, info)
		}
	case *ir.StructLit:
		for i := range x.Fields {
			collectScopeRefsExpr(x.Fields[i].Value, info)
		}
	case *ir.Spread:
		collectScopeRefsExpr(x.Operand, info)
	case *ir.MapLitIR:
		for _, en := range x.Entries {
			collectScopeRefsExpr(en.Key, info)
			collectScopeRefsExpr(en.Value, info)
		}
	case *ir.Closure:
		if x.Func != nil {
			info.nested = append(info.nested, scopeFromFuncBody(x.Func))
		}
	case *ir.Lambda:
		if x.Func != nil {
			info.nested = append(info.nested, scopeFromFuncBody(x.Func))
		}
	case *ir.Literal, *ir.ContextRead:
		// Terminal — no refs.
	}
}
