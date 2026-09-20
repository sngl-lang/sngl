package ir

// ZeroExpr returns a zero-value IR expression for t. Returns nil when t has no
// well-defined zero (dyn, invalid, unresolved type parameters, component types).
func ZeroExpr(t *Type) Expr {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case TypeBool:
		return &Literal{Type: t, Value: "false"}
	case TypeInt:
		return &Literal{Type: t, Value: "0"}
	case TypeFloat:
		return &Literal{Type: t, Value: "0.0"}
	case TypeString:
		return &Literal{Type: t, Value: ""}
	case TypeNull:
		return &Literal{Type: TypNull, Value: "null"}
	case TypeOption:
		return &Literal{Type: TypNull, Value: "null"}
	case TypeList:
		return &ListLit{Type: t, Elems: nil}
	case TypeRemote:
		// The idle remote: no value, no failure, nothing in flight. An empty
		// composite of the type, the way a struct's zero is — every host's zero
		// three-field record already reads that way, so no backend has to
		// special-case it.
		//
		// Having one is what keeps a remote total. Reading `.value()` off a
		// remote nothing has fetched answers null rather than faulting, which is
		// the point of returning the box instead of the bare T: `option<T>`
		// carries the absence, so no accessor has a case with no answer.
		//
		// Note this is a zero of remote<T>, not a zero of T. The bug in the
		// current async lowering is that it seeds with ZeroExpr(T) — a fetched
		// empty list and a fetch still in flight are then the same []. A zero
		// remote is distinguishable from a fetched anything, because `value()`
		// is null and `inFlight()` is false.
		return &StructLit{Type: t, Fields: nil}
	case TypeStruct:
		// date/time/datetime are string-representable stdlib structs (like
		// color); their zero value is a canonical-form string literal rather
		// than an empty struct literal.
		switch {
		case IsDateStruct(t):
			return &Literal{Type: t, Value: "0001-01-01"}
		case IsTimeStruct(t):
			return &Literal{Type: t, Value: "00:00:00"}
		case IsDateTimeStruct(t):
			return &Literal{Type: t, Value: "0001-01-01 00:00:00"}
		}
		sd, _ := t.Decl.(*StructDef)
		return &StructLit{Type: t, Def: sd, Fields: nil}
	case TypeEnum:
		ed, _ := t.Decl.(*EnumDef)
		if ed == nil || len(ed.Members) == 0 {
			return nil
		}
		return &Ident{Type: t, Name: ed.Members[0].Name, Member: ed.Members[0].Name, Sym: ed}
	case TypeUnit:
		ud, _ := t.Decl.(*UnitDef)
		suffix := ""
		if ud != nil {
			for _, s := range ud.Suffixes {
				if s.IsBase() {
					suffix = s.Name
					break
				}
			}
		}
		return &Literal{Type: t, Value: "0", Suffix: suffix}
	case TypeFunc:
		return zeroFuncExpr(t)
	}
	return nil
}

// DeclaredDefault is the value a declaration means by omitting one.
//
// The same as ZeroExpr except for a struct, where it carries the fields' own
// declared defaults. An empty struct literal reads as every field's zero, and
// a declaration that says `strokeWidth float = 1.0` and `fontSize float = 16.0`
// means those -- so a platform override reading a prop the call site left out
// gets what the stdlib documents rather than a transparent hairline in a
// zero-point font.
//
// Only the fields the declaration actually gave a default, which is what
// DefaultWritten is for: Normalize fills every nil Default with the type's
// zero, so reading Default alone names every field of every struct. That
// difference is not academic -- a backend reads a field's *presence* as
// having been set, and `ui.Style` declares no defaults at all while
// documenting that an unset field inherits the platform's. Naming them all
// put `flex:0` and a zero-line `-webkit-line-clamp` on every element of every
// generated page.
func DeclaredDefault(t *Type) Expr {
	if t == nil || t.Kind != TypeStruct {
		return ZeroExpr(t)
	}
	// A string-representable stdlib struct has a canonical zero of its own.
	if IsDateStruct(t) || IsTimeStruct(t) || IsDateTimeStruct(t) {
		return ZeroExpr(t)
	}
	sd, _ := t.Decl.(*StructDef)
	if sd == nil || len(sd.Fields) == 0 {
		return ZeroExpr(t)
	}
	lit := &StructLit{Type: t, Def: sd}
	for _, f := range sd.Fields {
		if !f.DefaultWritten || f.Default == nil {
			continue
		}
		lit.Fields = append(lit.Fields, FieldInit{Name: f.Name, Value: f.Default})
	}
	return lit
}

