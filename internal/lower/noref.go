package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passNoRef = pass{
	name:    "NoRef",
	enabled: func(c Caps) bool { return c.NoRef },
	apply:   lowerNoRef,
}

// lowerNoRef boxes every addressed binding into a synthesized one-field
// reference-semantic struct. After this pass no *ir.TypeRef remains.
//
// The pass runs in three steps:
//
//  1. seedAddressedVars walks every Expr/Stmt and records any *ir.Var whose
//     address is taken (via *ir.Unary{UnaryAddr}). Entries already populated
//     by NoLambda's lifter persist.
//  2. Each addressed Var has its declared type rewritten from T to a
//     synthesized __ref_T struct, and its initializer wrapped in a StructLit
//     containing the original init under field "value".
//  3. Every Expr and Stmt is rewritten via refRewriter: &v collapses to
//     plain v; *p becomes p.value; reads of an addressed Var route through
//     a Select on .value; ref<T> types appearing in struct fields and func
//     params/returns are replaced with the corresponding box-struct type.
func lowerNoRef(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	seedAddressedVars(pkg)

	reg := newBoxRegistry(pkg)
	rw := &refRewriter{pkg: pkg, reg: reg}

	// Step 1: rewrite addressed Var declarations and their initializers.
	for _, v := range collectAllVars(pkg) {
		if !pkg.AddressedVars[v] {
			continue
		}
		elem := v.Type
		boxDef := reg.boxFor(elem)
		boxType := &ir.Type{Kind: ir.TypeStruct, Decl: boxDef}
		oldInit := v.Init
		v.Type = boxType
		v.Init = &ir.StructLit{
			Type: boxType,
			Def:  boxDef,
			Fields: []ir.FieldInit{
				{Name: "value", Value: orNullLiteral(oldInit, elem)},
			},
		}
	}

	// Step 1.5: rewrite Var.Type for ALL Vars, not just addressed ones.
	// Catches non-addressed Vars whose declared type is ref<T> (e.g.,
	// `var p ref<int> = &n` — p is not itself addressed but holds a ref),
	// and composite types like `list<ref<int>>`. rewriteType is idempotent,
	// so this is a no-op for Vars whose Type contains no TypeRef.
	for _, v := range collectAllVars(pkg) {
		v.Type = rw.rewriteType(v.Type)
	}

	// Step 2: rewrite every Expr and Stmt — reads, writes, & and *, ref<T> types.
	walkPackage(pkg, walkFuncs{
		expr:  func(e ir.Expr) ir.Expr { return rw.rewriteExpr(e) },
		stmts: func(stmts []ir.Stmt) []ir.Stmt { rw.rewriteStmts(stmts); return stmts },
	})

	// Step 3: rewrite ref<T> appearances in struct fields and func params/returns.
	for _, s := range pkg.Structs {
		for _, f := range s.Fields {
			f.Type = rw.rewriteType(f.Type)
		}
	}
	for _, f := range pkg.Funcs {
		rewriteFuncSignature(f, rw)
	}
	for _, comp := range pkg.Components {
		for _, f := range comp.Funcs {
			rewriteFuncSignature(f, rw)
		}
	}
	for _, w := range pkg.Windows {
		for _, f := range w.Funcs {
			rewriteFuncSignature(f, rw)
		}
	}
	if err := assertNoRefSurvives(pkg); err != nil {
		return err
	}
	return nil
}

