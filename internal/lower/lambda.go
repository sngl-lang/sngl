package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passLambda = pass{
	name:    "NoLambda",
	enabled: func(c Caps) bool { return c.NoLambda },
	apply:   lowerLambda,
}

// capture is one captured outer-scope binding referenced by a lambda body.
type capture struct {
	Sym     ir.Symbol // the outer Var or Param being captured
	Mutable bool      // true if the body assigns to Sym
}

// analyzeCaptures walks body collecting every Ident.Sym that resolves to a
// *Var or *Param not declared inside body's params. Mutability is set when
// the body contains an Assign whose Target chain bottoms out at the same Sym.
// Returned slice is in first-occurrence order (stable across runs).
func analyzeCaptures(body []ir.Stmt, params []*ir.Param) []capture {
	paramSet := make(map[ir.Symbol]bool, len(params))
	for _, p := range params {
		paramSet[p] = true
	}

	seen := make(map[ir.Symbol]int) // sym → index in result
	var caps []capture

	addRead := func(sym ir.Symbol) {
		if sym == nil || paramSet[sym] {
			return
		}
		switch sym.(type) {
		case *ir.Var, *ir.Param:
			// OK
		default:
			return
		}
		if _, ok := seen[sym]; ok {
			return
		}
		seen[sym] = len(caps)
		caps = append(caps, capture{Sym: sym})
	}

	markMutable := func(sym ir.Symbol) {
		if sym == nil {
			return
		}
		addRead(sym)
		if i, ok := seen[sym]; ok {
			caps[i].Mutable = true
		}
	}

	var walkExpr func(ir.Expr)
	var walkStmt func(ir.Stmt)
	var walkStmts func([]ir.Stmt)

	walkExpr = func(e ir.Expr) {
		switch x := e.(type) {
		case nil:
			return
		case *ir.Ident:
			addRead(x.Sym)
		case *ir.Binary:
			walkExpr(x.Left)
			walkExpr(x.Right)
		case *ir.Unary:
			walkExpr(x.Operand)
		case *ir.Ternary:
			walkExpr(x.Cond)
			walkExpr(x.Then)
			walkExpr(x.Else)
		case *ir.Call:
			walkExpr(x.Receiver)
			for i := range x.Args {
				walkExpr(x.Args[i].Value)
			}
		case *ir.Conversion:
			walkExpr(x.Operand)
		case *ir.Select:
			walkExpr(x.Operand)
		case *ir.Index:
			walkExpr(x.Operand)
			walkExpr(x.Idx)
		case *ir.ListLit:
			for _, el := range x.Elems {
				walkExpr(el)
			}
		case *ir.StructLit:
			for i := range x.Fields {
				walkExpr(x.Fields[i].Value)
			}
		case *ir.Spread:
			walkExpr(x.Operand)
		case *ir.Lambda:
			// Nested lambdas: their own captures are the responsibility of
			// their own Lift call. Do not descend (the outer lambda's
			// captures are only the Idents directly visible at this level).
			return
		case *ir.Closure:
			// Already-lifted; do not descend.
			return
		}
	}

	rootSym := func(e ir.Expr) ir.Symbol {
		for {
			switch x := e.(type) {
			case *ir.Ident:
				return x.Sym
			case *ir.Select:
				e = x.Operand
			case *ir.Index:
				e = x.Operand
			case *ir.Unary:
				if x.Op == ast.UnaryDeref {
					e = x.Operand
					continue
				}
				return nil
			default:
				return nil
			}
		}
	}

	walkStmt = func(s ir.Stmt) {
		switch n := s.(type) {
		case *ir.Assign:
			markMutable(rootSym(n.Target))
			walkExpr(n.Target)
			walkExpr(n.Value)
		case *ir.LocalVar:
			walkExpr(n.Init)
		case *ir.Return:
			walkExpr(n.Value)
		case *ir.If:
			walkExpr(n.Cond)
			walkStmts(n.Body)
			walkStmts(n.Else)
		case *ir.For:
			walkExpr(n.Iter)
			walkStmts(n.Body)
			walkStmts(n.Else)
		case *ir.PlatformFilter:
			walkStmts(n.Body)
		case *ir.NodeInst:
			for i := range n.Props {
				walkExpr(n.Props[i].Value)
			}
			walkExpr(n.Key)
			walkExpr(n.Ref)
			walkStmts(n.Children)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					walkStmts(n.Handlers[i].Func.Block)
				}
			}
		case *ir.SlotInst:
			walkStmts(n.Children)
		case *ir.ErrorBoundary:
			walkStmts(n.Children)
			if n.Handler != nil && n.Handler.Func != nil {
				walkStmts(n.Handler.Func.Block)
			}
		case *ir.Emit:
			for i := range n.Args {
				walkExpr(n.Args[i].Value)
			}
		case *ir.CallStmt:
			if n.Call != nil {
				walkExpr(n.Call)
			}
		case *ir.Toggle:
			markMutable(rootSym(n.Target))
			walkExpr(n.Target)
		case *ir.Window:
			walkExpr(n.Href)
			walkExpr(n.Title)
			walkExpr(n.Favicon)
			walkStmts(n.Body)
		}
	}

	walkStmts = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			walkStmt(s)
		}
	}

	walkStmts(body)
	return caps
}

