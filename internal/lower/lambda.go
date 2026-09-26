package lower

import (
	"fmt"
	"slices"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/names"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passLambda = pass{
	name:    "NoLambda",
	enabled: func(c Features) bool { return !c.Lambda },
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
		case *ir.Var, *ir.Param, *ir.LoopVar:
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
			// The inner lambda's free variables (other than its own params)
			// are also captures of this outer body. Descend with the inner's
			// params added to paramSet so they don't get treated as captures
			// of the outer.
			saved := paramSet
			inner := make(map[ir.Symbol]bool, len(saved)+len(x.Func.Params))
			for s := range saved {
				inner[s] = true
			}
			for _, p := range x.Func.Params {
				inner[p] = true
			}
			paramSet = inner
			walkStmts(x.Func.Block)
			paramSet = saved
		case *ir.Closure:
			// Already-lifted; do not descend.
			return
		case *ir.MapLitIR:
			for _, en := range x.Entries {
				walkExpr(en.Key)
				walkExpr(en.Value)
			}
		case *ir.Literal, *ir.ContextRead:
			// Terminal — no captures hidden inside.
		default:
			panic(fmt.Sprintf("analyzeCaptures.walkExpr: unhandled %T", x))
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
		case *ir.ContextProvider:
			walkExpr(n.Value)
			walkStmts(n.Children)
		case *ir.Break, *ir.Continue:
			// A loop escape has nothing to capture.
		case *ir.CanvasRedrawStmt:
			// Carries a NodeInst and the draw Func, both owned elsewhere --
			// a reference, like a Call's callee, so the body it names is not
			// this body and what it reads is not this body's capture.
		default:
			panic(fmt.Sprintf("analyzeCaptures.walkStmt: unhandled %T", n))
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
// structs. Walks every *ir.Lambda in the package and replaces it with an
// *ir.Closure produced by lifter.Lift. Asserts no Lambda survives the pass.
func lowerLambda(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	if pkg.LiftedCaptures == nil {
		pkg.LiftedCaptures = map[*ir.Func]map[ir.Symbol]string{}
	}

	l := newLifter(pkg)

	rewrite := func(e ir.Expr) ir.Expr {
		return liftLambdas(e, l)
	}

	walkPackage(pkg, walkFuncs{
		expr: rewrite,
		stmts: func(stmts []ir.Stmt) []ir.Stmt {
			return liftLambdasInStmts(stmts, l)
		},
	})

	if err := assertNoLambdaSurvives(pkg); err != nil {
		return err
	}
	return nil
}

// liftLambdas replaces every *ir.Lambda found in e (recursively) with a
// *ir.Closure produced by l.Lift.
func liftLambdas(e ir.Expr, l *lifter) ir.Expr {
	switch x := e.(type) {
	case nil:
		return nil
	case *ir.Lambda:
		// Lift directly. l.Lift pushes its frame and rewrites the body via
		// rewriteStmts/rewriteExpr — those encounter nested *ir.Lambda nodes
		// and lift them inline (with the outer frame on the stack), so that
		// nested closures can reuse the outer's already-ref-typed capture
		// fields.
		return l.Lift(x.Func.Block, x.Func.Params, x.Func.Return, x.AST)
	case *ir.Binary:
		x.Left = liftLambdas(x.Left, l)
		x.Right = liftLambdas(x.Right, l)
		return x
	case *ir.Unary:
		x.Operand = liftLambdas(x.Operand, l)
		return x
	case *ir.Ternary:
		x.Cond = liftLambdas(x.Cond, l)
		x.Then = liftLambdas(x.Then, l)
		x.Else = liftLambdas(x.Else, l)
		return x
	case *ir.Call:
		x.Receiver = liftLambdas(x.Receiver, l)
		for i := range x.Args {
			x.Args[i].Value = liftLambdas(x.Args[i].Value, l)
		}
		return x
	case *ir.Conversion:
		x.Operand = liftLambdas(x.Operand, l)
		return x
	case *ir.Select:
		x.Operand = liftLambdas(x.Operand, l)
		return x
	case *ir.Index:
		x.Operand = liftLambdas(x.Operand, l)
		x.Idx = liftLambdas(x.Idx, l)
		return x
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = liftLambdas(x.Elems[i], l)
		}
		return x
	case *ir.StructLit:
		for i := range x.Fields {
			x.Fields[i].Value = liftLambdas(x.Fields[i].Value, l)
		}
		return x
	case *ir.Spread:
		x.Operand = liftLambdas(x.Operand, l)
		return x
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = liftLambdas(x.Entries[i].Key, l)
			x.Entries[i].Value = liftLambdas(x.Entries[i].Value, l)
		}
		return x
	case *ir.Literal, *ir.Ident, *ir.ContextRead, *ir.Closure:
		// Terminal / already-lifted — no nested *ir.Lambda.
		return x
	default:
		panic(fmt.Sprintf("liftLambdas: unhandled %T", x))
	}
}