// assertNoRefSurvives walks every type position in pkg and reports an error
// if any *ir.Type with Kind == TypeRef remains. Run as a post-pass invariant
// of lowerNoRef so a future change that misses a position fails loudly rather
// than producing silently-broken IR.
func assertNoRefSurvives(pkg *ir.Package) error {
	var found bool
	var check func(t *ir.Type)
	check = func(t *ir.Type) {
		if t == nil || found {
			return
		}
		if t.Kind == ir.TypeRef {
			found = true
			return
		}
		for _, e := range t.Elems {
			check(e)
		}
	}
	walkPackage(pkg, walkFuncs{
		expr: func(e ir.Expr) ir.Expr {
			if e != nil {
				check(e.ExprType())
			}
			return e
		},
	})
	for _, s := range pkg.Structs {
		for _, f := range s.Fields {
			check(f.Type)
		}
	}
	for _, f := range pkg.Funcs {
		for _, p := range f.Params {
			check(p.Type)
		}
		check(f.Return)
	}
	for _, c := range pkg.Components {
		for _, f := range c.Funcs {
			for _, p := range f.Params {
				check(p.Type)
			}
			check(f.Return)
		}
	}
	for _, w := range pkg.Windows {
		for _, f := range w.Funcs {
			for _, p := range f.Params {
				check(p.Type)
			}
			check(f.Return)
		}
	}
	if found {
		return fmt.Errorf("lower: NoRef invariant violated — TypeRef survived the pass")
	}
	return nil
}

// rewriteFuncSignature rewrites every Param's Type and the Return type of f
// through rw, replacing any ref<T> shape with the corresponding box struct.
func rewriteFuncSignature(f *ir.Func, rw *refRewriter) {
	if f == nil {
		return
	}
	for _, p := range f.Params {
		p.Type = rw.rewriteType(p.Type)
	}
	f.Return = rw.rewriteType(f.Return)
}

// orNullLiteral returns init if non-nil, else a null literal of elem's type.
func orNullLiteral(init ir.Expr, elem *ir.Type) ir.Expr {
	if init != nil {
		return init
	}
	return &ir.Literal{Type: elem, Value: "null"}
}

// collectAllVars returns every *ir.Var declared in pkg (top-level, components,
// windows). Local vars inside func bodies are handled by the rewriteStmt walker.
func collectAllVars(pkg *ir.Package) []*ir.Var {
	var out []*ir.Var
	out = append(out, pkg.Vars...)
	for _, c := range pkg.Components {
		out = append(out, c.Vars...)
	}
	for _, w := range pkg.Windows {
		out = append(out, w.Vars...)
	}
	return out
}

// refRewriter rewrites every Expr/Stmt to eliminate ref<T> shapes:
//   - &v → v (the box itself is the reference).
//   - *p → p.value.
//   - read of an addressed Var → Select{ident, "value"}.
//   - ref<T> Type → corresponding __ref_T struct Type.
type refRewriter struct {
	pkg *ir.Package
	reg *boxRegistry
}

// rewriteType replaces ref<T> with the corresponding __ref_T struct type.
// Recurses into Elems so nested types (list<ref<int>>) are handled.
func (r *refRewriter) rewriteType(t *ir.Type) *ir.Type {
	if t == nil {
		return nil
	}
	if t.Kind == ir.TypeRef {
		var elem *ir.Type
		if len(t.Elems) > 0 {
			elem = r.rewriteType(t.Elems[0])
		}
		boxDef := r.reg.boxFor(elem)
		return &ir.Type{Kind: ir.TypeStruct, Decl: boxDef}
	}
	if len(t.Elems) > 0 {
		newElems := make([]*ir.Type, len(t.Elems))
		changed := false
		for i, e := range t.Elems {
			ne := r.rewriteType(e)
			newElems[i] = ne
			if ne != e {
				changed = true
			}
		}
		if !changed {
			return t
		}
		return &ir.Type{
			Kind:      t.Kind,
			Elems:     newElems,
			Decl:      t.Decl,
			Sig:       t.Sig,
			ParamName: t.ParamName,
			Package:   t.Package,
			Meta:      t.Meta,
		}
	}
	return t
}