// zeroFuncExpr synthesizes a dummy lambda matching t's function signature: it
// ignores every parameter and returns the zero of the declared return type.
func zeroFuncExpr(t *Type) Expr {
	if t.Sig == nil {
		return nil
	}
	params := make([]*Param, len(t.Sig.Params))
	for i, p := range t.Sig.Params {
		var pt *Type
		if p != nil {
			pt = p.Type
		}
		params[i] = &Param{Name: "_", Type: pt}
	}
	fn := &Func{
		Params:     params,
		Return:     t.Sig.Return,
		TypeParams: t.Sig.TypeParams,
		Purity:     t.Sig.Purity,
	}
	if t.Sig.Return != nil && t.Sig.Return.Kind != TypeDyn && t.Sig.Return.Kind != TypeInvalid {
		if z := ZeroExpr(t.Sig.Return); z != nil {
			fn.Block = []Stmt{&Return{Value: z}}
		} else {
			fn.Block = []Stmt{&Return{}}
		}
	}
	return &Lambda{Type: t, Func: fn}
}

// Normalize walks pkg and populates missing zero values so codegen never sees
// nil Init/Default and every non-void function terminates with an ir.Return.
func Normalize(pkg *Package) {
	if pkg == nil {
		return
	}
	for _, s := range pkg.Structs {
		normalizeStructDef(s)
	}
	for _, v := range pkg.Consts {
		normalizeVar(v)
	}
	for _, v := range pkg.Vars {
		normalizeVar(v)
	}
	for _, f := range pkg.Funcs {
		normalizeFunc(f)
	}
	for _, c := range pkg.Components {
		normalizeComponent(c)
	}
	for _, w := range pkg.Windows {
		normalizeWindow(w)
	}
}

func normalizeStructDef(s *StructDef) {
	if s == nil {
		return
	}
	for _, f := range s.Fields {
		if f.Default == nil {
			f.Default = ZeroExpr(f.Type)
		} else {
			f.DefaultWritten = true
			normalizeExpr(f.Default)
		}
	}
}

func normalizeVar(v *Var) {
	if v == nil {
		return
	}
	// A const that *is* a host identifier has no initializer and must not be
	// given one: the value lives on the host, and a zero here is a number the
	// program never wrote. `math.Pi` normalized to 0.0 and every read of it
	// folded to that, on every target, with nothing to say so.
	if IsHostValue(v) {
		return
	}
	if v.Init == nil {
		v.Init = ZeroExpr(v.Type)
	} else {
		normalizeExpr(v.Init)
	}
	for _, h := range v.Handlers {
		if h != nil && h.Func != nil {
			normalizeFunc(h.Func)
		}
	}
}

func normalizeFunc(f *Func) {
	if f == nil || f.Intrinsic != "" {
		return
	}
	f.Block = normalizeStmts(f.Block)
	if f.Return == nil {
		return
	}
	if f.Return.Kind == TypeInvalid || f.Return.Kind == TypeDyn {
		return
	}
	if len(f.Block) > 0 {
		if _, ok := f.Block[len(f.Block)-1].(*Return); ok {
			return
		}
	}
	if z := ZeroExpr(f.Return); z != nil {
		f.Block = append(f.Block, &Return{Value: z})
	}
}