func liftLambdasInStmts(stmts []ir.Stmt, l *lifter) []ir.Stmt {
	for _, s := range stmts {
		liftLambdasInStmt(s, l)
	}
	return stmts
}

func liftLambdasInStmt(s ir.Stmt, l *lifter) {
	switch n := s.(type) {
	case *ir.Assign:
		n.Target = liftLambdas(n.Target, l)
		n.Value = liftLambdas(n.Value, l)
	case *ir.LocalVar:
		n.Init = liftLambdas(n.Init, l)
	case *ir.Return:
		n.Value = liftLambdas(n.Value, l)
	case *ir.If:
		n.Cond = liftLambdas(n.Cond, l)
		liftLambdasInStmts(n.Body, l)
		liftLambdasInStmts(n.Else, l)
	case *ir.For:
		n.Iter = liftLambdas(n.Iter, l)
		liftLambdasInStmts(n.Body, l)
		liftLambdasInStmts(n.Else, l)
	case *ir.Emit:
		for i := range n.Args {
			n.Args[i].Value = liftLambdas(n.Args[i].Value, l)
		}
	case *ir.CallStmt:
		if n.Call != nil {
			n.Call = liftLambdas(n.Call, l).(*ir.Call)
		}
	case *ir.Toggle:
		n.Target = liftLambdas(n.Target, l)
	case *ir.NodeInst:
		for i := range n.Props {
			n.Props[i].Value = liftLambdas(n.Props[i].Value, l)
		}
		n.Key = liftLambdas(n.Key, l)
		n.Ref = liftLambdas(n.Ref, l)
		liftLambdasInStmts(n.Children, l)
		for i := range n.Handlers {
			if n.Handlers[i].Func != nil {
				liftLambdasInStmts(n.Handlers[i].Func.Block, l)
			}
		}
	case *ir.SlotInst:
		liftLambdasInStmts(n.Children, l)
	case *ir.ErrorBoundary:
		liftLambdasInStmts(n.Children, l)
		if n.Handler != nil && n.Handler.Func != nil {
			liftLambdasInStmts(n.Handler.Func.Block, l)
		}
	case *ir.ContextProvider:
		n.Value = liftLambdas(n.Value, l)
		liftLambdasInStmts(n.Children, l)
	case *ir.Break, *ir.Continue:
		// A loop escape holds no lambda to lift.
	default:
		panic(fmt.Sprintf("liftLambdasInStmt: unhandled %T", n))
	}
}

// assertNoLambdaSurvives walks pkg verifying that no *ir.Lambda remains.
// Cheap (one extra walk); failure here means the pass missed a position.
func assertNoLambdaSurvives(pkg *ir.Package) error {
	var found bool
	walkPackage(pkg, walkFuncs{
		expr: func(e ir.Expr) ir.Expr {
			if _, ok := e.(*ir.Lambda); ok {
				found = true
			}
			return e
		},
	})
	if found {
		return fmt.Errorf("lower: NoLambda invariant violated — *ir.Lambda survived the pass")
	}
	return nil
}

// lifter owns NoLambda's package-scoped state. The same lifter is shared
// between the main pass and NoDeclarative's handler-promote step.
type lifter struct {
	pkg     *ir.Package
	counter int
	names   *names.Registry
	// enclosing tracks ancestor lift frames so a nested lambda capturing
	// the same Sym as its enclosing lambda re-uses the outer's ref-typed
	// state field instead of taking a fresh address.
	enclosing []scopeFrame
}

// newLifter seeds the name registry from the package as it stands. Seeding
// once is enough because a lifter lives for one pass and is the only thing
// adding to pkg.Structs or pkg.Funcs within it.
func newLifter(pkg *ir.Package) *lifter {
	l := &lifter{pkg: pkg, names: &names.Registry{}}
	for _, s := range pkg.Structs {
		l.names.Reserve(s.Name)
	}
	for _, f := range pkg.Funcs {
		l.names.Reserve(f.Name)
	}
	return l
}