func (r *refRewriter) rewriteExpr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Unary:
		switch x.Op {
		case ast.UnaryAddr:
			// &v becomes plain v (the box is the reference). Critically, if
			// the operand is an Ident to an addressed Var, we MUST NOT route
			// it through the Ident arm below — that would emit `v.value`,
			// which yields the boxed elem rather than the box itself.
			if id, ok := x.Operand.(*ir.Ident); ok {
				if v, isVar := id.Sym.(*ir.Var); isVar && r.pkg.AddressedVars[v] {
					return id
				}
			}
			return r.rewriteExpr(x.Operand)
		case ast.UnaryDeref:
			// *p becomes p.value.
			inner := r.rewriteExpr(x.Operand)
			return &ir.Select{
				Operand: inner,
				Field:   "value",
				Type:    x.Type,
			}
		}
		x.Operand = r.rewriteExpr(x.Operand)
		return x
	case *ir.Ident:
		if v, ok := x.Sym.(*ir.Var); ok && r.pkg.AddressedVars[v] {
			// Reads of an addressed Var go through .value. The Ident's Type
			// has already been swapped to the box type by step 1; the read
			// result is the original elem type, recoverable from the box's
			// "value" field.
			elem := v.Type
			if v.Type != nil && v.Type.Kind == ir.TypeStruct {
				if def, ok := v.Type.Decl.(*ir.StructDef); ok && len(def.Fields) > 0 {
					elem = def.Fields[0].Type
				}
			}
			return &ir.Select{
				Operand: x,
				Field:   "value",
				Type:    elem,
			}
		}
		return x
	case *ir.Binary:
		x.Left = r.rewriteExpr(x.Left)
		x.Right = r.rewriteExpr(x.Right)
		return x
	case *ir.Ternary:
		x.Cond = r.rewriteExpr(x.Cond)
		x.Then = r.rewriteExpr(x.Then)
		x.Else = r.rewriteExpr(x.Else)
		return x
	case *ir.Call:
		x.Receiver = r.rewriteExpr(x.Receiver)
		for i := range x.Args {
			x.Args[i].Value = r.rewriteExpr(x.Args[i].Value)
		}
		return x
	case *ir.Conversion:
		x.Operand = r.rewriteExpr(x.Operand)
		x.Type = r.rewriteType(x.Type)
		return x
	case *ir.Select:
		x.Operand = r.rewriteExpr(x.Operand)
		return x
	case *ir.Index:
		x.Operand = r.rewriteExpr(x.Operand)
		x.Idx = r.rewriteExpr(x.Idx)
		return x
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = r.rewriteExpr(x.Elems[i])
		}
		x.Type = r.rewriteType(x.Type)
		return x
	case *ir.StructLit:
		for i := range x.Fields {
			x.Fields[i].Value = r.rewriteExpr(x.Fields[i].Value)
		}
		if x.Type != nil && x.Type.Kind == ir.TypeRef {
			x.Type = r.rewriteType(x.Type)
		}
		return x
	case *ir.Spread:
		x.Operand = r.rewriteExpr(x.Operand)
		return x
	case *ir.Closure:
		if x.State != nil {
			for i := range x.State.Fields {
				x.State.Fields[i].Value = r.rewriteExpr(x.State.Fields[i].Value)
			}
			if x.State.Type != nil {
				x.State.Type = r.rewriteType(x.State.Type)
			}
		}
		if x.Func != nil {
			rewriteFuncSignature(x.Func, r)
			r.rewriteStmts(x.Func.Block)
		}
		if x.Type != nil {
			x.Type = r.rewriteType(x.Type)
		}
		return x
	case *ir.Lambda:
		if x.Func != nil {
			rewriteFuncSignature(x.Func, r)
			r.rewriteStmts(x.Func.Block)
		}
		return x
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = r.rewriteExpr(x.Entries[i].Key)
			x.Entries[i].Value = r.rewriteExpr(x.Entries[i].Value)
		}
		x.Type = r.rewriteType(x.Type)
		return x
	case *ir.Literal, *ir.ContextRead:
		// Terminal — no ref<T> shapes to rewrite.
		return x
	default:
		panic(fmt.Sprintf("refRewriter.rewriteExpr: unhandled %T", x))
	}
}

func (r *refRewriter) rewriteStmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		r.rewriteStmt(s)
	}
}