func normalizeComponent(c *Component) {
	if c == nil {
		return
	}
	for _, p := range c.Props {
		if p.Default == nil {
			p.Default = ZeroExpr(p.Type)
		} else {
			normalizeExpr(p.Default)
		}
	}
	for _, v := range c.Vars {
		normalizeVar(v)
	}
	for _, f := range c.Funcs {
		normalizeFunc(f)
	}
	c.Body = normalizeStmts(c.Body)
}

func normalizeWindow(w *Window) {
	if w == nil {
		return
	}
	for _, p := range w.Props {
		if p.Value != nil {
			normalizeExpr(p.Value)
		}
	}
	w.Body = normalizeStmts(w.Body)
}

func normalizeStmts(stmts []Stmt) []Stmt {
	for i := range stmts {
		stmts[i] = normalizeStmt(stmts[i])
	}
	return stmts
}

func normalizeStmt(s Stmt) Stmt {
	switch n := s.(type) {
	case *LocalVar:
		if n.Init == nil {
			n.Init = ZeroExpr(n.Type)
		} else {
			normalizeExpr(n.Init)
		}
	case *Assign:
		normalizeExpr(n.Target)
		normalizeExpr(n.Value)
	case *Toggle:
		normalizeExpr(n.Target)
	case *Return:
		if n.Value != nil {
			normalizeExpr(n.Value)
		}
	case *If:
		normalizeExpr(n.Cond)
		n.Body = normalizeStmts(n.Body)
		n.Else = normalizeStmts(n.Else)
	case *For:
		normalizeExpr(n.Iter)
		n.Body = normalizeStmts(n.Body)
		n.Else = normalizeStmts(n.Else)
	case *NodeInst:
		for i := range n.Props {
			normalizeExpr(n.Props[i].Value)
		}
		for i := range n.Handlers {
			if n.Handlers[i].Func != nil {
				normalizeFunc(n.Handlers[i].Func)
			}
		}
		n.Children = normalizeStmts(n.Children)
		if n.Key != nil {
			normalizeExpr(n.Key)
		}
		if n.Ref != nil {
			normalizeExpr(n.Ref)
		}
	case *SlotInst:
		n.Children = normalizeStmts(n.Children)
	case *CallStmt:
		if n.Call != nil {
			normalizeExpr(n.Call)
		}
	case *Emit:
		for i := range n.Args {
			normalizeExpr(n.Args[i].Value)
		}
	case *Window:
		normalizeWindow(n)
	}
	return s
}

func normalizeExpr(e Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *Binary:
		normalizeExpr(n.Left)
		normalizeExpr(n.Right)
	case *Unary:
		normalizeExpr(n.Operand)
	case *Ternary:
		normalizeExpr(n.Cond)
		normalizeExpr(n.Then)
		normalizeExpr(n.Else)
	case *Call:
		if n.Callee != nil {
			normalizeExpr(n.Callee)
		}
		if n.Receiver != nil {
			normalizeExpr(n.Receiver)
		}
		for i := range n.Args {
			normalizeExpr(n.Args[i].Value)
		}
	case *Conversion:
		normalizeExpr(n.Operand)
	case *Select:
		normalizeExpr(n.Operand)
	case *Index:
		normalizeExpr(n.Operand)
		normalizeExpr(n.Idx)
	case *StructLit:
		for i := range n.Fields {
			normalizeExpr(n.Fields[i].Value)
		}
	case *ListLit:
		for _, el := range n.Elems {
			normalizeExpr(el)
		}
	case *Spread:
		normalizeExpr(n.Operand)
	case *Lambda:
		if n.Func != nil {
			normalizeFunc(n.Func)
		}
	case *Closure:
		if n.Func != nil {
			normalizeFunc(n.Func)
		}
		if n.State != nil {
			normalizeExpr(n.State)
		}
	}
}