type scopeFrame struct {
	// captureField maps a captured outer Symbol to the bare Select expression
	// reaching it from this frame's lifted body: Select{Ident{state},
	// fieldName, type=ref<T>} for mutable captures, or
	// Select{Ident{state}, fieldName, type=T} for read-only captures.
	// Reads/writes inside the body wrap mutable captures in Unary{Deref};
	// inner-lambda caps-init reads the bare Select so the ref<T> threads
	// through unchanged.
	captureField map[ir.Symbol]ir.Expr
	// captureMutable[sym] is true iff the captured field is ref<T>.
	captureMutable map[ir.Symbol]bool
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

	// Build the rewrite maps. captureField stores the bare Select for each
	// captured Sym (typed ref<T> if the body mutates it, T otherwise).
	// captureMutable flags which captures are ref-typed so rewriteExpr knows
	// to wrap with Unary{Deref} on reads.
	captureField := make(map[ir.Symbol]ir.Expr, len(caps))
	captureMutable := make(map[ir.Symbol]bool, len(caps))
	for _, c := range caps {
		fieldType := c.Sym.SymType()
		if c.Mutable {
			fieldType = ir.RefOf(c.Sym.SymType())
		}
		sel := &ir.Select{
			Operand: &ir.Ident{Name: "state", Sym: stateParam, Type: capsType},
			Field:   c.Sym.SymName(),
			Type:    fieldType,
		}
		captureField[c.Sym] = sel
		captureMutable[c.Sym] = c.Mutable
	}

	// Push frame and rewrite body. Nested *ir.Lambda nodes encountered during
	// rewrite are lifted inline by rewriteExpr — they see this frame on the
	// stack and reuse its capture access expressions.
	l.enclosing = append(l.enclosing, scopeFrame{
		captureField:   captureField,
		captureMutable: captureMutable,
	})
	lifted.Block = l.rewriteStmts(body, captureField, captureMutable)
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
				if v, ok := c.Sym.(*ir.Var); ok {
					if l.pkg.AddressedVars == nil {
						l.pkg.AddressedVars = map[*ir.Var]bool{}
					}
					l.pkg.AddressedVars[v] = true
				}
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
	for _, frame := range slices.Backward(l.enclosing) {
		if access, ok := frame.captureField[sym]; ok {
			return access
		}
	}
	return nil
}