func (r *refRewriter) rewriteStmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.Assign:
		n.Target = r.rewriteAssignTarget(n.Target)
		n.Value = r.rewriteExpr(n.Value)
	case *ir.LocalVar:
		n.Init = r.rewriteExpr(n.Init)
		n.Type = r.rewriteType(n.Type)
	case *ir.Return:
		n.Value = r.rewriteExpr(n.Value)
	case *ir.If:
		n.Cond = r.rewriteExpr(n.Cond)
		r.rewriteStmts(n.Body)
		r.rewriteStmts(n.Else)
	case *ir.For:
		n.Iter = r.rewriteExpr(n.Iter)
		n.ElemType = r.rewriteType(n.ElemType)
		r.rewriteStmts(n.Body)
		r.rewriteStmts(n.Else)
	case *ir.NodeInst:
		for i := range n.Props {
			n.Props[i].Value = r.rewriteExpr(n.Props[i].Value)
		}
		n.Key = r.rewriteExpr(n.Key)
		n.Ref = r.rewriteExpr(n.Ref)
		r.rewriteStmts(n.Children)
		for i := range n.Handlers {
			if n.Handlers[i].Func != nil {
				rewriteFuncSignature(n.Handlers[i].Func, r)
				r.rewriteStmts(n.Handlers[i].Func.Block)
			}
		}
	case *ir.SlotInst:
		r.rewriteStmts(n.Children)
	case *ir.ErrorBoundary:
		r.rewriteStmts(n.Children)
		if n.Handler != nil && n.Handler.Func != nil {
			rewriteFuncSignature(n.Handler.Func, r)
			r.rewriteStmts(n.Handler.Func.Block)
		}
	case *ir.Emit:
		for i := range n.Args {
			n.Args[i].Value = r.rewriteExpr(n.Args[i].Value)
		}
	case *ir.CallStmt:
		if n.Call != nil {
			if rewritten, ok := r.rewriteExpr(n.Call).(*ir.Call); ok {
				n.Call = rewritten
			}
		}
	case *ir.Toggle:
		n.Target = r.rewriteAssignTarget(n.Target)
	case *ir.Window:
		for i := range n.Props {
			n.Props[i].Value = r.rewriteExpr(n.Props[i].Value)
		}
		r.rewriteStmts(n.Body)
	case *ir.ContextProvider:
		n.Value = r.rewriteExpr(n.Value)
		r.rewriteStmts(n.Children)
	case *ir.Break, *ir.Continue:
		// A loop escape holds no ref to rewrite.
	default:
		panic(fmt.Sprintf("refRewriter.rewriteStmt: unhandled %T", n))
	}
}

// rewriteAssignTarget handles the LHS of an assignment. Idents to addressed
// Vars become Select{ident, "value"}; *p becomes p.value; otherwise normal
// rewriteExpr applies.
func (r *refRewriter) rewriteAssignTarget(t ir.Expr) ir.Expr {
	return r.rewriteExpr(t)
}

// seedAddressedVars walks pkg recording every *ir.Var whose address is
// taken by *ir.Unary{UnaryAddr}. Idempotent — entries set by NoLambda's
// lifter persist; this pass adds any Vars addressed by hand-written code.
func seedAddressedVars(pkg *ir.Package) {
	if pkg == nil {
		return
	}
	if pkg.AddressedVars == nil {
		pkg.AddressedVars = map[*ir.Var]bool{}
	}
	walkPackage(pkg, walkFuncs{
		expr: func(e ir.Expr) ir.Expr {
			seedAddressedVarsInExpr(e, pkg.AddressedVars)
			return e
		},
		stmts: func(stmts []ir.Stmt) []ir.Stmt {
			for _, s := range stmts {
				seedAddressedVarsInStmt(s, pkg.AddressedVars)
			}
			return stmts
		},
	})
}

