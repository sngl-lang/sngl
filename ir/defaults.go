package ir

import "strconv"

// ZeroExpr returns a zero-value IR expression for t. Returns nil when t has no
// well-defined zero (invalid, unresolved type parameters, component types).
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
	case TypeMap:
		return &MapLitIR{Type: t}
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
		return &StructLit{Type: t, Def: sd}
	case TypeEnum:
		ed, _ := t.Decl.(*EnumDef)
		if ed == nil || len(ed.Members) == 0 {
			return nil
		}
		// A member carrying a payload is zero with its payload's zero.
		if first := ed.Members[0]; first.Payload != nil {
			payload := ZeroExpr(MemberPayload(t, first.Name))
			if payload == nil {
				return nil
			}
			return &StructLit{Type: t, Fields: []FieldInit{{Name: first.Name, Value: payload}}}
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

// FieldZero is what a literal of t holds in a field f it leaves out: the
// field's declared default, or its type's zero at t's type arguments. nil
// where neither is known: a field typed by a type parameter nothing binds.
func FieldZero(t *Type, f *StructField) Expr {
	if f.Default != nil && (f.DefaultWritten || f.Type == nil || !f.Type.HasTypeParam()) {
		return f.Default
	}
	ft := f.Type
	if bound, ok := FieldsOf(t); ok {
		if b := FieldNamed(bound, f.Name); b != nil {
			ft = b.Type
		}
	}
	// One null for every option a literal leaves out, and one empty string:
	// a library struct is not normalized, so its fields carry no zero of
	// their own, and a fresh one per read of a Style's fields was a tenth of
	// what the docs site allocated. A literal is a leaf nothing writes
	// (SharedLiteral).
	switch {
	case ft != nil && ft.Kind == TypeOption:
		return sharedNull
	case ft == TypString:
		return sharedEmpty
	}
	return ZeroExpr(ft)
}

var (
	sharedNull  = &Literal{Type: TypNull, Value: "null"}
	sharedEmpty = &Literal{Type: TypString, Value: ""}
)

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
	if t.Sig.Return != nil && t.Sig.Return.Kind != TypeInvalid {
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
	pkg.Body = normalizeStmts(pkg.Body)
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
}

func normalizeFunc(f *Func) {
	if f == nil || f.Intrinsic != "" {
		return
	}
	f.Block = normalizeStmts(f.Block)
	if f.Return == nil {
		return
	}
	if f.Return.Kind == TypeInvalid {
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
	c.Stmts = normalizeStmts(c.Stmts)
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
	case *Match:
		normalizeExpr(n.Subject)
		for _, arm := range n.Arms {
			for _, p := range arm.Patterns {
				normalizeExpr(p)
			}
			arm.Body = normalizeStmts(arm.Body)
		}
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
	case *MatchExpr:
		normalizeExpr(n.Subject)
		for _, arm := range n.Arms {
			for _, p := range arm.Patterns {
				normalizeExpr(p)
			}
			normalizeExpr(arm.Value)
		}
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
	}
}

// SameConstant reports whether a and b are one constant written alike: the
// same literal, both null, the same enum member, or struct literals of one
// declaration holding such constants field for field. Anything else is not
// known to be the same, and answers false.
func SameConstant(a, b Expr) bool {
	if a == b {
		return a != nil
	}
	if IsNullLiteral(a) || IsNullLiteral(b) {
		return IsNullLiteral(a) && IsNullLiteral(b)
	}
	switch x := a.(type) {
	case *Literal:
		y, ok := b.(*Literal)
		return ok && x.Value == y.Value && x.Suffix == y.Suffix && x.Type != nil && x.Type.Equal(y.Type)
	case *Ident:
		y, ok := b.(*Ident)
		return ok && x.Member != "" && x.Member == y.Member && x.Type != nil && x.Type.Equal(y.Type)
	case *Conversion:
		y, ok := b.(*Conversion)
		return ok && x.Type != nil && x.Type.Equal(y.Type) && SameConstant(x.Operand, y.Operand)
	case *StructLit:
		y, ok := b.(*StructLit)
		if !ok || x.Def == nil || x.Def != y.Def || !x.declared() {
			return false
		}
		for _, d := range x.Def.Fields {
			if !SameConstant(x.Field(d.Name), y.Field(d.Name)) {
				return false
			}
		}
		return true
	}
	return false
}

// IsZeroValue reports whether e is written as its type's zero (ZeroKnown),
// false where that is not known.
func IsZeroValue(e Expr) bool {
	zero, known := ZeroKnown(e)
	return zero && known
}

// ZeroKnown reports whether e holds its type's zero -- null, an empty string,
// list or map, false, a zero number or unit, an enum's first member, or a
// struct literal each of whose fields holds what it would left out
// (FieldZero) -- and whether e is a constant written plainly enough to say:
// what a spread decides by, since it writes a field only where the field is
// not at its zero. Asked without building the zero to compare against.
func ZeroKnown(e Expr) (zero, known bool) {
	if IsNullLiteral(e) {
		return true, true
	}
	switch x := e.(type) {
	case *Literal:
		if x.Type == nil {
			return false, false
		}
		switch x.Type.Kind {
		case TypeString:
			return x.Value == "", true
		case TypeBool:
			return x.Value == "false", true
		case TypeInt, TypeFloat, TypeUnit:
			n, err := strconv.ParseFloat(x.Value, 64)
			return err == nil && n == 0, err == nil
		}
	case *Ident:
		if x.Member == "" || x.Type == nil || x.Type.Kind != TypeEnum {
			return false, false
		}
		ed, _ := x.Type.Decl.(*EnumDef)
		if ed == nil || len(ed.Members) == 0 {
			return false, false
		}
		return ed.Members[0].Payload == nil && ed.Members[0].Name == x.Member, true
	case *Conversion:
		// A value put into an option is a present option, whatever it holds.
		if IsOptionWrap(x) {
			return false, true
		}
	case *ListLit:
		if len(x.Elems) == 0 {
			return true, true
		}
		return false, true
	case *MapLitIR:
		if len(x.Entries) == 0 {
			return true, true
		}
		return false, true
	case *StructLit:
		if !x.declared() || IsDateStruct(x.Type) || IsTimeStruct(x.Type) || IsDateTimeStruct(x.Type) {
			return false, false
		}
		zero := true
		for _, f := range x.Fields {
			if f.Spread {
				return false, false
			}
			d := FieldNamed(x.Def.Fields, f.Name)
			if d == nil {
				return false, false
			}
			if SameConstant(f.Value, FieldZero(x.Type, d)) {
				continue
			}
			if d.DefaultWritten {
				// Not the default, and whether it differs is not known.
				if _, k := ZeroKnown(f.Value); !k {
					return false, false
				}
				zero = false
				continue
			}
			z, k := ZeroKnown(f.Value)
			if !k {
				return false, false
			}
			zero = zero && z
		}
		return zero, true
	}
	return false, false
}

// SharedLiteral reports whether a copy of lit may be lit itself: a literal is
// a leaf nothing writes in place, so an expression cloned for another place
// can hold the one it was cloned from. Not one typed by a type parameter,
// which an instantiation substitutes into its own copy. Copying each literal
// per clone was most of what a spliced or unrolled body cost.
func SharedLiteral(lit *Literal) bool {
	return lit.Type == nil || !lit.Type.HasTypeParam()
}