// lowerLambda lifts closures to top-level functions plus captured-state
// structs. Phase B Task 3: capture analysis and the lifter are in place; the
// pass body is still a placeholder until Task 4 (apply wiring).
func lowerLambda(pkg *ir.Package) error { return nil }

// lifter owns NoLambda's package-scoped state. The same lifter is shared
// between the main pass and NoDeclarative's handler-promote step.
type lifter struct {
	pkg     *ir.Package
	counter int
	// enclosing tracks ancestor lift frames so a nested lambda capturing
	// the same Sym as its enclosing lambda re-uses the outer's ref-typed
	// state field instead of taking a fresh address.
	enclosing []scopeFrame
}

type scopeFrame struct {
	// captureField maps a captured outer Symbol to the access expression
	// that reaches it from this frame's lifted body — typically
	// Select{Ident{state}, fieldName}, or Unary{Deref, Select{...}} for
	// mutable captures.
	captureField map[ir.Symbol]ir.Expr
}

// Lift converts a closure-shaped (body, params, return) into a top-level Func
// + caps StructDef and returns the *ir.Closure that replaces the original
// lambda expression.
func (l *lifter) Lift(body []ir.Stmt, params []*ir.Param, ret *ir.Type, src *ast.LambdaExpr) *ir.Closure {
	caps := analyzeCaptures(body, params)

	// Synthesize names.
	capsName, funcName := l.freshNames()

	// Build the state struct definition.
	capsDef := &ir.StructDef{Name: capsName}
	for _, c := range caps {
		fieldType := c.Sym.SymType()
		if c.Mutable {
			fieldType = ir.RefOf(fieldType)
		}
		capsDef.Fields = append(capsDef.Fields, &ir.StructField{
			Name: c.Sym.SymName(),
			Type: fieldType,
		})
	}
	l.pkg.Structs = append(l.pkg.Structs, capsDef)

	capsType := &ir.Type{Kind: ir.TypeStruct, Decl: capsDef}

	// Synthesize the state Param.
	stateParam := &ir.Param{Name: "state", Type: capsType}

	// Synthesize the lifted Func.
	lifted := &ir.Func{
		Name:   funcName,
		Params: append([]*ir.Param{stateParam}, params...),
		Return: ret,
	}

	// Build the rewrite map: captured Sym → expression accessing it inside
	// the lifted body. State is value-typed; mutable fields are ref<T>.
	captureField := make(map[ir.Symbol]ir.Expr, len(caps))
	for _, c := range caps {
		sel := &ir.Select{
			Operand: &ir.Ident{Name: "state", Sym: stateParam, Type: capsType},
			Field:   c.Sym.SymName(),
			Type:    c.Sym.SymType(),
		}
		var access ir.Expr = sel
		if c.Mutable {
			// Field type is ref<T>; reads/writes go through Unary{Deref}.
			sel.Type = ir.RefOf(c.Sym.SymType())
			access = &ir.Unary{
				Op:      ast.UnaryDeref,
				Operand: sel,
				Type:    c.Sym.SymType(),
			}
		}
		captureField[c.Sym] = access
	}

	// Push frame and rewrite body.
	l.enclosing = append(l.enclosing, scopeFrame{captureField: captureField})
	lifted.Block = l.rewriteStmts(body, captureField)
	l.enclosing = l.enclosing[:len(l.enclosing)-1]

	l.pkg.Funcs = append(l.pkg.Funcs, lifted)

	// Build the State StructLit at the original lambda position.
	state := &ir.StructLit{Type: capsType, Def: capsDef}
	for _, c := range caps {
		var fieldValue ir.Expr
		// If an enclosing frame already aliases this Sym, re-use its access
		// expression (already a ref<T> read or value access). Otherwise
		// synthesize a fresh Ident or & based on mutability.
		if outer := l.outerCaptureAccess(c.Sym); outer != nil {
			fieldValue = cloneExpr(outer)
		} else {
			ident := &ir.Ident{Name: c.Sym.SymName(), Sym: c.Sym, Type: c.Sym.SymType()}
			if c.Mutable {
				fieldValue = &ir.Unary{
					Op:      ast.UnaryAddr,
					Operand: ident,
					Type:    ir.RefOf(c.Sym.SymType()),
				}
			} else {
				fieldValue = ident
			}
		}
		state.Fields = append(state.Fields, ir.FieldInit{
			Name:  c.Sym.SymName(),
			Value: fieldValue,
		})
	}

	// Record capture names for NoReactivity.
	capMap := make(map[ir.Symbol]string, len(caps))
	for _, c := range caps {
		capMap[c.Sym] = c.Sym.SymName()
	}
	if l.pkg.LiftedCaptures == nil {
		l.pkg.LiftedCaptures = map[*ir.Func]map[ir.Symbol]string{}
	}
	l.pkg.LiftedCaptures[lifted] = capMap

	// User-visible signature: original params + return only.
	userSig := &ir.FuncSig{Params: params, Return: ret}

	return &ir.Closure{
		AST:   src,
		Type:  &ir.Type{Kind: ir.TypeFunc, Sig: userSig},
		Func:  lifted,
		State: state,
	}
}