func seedAddressedVarsInExpr(e ir.Expr, set map[*ir.Var]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Unary:
		if x.Op == ast.UnaryAddr {
			if leaf := addressLeafVar(x.Operand); leaf != nil {
				set[leaf] = true
			}
		}
		seedAddressedVarsInExpr(x.Operand, set)
	case *ir.Binary:
		seedAddressedVarsInExpr(x.Left, set)
		seedAddressedVarsInExpr(x.Right, set)
	case *ir.Ternary:
		seedAddressedVarsInExpr(x.Cond, set)
		seedAddressedVarsInExpr(x.Then, set)
		seedAddressedVarsInExpr(x.Else, set)
	case *ir.Call:
		seedAddressedVarsInExpr(x.Receiver, set)
		for i := range x.Args {
			seedAddressedVarsInExpr(x.Args[i].Value, set)
		}
	case *ir.Conversion:
		seedAddressedVarsInExpr(x.Operand, set)
	case *ir.Select:
		seedAddressedVarsInExpr(x.Operand, set)
	case *ir.Index:
		seedAddressedVarsInExpr(x.Operand, set)
		seedAddressedVarsInExpr(x.Idx, set)
	case *ir.ListLit:
		for _, el := range x.Elems {
			seedAddressedVarsInExpr(el, set)
		}
	case *ir.StructLit:
		for i := range x.Fields {
			seedAddressedVarsInExpr(x.Fields[i].Value, set)
		}
	case *ir.Spread:
		seedAddressedVarsInExpr(x.Operand, set)
	case *ir.Closure:
		if x.State != nil {
			for i := range x.State.Fields {
				seedAddressedVarsInExpr(x.State.Fields[i].Value, set)
			}
		}
		if x.Func != nil {
			for _, s := range x.Func.Block {
				seedAddressedVarsInStmt(s, set)
			}
		}
	case *ir.Lambda:
		if x.Func != nil {
			for _, s := range x.Func.Block {
				seedAddressedVarsInStmt(s, set)
			}
		}
	case *ir.MapLitIR:
		for _, en := range x.Entries {
			seedAddressedVarsInExpr(en.Key, set)
			seedAddressedVarsInExpr(en.Value, set)
		}
	case *ir.Ident, *ir.Literal, *ir.ContextRead:
		// Terminal — no UnaryAddr to seed.
	default:
		panic(fmt.Sprintf("seedAddressedVarsInExpr: unhandled %T", x))
	}
}

func seedAddressedVarsInStmt(s ir.Stmt, set map[*ir.Var]bool) {
	switch n := s.(type) {
	case *ir.Assign:
		seedAddressedVarsInExpr(n.Target, set)
		seedAddressedVarsInExpr(n.Value, set)
	case *ir.LocalVar:
		seedAddressedVarsInExpr(n.Init, set)
	case *ir.Return:
		seedAddressedVarsInExpr(n.Value, set)
	case *ir.If:
		seedAddressedVarsInExpr(n.Cond, set)
		for _, t := range n.Body {
			seedAddressedVarsInStmt(t, set)
		}
		for _, t := range n.Else {
			seedAddressedVarsInStmt(t, set)
		}
	case *ir.For:
		seedAddressedVarsInExpr(n.Iter, set)
		for _, t := range n.Body {
			seedAddressedVarsInStmt(t, set)
		}
		for _, t := range n.Else {
			seedAddressedVarsInStmt(t, set)
		}
	case *ir.NodeInst:
		for i := range n.Props {
			seedAddressedVarsInExpr(n.Props[i].Value, set)
		}
		seedAddressedVarsInExpr(n.Key, set)
		seedAddressedVarsInExpr(n.Ref, set)
		for _, t := range n.Children {
			seedAddressedVarsInStmt(t, set)
		}
		for i := range n.Handlers {
			if n.Handlers[i].Func != nil {
				for _, t := range n.Handlers[i].Func.Block {
					seedAddressedVarsInStmt(t, set)
				}
			}
		}
	case *ir.SlotInst:
		for _, t := range n.Children {
			seedAddressedVarsInStmt(t, set)
		}
	case *ir.ErrorBoundary:
		for _, t := range n.Children {
			seedAddressedVarsInStmt(t, set)
		}
		if n.Handler != nil && n.Handler.Func != nil {
			for _, t := range n.Handler.Func.Block {
				seedAddressedVarsInStmt(t, set)
			}
		}
	case *ir.Emit:
		for i := range n.Args {
			seedAddressedVarsInExpr(n.Args[i].Value, set)
		}
	case *ir.CallStmt:
		if n.Call != nil {
			seedAddressedVarsInExpr(n.Call, set)
		}
	case *ir.Toggle:
		seedAddressedVarsInExpr(n.Target, set)
	case *ir.Window:
		for i := range n.Props {
			seedAddressedVarsInExpr(n.Props[i].Value, set)
		}
		for _, t := range n.Body {
			seedAddressedVarsInStmt(t, set)
		}
	case *ir.ContextProvider:
		seedAddressedVarsInExpr(n.Value, set)
		for _, t := range n.Children {
			seedAddressedVarsInStmt(t, set)
		}
	case *ir.Break, *ir.Continue:
		// A loop escape addresses nothing.
	default:
		panic(fmt.Sprintf("seedAddressedVarsInStmt: unhandled %T", n))
	}
}