// freshNames returns (capsName, funcName) skipping past any existing names
// in pkg.Structs / pkg.Funcs.
//
// It asks the registry rather than allocating from it, because the two names
// have to carry the same N: taken one at a time they would drift apart under
// the first collision and `__lambda3_caps` would belong to `__lambda4`.
func (l *lifter) freshNames() (string, string) {
	for {
		capsName := "__lambda" + itoa(l.counter) + "_caps"
		funcName := "__lambda" + itoa(l.counter)
		l.counter++
		if l.names.Free(capsName) && l.names.Free(funcName) {
			l.names.Reserve(capsName, funcName)
			return capsName, funcName
		}
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// rewriteStmts replaces every Ident.Sym appearing in stmts that is in
// captureField with the field's access expression (wrapping mutable
// captures with Unary{Deref}). Nested *ir.Lambda nodes are lifted inline
// via l.Lift; the active frame stays on l.enclosing while the inner runs
// so the inner's caps-init can pull access expressions through
// outerCaptureAccess.
func (l *lifter) rewriteStmts(stmts []ir.Stmt, captureField map[ir.Symbol]ir.Expr, captureMutable map[ir.Symbol]bool) []ir.Stmt {
	for _, s := range stmts {
		l.rewriteStmt(s, captureField, captureMutable)
	}
	return stmts
}

func (l *lifter) rewriteStmt(s ir.Stmt, captureField map[ir.Symbol]ir.Expr, captureMutable map[ir.Symbol]bool) {
	switch n := s.(type) {
	case *ir.Assign:
		n.Target = l.rewriteExpr(n.Target, captureField, captureMutable)
		n.Value = l.rewriteExpr(n.Value, captureField, captureMutable)
	case *ir.LocalVar:
		n.Init = l.rewriteExpr(n.Init, captureField, captureMutable)
	case *ir.Return:
		n.Value = l.rewriteExpr(n.Value, captureField, captureMutable)
	case *ir.If:
		n.Cond = l.rewriteExpr(n.Cond, captureField, captureMutable)
		l.rewriteStmts(n.Body, captureField, captureMutable)
		l.rewriteStmts(n.Else, captureField, captureMutable)
	case *ir.For:
		n.Iter = l.rewriteExpr(n.Iter, captureField, captureMutable)
		l.rewriteStmts(n.Body, captureField, captureMutable)
		l.rewriteStmts(n.Else, captureField, captureMutable)
	case *ir.Emit:
		for i := range n.Args {
			n.Args[i].Value = l.rewriteExpr(n.Args[i].Value, captureField, captureMutable)
		}
	case *ir.CallStmt:
		if n.Call != nil {
			n.Call = l.rewriteExpr(n.Call, captureField, captureMutable).(*ir.Call)
		}
	case *ir.Toggle:
		n.Target = l.rewriteExpr(n.Target, captureField, captureMutable)
	case *ir.NodeInst:
		for i := range n.Props {
			n.Props[i].Value = l.rewriteExpr(n.Props[i].Value, captureField, captureMutable)
		}
		n.Key = l.rewriteExpr(n.Key, captureField, captureMutable)
		n.Ref = l.rewriteExpr(n.Ref, captureField, captureMutable)
		l.rewriteStmts(n.Children, captureField, captureMutable)
		for i := range n.Handlers {
			if n.Handlers[i].Func != nil {
				l.rewriteStmts(n.Handlers[i].Func.Block, captureField, captureMutable)
			}
		}
	case *ir.ContextProvider:
		n.Value = l.rewriteExpr(n.Value, captureField, captureMutable)
		l.rewriteStmts(n.Children, captureField, captureMutable)
	case *ir.Break, *ir.Continue:
		// A loop escape holds no ident to rewrite.
	default:
		panic(fmt.Sprintf("lifter.rewriteStmt: unhandled %T", n))
	}
}

func (l *lifter) rewriteExpr(e ir.Expr, captureField map[ir.Symbol]ir.Expr, captureMutable map[ir.Symbol]bool) ir.Expr {
	switch x := e.(type) {
	case nil:
		return nil
	case *ir.Ident:
		if x.Sym != nil {
			if access, ok := captureField[x.Sym]; ok {
				cloned := cloneExpr(access)
				if captureMutable[x.Sym] {
					cloned = &ir.Unary{
						Op:      ast.UnaryDeref,
						Operand: cloned,
						Type:    x.Sym.SymType(),
					}
				}
				return cloned
			}
		}
		return x
	case *ir.Binary:
		x.Left = l.rewriteExpr(x.Left, captureField, captureMutable)
		x.Right = l.rewriteExpr(x.Right, captureField, captureMutable)
		return x
	case *ir.Unary:
		x.Operand = l.rewriteExpr(x.Operand, captureField, captureMutable)
		return x
	case *ir.Ternary:
		x.Cond = l.rewriteExpr(x.Cond, captureField, captureMutable)
		x.Then = l.rewriteExpr(x.Then, captureField, captureMutable)
		x.Else = l.rewriteExpr(x.Else, captureField, captureMutable)
		return x
	case *ir.Call:
		x.Receiver = l.rewriteExpr(x.Receiver, captureField, captureMutable)
		for i := range x.Args {
			x.Args[i].Value = l.rewriteExpr(x.Args[i].Value, captureField, captureMutable)
		}
		return x
	case *ir.Conversion:
		x.Operand = l.rewriteExpr(x.Operand, captureField, captureMutable)
		return x
	case *ir.Select:
		x.Operand = l.rewriteExpr(x.Operand, captureField, captureMutable)
		return x
	case *ir.Index:
		x.Operand = l.rewriteExpr(x.Operand, captureField, captureMutable)
		x.Idx = l.rewriteExpr(x.Idx, captureField, captureMutable)
		return x
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = l.rewriteExpr(x.Elems[i], captureField, captureMutable)
		}
		return x
	case *ir.StructLit:
		for i := range x.Fields {
			x.Fields[i].Value = l.rewriteExpr(x.Fields[i].Value, captureField, captureMutable)
		}
		return x
	case *ir.Spread:
		x.Operand = l.rewriteExpr(x.Operand, captureField, captureMutable)
		return x
	case *ir.Lambda:
		// Inner lambda. Lift inline so its caps-init runs while the enclosing
		// frame is still on l.enclosing; outerCaptureAccess can then return
		// the outer's bare Select for any Sym both lambdas capture.
		return l.Lift(x.Func.Block, x.Func.Params, x.Func.Return, x.AST)
	case *ir.Closure:
		// Already lifted; do not descend.
		return x
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = l.rewriteExpr(x.Entries[i].Key, captureField, captureMutable)
			x.Entries[i].Value = l.rewriteExpr(x.Entries[i].Value, captureField, captureMutable)
		}
		return x
	case *ir.Literal, *ir.ContextRead:
		// Terminal — no Idents to rewrite.
		return x
	default:
		panic(fmt.Sprintf("lifter.rewriteExpr: unhandled %T", x))
	}
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