// outerCaptureAccess returns the access expression an enclosing frame
// already exposes for sym, or nil if no enclosing frame captures sym.
func (l *lifter) outerCaptureAccess(sym ir.Symbol) ir.Expr {
	for i := len(l.enclosing) - 1; i >= 0; i-- {
		if access, ok := l.enclosing[i].captureField[sym]; ok {
			return access
		}
	}
	return nil
}

// freshNames returns (capsName, funcName) skipping past any existing names
// in pkg.Structs / pkg.Funcs.
func (l *lifter) freshNames() (string, string) {
	for {
		capsName := "__lambda" + itoa(l.counter) + "_caps"
		funcName := "__lambda" + itoa(l.counter)
		l.counter++
		if !l.nameExists(capsName) && !l.nameExists(funcName) {
			return capsName, funcName
		}
	}
}

func (l *lifter) nameExists(name string) bool {
	for _, s := range l.pkg.Structs {
		if s.Name == name {
			return true
		}
	}
	for _, f := range l.pkg.Funcs {
		if f.Name == name {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// rewriteStmts replaces every Ident.Sym appearing in stmts that is in
// captureField with the field's access expression. It does not descend into
// nested *ir.Lambda nodes — those will be lifted by their own Lift call,
// which establishes its own frame and inherits the current frame via
// l.enclosing.
func (l *lifter) rewriteStmts(stmts []ir.Stmt, captureField map[ir.Symbol]ir.Expr) []ir.Stmt {
	for _, s := range stmts {
		l.rewriteStmt(s, captureField)
	}
	return stmts
}

func (l *lifter) rewriteStmt(s ir.Stmt, captureField map[ir.Symbol]ir.Expr) {
	switch n := s.(type) {
	case *ir.Assign:
		n.Target = l.rewriteExpr(n.Target, captureField)
		n.Value = l.rewriteExpr(n.Value, captureField)
	case *ir.LocalVar:
		n.Init = l.rewriteExpr(n.Init, captureField)
	case *ir.Return:
		n.Value = l.rewriteExpr(n.Value, captureField)
	case *ir.If:
		n.Cond = l.rewriteExpr(n.Cond, captureField)
		l.rewriteStmts(n.Body, captureField)
		l.rewriteStmts(n.Else, captureField)
	case *ir.For:
		n.Iter = l.rewriteExpr(n.Iter, captureField)
		l.rewriteStmts(n.Body, captureField)
		l.rewriteStmts(n.Else, captureField)
	case *ir.PlatformFilter:
		l.rewriteStmts(n.Body, captureField)
	case *ir.Emit:
		for i := range n.Args {
			n.Args[i].Value = l.rewriteExpr(n.Args[i].Value, captureField)
		}
	case *ir.CallStmt:
		if n.Call != nil {
			n.Call = l.rewriteExpr(n.Call, captureField).(*ir.Call)
		}
	case *ir.Toggle:
		n.Target = l.rewriteExpr(n.Target, captureField)
	case *ir.NodeInst:
		for i := range n.Props {
			n.Props[i].Value = l.rewriteExpr(n.Props[i].Value, captureField)
		}
		n.Key = l.rewriteExpr(n.Key, captureField)
		n.Ref = l.rewriteExpr(n.Ref, captureField)
		l.rewriteStmts(n.Children, captureField)
		for i := range n.Handlers {
			if n.Handlers[i].Func != nil {
				l.rewriteStmts(n.Handlers[i].Func.Block, captureField)
			}
		}
	case *ir.SlotInst:
		l.rewriteStmts(n.Children, captureField)
	case *ir.ErrorBoundary:
		l.rewriteStmts(n.Children, captureField)
		if n.Handler != nil && n.Handler.Func != nil {
			l.rewriteStmts(n.Handler.Func.Block, captureField)
		}
	}
}

func (l *lifter) rewriteExpr(e ir.Expr, captureField map[ir.Symbol]ir.Expr) ir.Expr {
	switch x := e.(type) {
	case nil:
		return nil
	case *ir.Ident:
		if x.Sym != nil {
			if access, ok := captureField[x.Sym]; ok {
				return cloneExpr(access)
			}
		}
		return x
	case *ir.Binary:
		x.Left = l.rewriteExpr(x.Left, captureField)
		x.Right = l.rewriteExpr(x.Right, captureField)
		return x
	case *ir.Unary:
		x.Operand = l.rewriteExpr(x.Operand, captureField)
		return x
	case *ir.Ternary:
		x.Cond = l.rewriteExpr(x.Cond, captureField)
		x.Then = l.rewriteExpr(x.Then, captureField)
		x.Else = l.rewriteExpr(x.Else, captureField)
		return x
	case *ir.Call:
		x.Receiver = l.rewriteExpr(x.Receiver, captureField)
		for i := range x.Args {
			x.Args[i].Value = l.rewriteExpr(x.Args[i].Value, captureField)
		}
		return x
	case *ir.Conversion:
		x.Operand = l.rewriteExpr(x.Operand, captureField)
		return x
	case *ir.Select:
		x.Operand = l.rewriteExpr(x.Operand, captureField)
		return x
	case *ir.Index:
		x.Operand = l.rewriteExpr(x.Operand, captureField)
		x.Idx = l.rewriteExpr(x.Idx, captureField)
		return x
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = l.rewriteExpr(x.Elems[i], captureField)
		}
		return x
	case *ir.StructLit:
		for i := range x.Fields {
			x.Fields[i].Value = l.rewriteExpr(x.Fields[i].Value, captureField)
		}
		return x
	case *ir.Spread:
		x.Operand = l.rewriteExpr(x.Operand, captureField)
		return x
	case *ir.Lambda:
		// Nested lambda inside this body. The outer pass's walker will
		// reach it as part of normal traversal (after the outer Lift
		// returns) and call Lift on it. Do not rewrite into the lambda
		// body here — that would conflate scope frames.
		return x
	case *ir.Closure:
		// Already lifted; do not descend.
		return x
	}
	return e
}

// cloneExpr returns a deep-enough copy of e so multiple insertions of an
// access expression do not alias mutations across sites. Only the spine
// matters; leaf Symbols are shared by design.
func cloneExpr(e ir.Expr) ir.Expr {
	switch x := e.(type) {
	case *ir.Ident:
		cp := *x
		return &cp
	case *ir.Select:
		cp := *x
		cp.Operand = cloneExpr(x.Operand)
		return &cp
	case *ir.Unary:
		cp := *x
		cp.Operand = cloneExpr(x.Operand)
		return &cp
	}
	return e
}