// addressLeafVar peels Select/Index off operand and returns the leaf *ir.Var
// (or nil if the chain doesn't terminate at one).
func addressLeafVar(e ir.Expr) *ir.Var {
	for {
		switch x := e.(type) {
		case *ir.Ident:
			if v, ok := x.Sym.(*ir.Var); ok {
				return v
			}
			return nil
		case *ir.Select:
			e = x.Operand
		case *ir.Index:
			e = x.Operand
		default:
			return nil
		}
	}
}

// boxRegistry deduplicates synthesized box structs by element-type identity.
// Element types are compared via canonical type-name strings.
type boxRegistry struct {
	pkg    *ir.Package
	byElem map[string]*ir.StructDef // elem-typename → box StructDef
}

func newBoxRegistry(pkg *ir.Package) *boxRegistry {
	return &boxRegistry{pkg: pkg, byElem: map[string]*ir.StructDef{}}
}

// boxFor returns the synthesized one-field struct definition that boxes elem.
// Reuses an existing __ref_<elem> if one was already synthesized this pass.
func (r *boxRegistry) boxFor(elem *ir.Type) *ir.StructDef {
	key := canonicalTypeName(elem)
	if def, ok := r.byElem[key]; ok {
		return def
	}
	def := &ir.StructDef{
		Name: "__ref_" + key,
		Fields: []*ir.StructField{
			{Name: "value", Type: elem},
		},
	}
	r.byElem[key] = def
	r.pkg.Structs = append(r.pkg.Structs, def)
	return def
}

// canonicalTypeName produces a stable, identifier-safe name for elem suitable
// as a suffix of __ref_. Collisions are avoided by structural-name encoding.
func canonicalTypeName(t *ir.Type) string {
	if t == nil {
		return "dyn"
	}
	switch t.Kind {
	case ir.TypeInt:
		return "int"
	case ir.TypeFloat:
		return "float"
	case ir.TypeString:
		return "string"
	case ir.TypeBool:
		return "bool"
	case ir.TypeStruct:
		if t.Decl != nil {
			return t.Decl.SymName()
		}
		return "anon_struct"
	case ir.TypeComponent:
		if t.Decl != nil {
			if c, ok := t.Decl.(interface{ SymName() string }); ok {
				return c.SymName()
			}
		}
		return "component"
	case ir.TypeInstance:
		if t.Decl != nil {
			if c, ok := t.Decl.(interface{ SymName() string }); ok {
				return "instance_" + c.SymName()
			}
		}
		return "instance"
	case ir.TypeList:
		var elem *ir.Type
		if len(t.Elems) > 0 {
			elem = t.Elems[0]
		}
		return "list_" + canonicalTypeName(elem)
	case ir.TypeOption:
		var elem *ir.Type
		if len(t.Elems) > 0 {
			elem = t.Elems[0]
		}
		return "option_" + canonicalTypeName(elem)
	case ir.TypeRemote:
		var elem *ir.Type
		if len(t.Elems) > 0 {
			elem = t.Elems[0]
		}
		return "remote_" + canonicalTypeName(elem)
	case ir.TypeFunc:
		return "func"
	}
	return t.Kind.String()
}
