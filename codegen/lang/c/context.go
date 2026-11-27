package c

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/irwalk"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// Context renders IR as C. It is the irwalk.Renderer for the language, and
// the CInterop a C call goes through, which on C is mostly the identity.
//
// What C does not yet write is refused rather than written as C that fails
// to compile: the renderer records the first such construct, writes a
// placeholder, and Err answers it, which the emitter returns from Close.
type Context struct {
	Ctx *codegen.ExprCtx
	// State, where set, renders a name the platform holds rather than the
	// scope -- a package var, a widget the package body made, which gtk4
	// keeps as statics of the file -- and reports whether n was one. local
	// says the scope declares a local of n's name, which a name the platform
	// matches by its spelling alone does not reach past.
	State func(n *ir.Ident, local bool) (string, bool)
	// Native, where set, renders a call the platform answers itself -- one
	// of a runtime the platform has in C under another name -- and reports
	// whether it was one.
	Native func(c *Context, n *ir.Call) (string, bool)
	// Closures, where set, writes a closure the platform holds otherwise --
	// one reading a record a platform's code is written in -- and reports
	// whether n was one.
	Closures func(c *Context, n *ir.Closure) (string, bool)
	// Types, where set, names a type the platform holds as a C type of its
	// own: a widget, which gtk4 holds as the GObject pointer it is.
	Types func(t *ir.Type) (string, bool)
	// file is what the rendering needs declared ahead of it.
	file *file
	// locals is what the scope declares that shadows the package.
	locals map[string]bool
	// refs are the locals a lifted lambda reaches through their address,
	// since it writes them (Lambda).
	refs map[string]bool
	// ret is the return type of the function being written.
	ret *ir.Type
	// Self, where set, is the parameter the code being written reads a
	// platform's record through, `T *name`, which a lambda lifted out of it
	// takes along.
	Self string
}

var (
	_ irwalk.Renderer  = (*Context)(nil)
	_ codegen.CInterop = (*Context)(nil)
)

// NewContext is a renderer over ctx's package.
func NewContext(ctx *codegen.ExprCtx) *Context {
	return &Context{Ctx: ctx, file: newFile(), locals: map[string]bool{}}
}

// Err is the first construct the renderer met that C does not yet write.
func (c *Context) Err() error { return c.file.err }

// fail records that C does not yet write what format says, and is the
// placeholder written in its place.
func (c *Context) fail(format string, args ...any) string {
	if c.file.err == nil {
		c.file.err = fmt.Errorf("c: "+format, args...)
	}
	return "/* unsupported */ 0"
}

// decline is fail for what C cannot express by design rather than what this
// generator gets wrong: a host value or function another language owns.
func (c *Context) decline(format string, args ...any) string {
	return c.declined(ir.Decline("c", "c: "+format, args...))
}

func (c *Context) declined(d *ir.TargetDeclined) string {
	if c.file.err == nil {
		c.file.err = d
	}
	return "/* unsupported */ 0"
}

// TypeName is t as C declares a value of it.
func (c *Context) TypeName(t *ir.Type) string { return c.typeName(t) }

func (c *Context) typeName(t *ir.Type) string {
	t = listed(t)
	if t != nil && c.Types != nil {
		if s, ok := c.Types(t); ok {
			return s
		}
	}
	if t != nil {
		switch t.Kind {
		case ir.TypeFunc:
			return c.fnType(t)
		case ir.TypeList:
			return c.listType(t)
		case ir.TypeOption:
			return c.optType(t)
		case ir.TypeUnit:
			if ud := unitDef(t); ud != nil {
				return c.unitType(ud)
			}
		case ir.TypeMap:
			return c.mapType(t)
		case ir.TypeEnum:
			if ed := ir.PayloadEnum(t); ed != nil {
				return c.enumType(t, ed)
			}
		case ir.TypeStruct:
			if textual(t) {
				return "sngl_string"
			}
			// A box is shared by every copy of it, so it is held through a
			// pointer to the one on the heap.
			if sd, ok := t.Decl.(*ir.StructDef); ok && sd.RefBox {
				c.Box(sd)
				return Decl(&sd.DeclInfo) + " *"
			}
			if sd, ok := t.Decl.(*ir.StructDef); ok && isProgramStruct(t) {
				c.defineStruct(sd)
				return Decl(&sd.DeclInfo)
			}
		case ir.TypeNative:
			if ref, ok := t.Meta.(ir.NativeTypeRef); ok {
				name := strings.TrimPrefix(ref.Name, "C.")
				switch {
				case ref.Bare:
					return name
				case name == "":
					return "void *"
				}
				return name + " *"
			}
		}
	}
	s, err := cTypeName(t)
	if err != nil {
		return c.fail("%v", err)
	}
	return s
}

// WithReturn is c writing a function returning t.
func (c *Context) WithReturn(t *ir.Type) *Context {
	cc := *c
	cc.ret = t
	return &cc
}

// WithLocal is c with name declared in its scope.
func (c *Context) WithLocal(name string) *Context {
	cc := *c
	cc.locals = make(map[string]bool, len(c.locals)+1)
	for k := range c.locals {
		cc.locals[k] = true
	}
	cc.locals[name] = true
	return &cc
}

func (c *Context) EvalExpr(e ir.Expr) string { return irwalk.EvalExpr(c, e) }

// EvalBlock renders stmts in order, each local one declares in scope from
// its declaration on.
func (c *Context) EvalBlock(stmts []ir.Stmt) []string { return irwalk.EvalBlock(c, stmts) }
func (c *Context) EvalStmt(s ir.Stmt) []string        { return irwalk.EvalStmt(c, s) }

// --- Leaves ---

func (c *Context) NilExpr() string { return "NULL" }

func (c *Context) Literal(n *ir.Literal) string {
	if out, ok := c.unitLiteral(n); ok {
		return out
	}
	return literal(n)
}

func literal(n *ir.Literal) string {
	if n.Type == nil {
		return n.Value
	}
	if textual(n.Type) {
		return stringLiteral(n.Value)
	}
	switch n.Type.Kind {
	case ir.TypeString:
		return stringLiteral(n.Value)
	case ir.TypeFloat:
		if !strings.ContainsAny(n.Value, ".eE") {
			return n.Value + ".0"
		}
		return n.Value
	case ir.TypeInt:
		return intLiteral(n.Value)
	case ir.TypeNull:
		return "NULL"
	}
	return n.Value
}

// intLiteral is v, a decimal integer, as a C constant of int64_t's range: an
// int's constant is the narrowest type holding it, and one past 32 bits is
// spelled through INT64_C so that no compiler warns.
func intLiteral(v string) string {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n > 1<<31-1 || n < -(1<<31) {
		if strings.HasPrefix(v, "-") {
			return "(-INT64_C(" + v[1:] + "))"
		}
		return "INT64_C(" + v + ")"
	}
	return v
}

// StringLiteral is s as a string literal of the program's.
func StringLiteral(s string) string { return stringLiteral(s) }

// stringLiteral is s as a string the program holds: its bytes, escaped so
// that C reads back exactly them, with the length the compiler counts.
func stringLiteral(s string) string {
	return "SNGL_STR(" + cQuote(s) + ")"
}

// cQuote is s as a C string literal. A byte outside printable ASCII is an
// octal escape, which, unlike a hex one, never runs into the digit after it.
func cQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '"' || ch == '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		case ch == '\n':
			b.WriteString(`\n`)
		case ch == '\t':
			b.WriteString(`\t`)
		case ch == '?':
			// `??` would begin a trigraph a C11 compiler may read.
			b.WriteString(`\?`)
		case ch >= 0x20 && ch < 0x7f:
			b.WriteByte(ch)
		default:
			fmt.Fprintf(&b, `\%03o`, ch)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (c *Context) Ident(n *ir.Ident) string {
	if c.refs[n.Name] {
		return "(*" + Local(n.Name) + ")"
	}
	switch sym := n.Sym.(type) {
	case *ir.Param, *ir.LoopVar:
		return Local(n.Name)
	case *ir.Var:
		// State first: a var the platform holds is that var whatever a local
		// is called, as a slot's render names its box `parent`.
		if c.State != nil {
			if s, ok := c.State(n, c.locals[n.Name]); ok {
				return s
			}
		}
		// The package's var before a local of its name: placement holds the
		// node a slot makes in a var named as the local making it
		// (holdAfter), and naming both by the local's name dropped the hold.
		if c.isPackageVar(sym) {
			return Decl(&sym.DeclInfo)
		}
		if c.locals[n.Name] {
			return Local(n.Name)
		}
		// A var no scope of the file's declares: a component's, read where
		// the build put none of it -- the handler of a boundary inlined
		// away, reached from a lambda lifted before it was.
		if ir.IsHostValue(sym) {
			return c.decline("%s, which is %s's", n.Name, sym.Foreign.Path)
		}
		if c.State != nil && !c.isLocalVar(sym) {
			return c.fail("reading %s, which no scope of the program holds here", n.Name)
		}
		return Local(n.Name)
	case *ir.Func:
		if n.Type == nil || n.Type.Kind != ir.TypeFunc {
			return c.fail("the function %s used as a value of type %s", n.Name, n.Type)
		}
		return c.funcValue(sym, n.Type)
	case *ir.Component:
		// The record a platform writes the component as, where it is one.
		if c.State != nil {
			if s, ok := c.State(n, false); ok {
				return s
			}
		}
		return c.fail("the component %s as a value outside its own record", n.Name)
	}
	if n.Member != "" && ir.PayloadEnum(n.Type) != nil {
		return c.enumLit(n.Type, n.Member, "")
	}
	if n.Member != "" {
		// A member a value is made of after NoEnum ran -- a prop's zero value
		// a record's constructor fills in -- is the ordinal NoEnum makes one.
		if ed, ok := n.Type.Decl.(*ir.EnumDef); ok && n.Type.Kind == ir.TypeEnum && ir.PayloadEnum(n.Type) == nil {
			for i, m := range ed.Members {
				if m.Name == n.Member {
					return strconv.Itoa(i)
				}
			}
		}
		return c.fail("an enum member (%s)", n.Member)
	}
	if c.State != nil {
		if s, ok := c.State(n, c.locals[n.Name]); ok {
			return s
		}
	}
	return Local(n.Name)
}

func (c *Context) isPackageVar(v *ir.Var) bool {
	if c.Ctx == nil || c.Ctx.Pkg == nil {
		return false
	}
	for _, list := range [][]*ir.Var{c.Ctx.Pkg.Vars, c.Ctx.Pkg.Consts} {
		if slices.Contains(list, v) {
			return true
		}
	}
	return false
}

// --- Composites ---

func (c *Context) Binary(n *ir.Binary, left, right string) string {
	if out, ok := c.optionCompare(n, left, right); ok {
		return out
	}
	if out, ok := c.unitBinary(n, left, right); ok {
		return out
	}
	lt := n.Left.ExprType()
	if lt != nil && (lt.Kind == ir.TypeString || textual(lt)) {
		switch n.Op {
		case ast.BinAdd:
			if parts, ok := c.sequenced(n, "sngl_string", left, right); ok {
				return "(" + parts[0] + ", " + parts[1] + ", sngl_concat(" + parts[2] + ", " + parts[3] + "))"
			}
			return "sngl_concat(" + left + ", " + right + ")"
		case ast.BinEq:
			return "sngl_string_eq(" + left + ", " + right + ")"
		case ast.BinNeq:
			return "!sngl_string_eq(" + left + ", " + right + ")"
		case ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte:
			return "(sngl_string_cmp(" + left + ", " + right + ") " + n.Op.String() + " 0)"
		}
	}
	if lt != nil && (lt.Kind == ir.TypeStruct || lt.Kind == ir.TypeList || ir.PayloadEnum(lt) != nil) && (n.Op == ast.BinEq || n.Op == ast.BinNeq) {
		eq := c.equal(lt, left, right)
		if n.Op == ast.BinNeq {
			return "!" + eq
		}
		return eq
	}
	if lt != nil && lt.Kind == ir.TypeInt {
		switch n.Op {
		case ast.BinDiv:
			return "sngl_div(" + left + ", " + right + ")"
		case ast.BinMod:
			return "sngl_mod(" + left + ", " + right + ")"
		}
	}
	if lt != nil && lt.Kind == ir.TypeFloat && n.Op == ast.BinMod {
		c.file.include("math.h", true)
		return "fmod(" + left + ", " + right + ")"
	}
	return "(" + left + " " + n.Op.String() + " " + right + ")"
}

// equal is whether a and b, two values of type t, are equal: a struct's
// fields each, a list's length and elements, and an option's presence and
// then its value, through a function the file defines for it, since C
// compares no struct.
func (c *Context) equal(t *ir.Type, a, b string) string {
	if textual(t) {
		return "sngl_string_eq(" + a + ", " + b + ")"
	}
	if ed := ir.PayloadEnum(t); ed != nil {
		return c.enumEqual(t, ed, a, b)
	}
	switch t.Kind {
	case ir.TypeString:
		return "sngl_string_eq(" + a + ", " + b + ")"
	case ir.TypeStruct:
		// A box is shared, held by its pointer, and is equal only to
		// itself: a record, a var a closure writes.
		if !isProgramStruct(t) || isBox(t) {
			break
		}
		sd := t.Decl.(*ir.StructDef)
		return c.eqHelper(t, a, b, func() string {
			var parts []string
			for _, f := range sd.Fields {
				parts = append(parts, c.equal(f.Type, "a."+Local(f.Name), "b."+Local(f.Name)))
			}
			if len(parts) == 0 {
				parts = []string{"true"}
			}
			return "\treturn " + strings.Join(parts, " && ") + ";\n"
		})
	case ir.TypeList:
		return c.eqHelper(t, a, b, func() string {
			return "\tif (a.len != b.len) {\n\t\treturn false;\n\t}\n\tfor (int64_t i = 0; i < a.len; i++) {\n\t\tif (!" +
				c.equal(t.Elems[0], "a.data[i]", "b.data[i]") + ") {\n\t\t\treturn false;\n\t\t}\n\t}\n\treturn true;\n"
		})
	case ir.TypeOption:
		// Held by its pointer, an option is NULL or the value's; otherwise
		// it is a struct of whether it holds one and the one it holds.
		if c.ptrOption(t) {
			break
		}
		return c.eqHelper(t, a, b, func() string {
			return "\treturn a.ok == b.ok && (!a.ok || " + c.equal(t.Elems[0], "a.value", "b.value") + ");\n"
		})
	}
	return "(" + a + " == " + b + ")"
}

// eqHelper is a call of the function the file defines to compare two values
// of t, whose body is written once. It is declared ahead, since a recursive
// type's -- a struct holding a list of itself -- reaches it again.
func (c *Context) eqHelper(t *ir.Type, a, b string, body func() string) string {
	name := c.typeName(t)
	fn := name + "__eq"
	call := fn + "(" + a + ", " + b + ")"
	if c.file.helperNames[fn] {
		return call
	}
	sig := "static bool " + fn + "(" + name + " a, " + name + " b)"
	c.file.helper(fn, "")
	c.file.protos = append(c.file.protos, sig+";")
	c.file.setHelper(fn, sig+" {\n"+body()+"}\n")
	return call
}

func (c *Context) Unary(n *ir.Unary, operand string) string {
	switch n.Op {
	case ast.UnaryNot:
		return "!" + operand
	case ast.UnaryNeg:
		return "(-" + operand + ")"
	}
	return c.fail("the %s operator", n.Op)
}

func (c *Context) Ternary(_ *ir.Ternary, cond, then, els string) string {
	return "(" + cond + " ? " + then + " : " + els + ")"
}

func (c *Context) Select(n *ir.Select, operand string) string {
	t := n.Operand.ExprType()
	// lower.NoRef reboxes a var and the reads it rewrites, and leaves an
	// identifier it did not visit typed as the value: the var says what it
	// holds.
	if id, ok := n.Operand.(*ir.Ident); ok {
		if v, ok := id.Sym.(*ir.Var); ok && v.Type != nil && (t == nil || t.Kind != ir.TypeStruct || isBox(v.Type)) {
			t = v.Type
		}
	}
	// A ref NoRef boxed, read where the type left on it is still the ref's.
	if t != nil && t.Kind == ir.TypeRef && n.Field == "value" {
		return operand + "->value"
	}
	if n.Field == "length" && t != nil && ir.HasLength(t) {
		if t.Kind == ir.TypeString {
			return "sngl_string_length(" + operand + ")"
		}
		return "(" + operand + ").len"
	}
	if t != nil && c.isPointer(t) {
		// A box, a C pointer, a record a platform holds by pointer.
		return operand + "->" + Local(n.Field)
	}
	if t == nil || (t.Kind != ir.TypeStruct && !multiBase(t)) {
		return c.fail("selecting %s of %s", n.Field, t)
	}
	if entry, ok := c.mapEntry(n.Operand, operand); ok {
		return entry + "->" + Local(n.Field)
	}
	return operand + "." + Local(n.Field)
}

// isBox reports whether t is a box lower.NoRef made.
func isBox(t *ir.Type) bool {
	if t == nil || t.Kind != ir.TypeStruct {
		return false
	}
	sd, ok := t.Decl.(*ir.StructDef)
	return ok && sd.RefBox
}

// isPointer reports whether a value of t is held through a pointer, which a
// field is selected through with `->`.
func (c *Context) isPointer(t *ir.Type) bool {
	return strings.HasSuffix(c.typeName(t), "*")
}

// StructLit is a compound literal naming each field it sets; one it leaves
// out is C's zero, which is SNGL's for every type C writes.
func (c *Context) StructLit(n *ir.StructLit, fields []string) string {
	// A value a platform holds by pointer, a gtk4 picture: its zero is
	// none.
	if len(n.Fields) == 0 && c.isPointer(n.Type) && !isBox(n.Type) {
		return "NULL"
	}
	if ir.PayloadEnum(n.Type) != nil && len(n.Fields) == 1 {
		return c.enumLit(n.Type, n.Fields[0].Name, fields[0])
	}
	if !isProgramStruct(n.Type) {
		// A C type SNGL holds opaquely has one literal, its zero: what a
		// parameter the compiler passes defaults to.
		if ct := ir.CTypeOf(n.Type); ct != nil && len(n.Fields) == 0 {
			return "(" + ct.String() + ")0"
		}
		return c.fail("a literal of %s", n.Type)
	}
	if sd, ok := n.Type.Decl.(*ir.StructDef); ok && sd.RefBox {
		name := Decl(&sd.DeclInfo)
		c.Box(sd)
		// A box is a holder: what it starts holding is kept.
		kept := slices.Clone(fields)
		for i, f := range n.Fields {
			if i >= len(kept) {
				continue
			}
			if j := slices.IndexFunc(sd.Fields, func(sf *ir.StructField) bool { return sf.Name == f.Name }); j >= 0 {
				kept[i] = c.keep(sd.Fields[j].Type, c.nullAs(sd.Fields[j].Type, f.Value, kept[i]))
			}
		}
		lit := "&(" + name + "[]){" + c.structLit(n, kept) + "}, sizeof(" + name + ")"
		if sd.Counted {
			// Shared by its holders, the last of which gives it back with
			// what it holds.
			release, rekeep, visit := c.boxOps(sd)
			return "((" + name + " *)sngl_counted_new(" + lit + ", " + release + ", " + rekeep + ", " + visit + "))"
		}
		// The box is kept itself, so the record it is can give it back.
		return "((" + name + " *)sngl_keep_copy(" + lit + "))"
	}
	return c.structLit(n, fields)
}

func (c *Context) structLit(n *ir.StructLit, fields []string) string {
	sd := n.Type.Decl.(*ir.StructDef)
	name := Decl(&sd.DeclInfo)
	// A literal may be the only place its type is named: an anonymous
	// struct handed straight to a parameter.
	c.defineStruct(sd)
	var parts []string
	for i, f := range n.Fields {
		if f.Spread || f.Name == "" {
			return c.fail("a spread into %s", n.Type)
		}
		v := fields[i]
		for _, sf := range sd.Fields {
			if sf.Name == f.Name {
				v = c.nullAs(sf.Type, f.Value, v)
			}
		}
		parts = append(parts, "."+Local(f.Name)+" = "+v)
	}
	if len(parts) == 0 {
		return "(" + name + "){0}"
	}
	return "(" + name + "){" + strings.Join(parts, ", ") + "}"
}

// Box defines sd, a box lower.NoRef made, as the struct it is.
func (c *Context) Box(sd *ir.StructDef) {
	c.defineStruct(sd)
}

// boxOps are the functions giving back what a counted box of sd holds,
// which its last holder letting go runs; keeping it again, which a holder
// taking it again the same turn runs; and visiting the counted boxes it
// holds, which the collector of cycles runs.
func (c *Context) boxOps(sd *ir.StructDef) (release, rekeep, visit string) {
	name := Decl(&sd.DeclInfo)
	release, rekeep, visit = name+"__release", name+"__rekeep", "NULL"
	if slices.ContainsFunc(sd.Fields, func(f *ir.StructField) bool { return visits(f.Type) }) {
		// One holding no box closes no cycle, and has no visit.
		visit = name + "__visit"
	}
	if c.file.helperNames[release] {
		return release, rekeep, visit
	}
	c.file.helper(release, "")
	c.file.helper(rekeep, "")
	c.file.protos = append(c.file.protos, "static void "+release+"(void *p);", "static void "+rekeep+"(void *p);")
	var rel, keep, vis strings.Builder
	fmt.Fprintf(&rel, "static void %s(void *p) {\n\t%s *b = p;\n", release, name)
	fmt.Fprintf(&keep, "static void %s(void *p) {\n\t%s *b = p;\n", rekeep, name)
	fmt.Fprintf(&vis, "static void %s(void *p, sngl_visit fn) {\n\t%s *b = p;\n", visit, name)
	for _, f := range sd.Fields {
		if d := c.drop(f.Type, "b->"+Local(f.Name)); d != "" {
			rel.WriteString("\t" + d + "\n")
			fmt.Fprintf(&keep, "\tb->%s = %s;\n", Local(f.Name), c.keep(f.Type, "b->"+Local(f.Name)))
		}
		if v := c.visit(f.Type, "b->"+Local(f.Name), "fn"); v != "" {
			vis.WriteString("\t" + v + "\n")
		}
	}
	for _, b := range []*strings.Builder{&rel, &keep, &vis} {
		b.WriteString("\t(void)b;\n}\n")
	}
	c.file.setHelper(release, rel.String())
	c.file.setHelper(rekeep, keep.String())
	if visit != "NULL" {
		c.file.helper(visit, vis.String())
		c.file.protos = append(c.file.protos, "static void "+visit+"(void *p, sngl_visit fn);")
	}
	return release, rekeep, visit
}

// Spread is the list a spread reads, which the list literal it is in joins
// (ListLit); a spread anywhere else is refused there.
func (c *Context) Spread(n *ir.Spread, operand string) string {
	if t := n.Operand.ExprType(); t == nil || t.Kind != ir.TypeList {
		return c.fail("a spread of %s", t)
	}
	return operand
}

func (c *Context) MatchExpr(n *ir.MatchExpr) string {
	return c.EvalExpr(n.TernaryChain())
}

// --- Calls ---

func (c *Context) Call(n *ir.Call) string {
	// The platform first: a test's trigger of a widget's event resolves to
	// no function, and is the platform's to fire.
	if c.Native != nil {
		if out, ok := c.Native(c, n); ok {
			return out
		}
	}
	if n.Func == nil {
		if n.Callee == nil {
			return c.fail("a call of nothing")
		}
		args := make([]string, len(n.Args))
		for i, a := range n.Args {
			args[i] = c.EvalExpr(a.Value)
		}
		return c.callValue(c.EvalExpr(n.Callee), args)
	}
	if n.Func.IsCCast() && len(n.Args) == 1 {
		return c.CConvert(ir.NewCConvert(n.Func, n.Args[0].Value))
	}
	if out, ok := c.listIntrinsic(n); ok {
		return out
	}
	if out, ok := c.codecIntrinsic(n); ok {
		return out
	}
	if out, ok := c.i18nIntrinsic(n); ok {
		return out
	}
	if out, ok := c.seqIntrinsic(n); ok {
		return out
	}
	if out, ok := c.stringIntrinsic(n); ok {
		return out
	}
	if out, ok := c.numberIntrinsic(n); ok {
		return out
	}
	if out, ok := c.enumIntrinsic(n); ok {
		return out
	}
	if out, ok := c.optionIntrinsic(n); ok {
		return out
	}
	switch n.Func.Intrinsic {
	case lower.AsyncStartIntrinsic, lower.AsyncDoIntrinsic:
		// C has no thread of its own to start work on: the work runs on the
		// loop's, once the turn that started it is done, on a stack of its
		// own (sngl_work_start), and its `do` through what the platform's
		// runtime defines sngl_post as.
		if len(n.Args) != 1 {
			return c.fail("%s of %d arguments", n.Func.Intrinsic, len(n.Args))
		}
		fn, ctx := c.Callback(ir.FuncOf(nil, ir.TypVoid).Sig, n.Args[0].Value)
		post := "sngl_post"
		if n.Func.Intrinsic == lower.AsyncStartIntrinsic {
			post = "sngl_work_start"
			c.file.works = true
		}
		return post + "((void (*)(void *))" + fn + ", " + ctx + ", sngl_release)"
	case "async.begin":
		return "sngl_async_begin()"
	case "async.end", "async.ended":
		if len(n.Args) == 1 {
			return "sngl_" + strings.ReplaceAll(n.Func.Intrinsic, ".", "_") + "(" + c.EvalExpr(n.Args[0].Value) + ")"
		}
	}
	if n.Func.Intrinsic == "map.remove" && len(n.Args) == 2 {
		return c.mapRemove(n)
	}
	if strings.HasPrefix(n.Func.Intrinsic, "map.") && len(n.Args) > 0 {
		if t := n.Args[0].Value.ExprType(); t != nil && t.Kind == ir.TypeMap {
			return c.mapIntrinsic(n, t, c.EvalExpr(n.Args[0].Value))
		}
	}
	if n.Func.Intrinsic == ir.ValueCopyIntrinsic && len(n.Args) == 1 {
		return c.copyValue(n.Args[0].Value.ExprType(), c.EvalExpr(n.Args[0].Value))
	}
	if out, _, ok := codegen.EmitIntrinsicCall(langC, c.platform(), n, c.EvalExpr); ok {
		return out
	}
	args := make([]string, len(n.Args))
	for i, a := range n.Args {
		args[i] = c.EvalExpr(a.Value)
		// A C function's null is its NULL, a program's the zero of the type.
		if p := paramFor(n.Func, a, i); p != nil && n.Func.Foreign.Path == "" {
			args[i] = c.nullAs(p.Type, a.Value, args[i])
		}
	}
	if n.Func.Foreign.Path == ir.CPath && !n.Func.Foreign.Marked {
		return c.CCall(n, args)
	}
	if out, ok := c.merge(n, args); ok {
		return out
	}
	if n.Func.Foreign.Path != "" && !n.Func.Foreign.Marked {
		return c.decline("calling %s, which is %s's", n.Func.Name, n.Func.Foreign.Path)
	}
	if n.Receiver != nil {
		return c.fail("calling %s on a component", n.Func.Name)
	}
	if n.Func.Intrinsic != "" && len(n.Func.Block) == 0 {
		return c.declined(codegen.UnemittedIntrinsic(langC, n))
	}
	return funcName(n.Func) + "(" + strings.Join(args, ", ") + ")"
}

func (c *Context) platform() string {
	if c.Ctx == nil {
		return ""
	}
	return c.Ctx.Platform
}

// funcName is fn as the program declares it in C: a method of a type its
// receiver's name and its own.
// FuncName is what the file calls fn.
func FuncName(fn *ir.Func) string { return funcName(fn) }

func funcName(fn *ir.Func) string {
	if fn.IsMethod() {
		name := Method(fn.RecvName(), fn.Name)
		if path := fn.PkgPath(); path != "" {
			name = "s_" + mangle(strings.TrimPrefix(path, "sngl:")) + "__" + strings.TrimPrefix(name, "s_")
		}
		return name
	}
	return Decl(&fn.DeclInfo)
}

// Conversion converts between SNGL's scalars, and writes a scalar's text.
func (c *Context) Conversion(n *ir.Conversion) string {
	if out, ok := c.option(n); ok {
		return out
	}
	from, to := n.Operand.ExprType(), n.Type
	if to != nil && to.Kind == ir.TypeIter && from != nil && from.Kind == ir.TypeList {
		// A list where an iter is read is the list: a loop walks either.
		return c.EvalExpr(n.Operand)
	}
	operand := c.EvalExpr(n.Operand)
	if from == nil || to == nil {
		return c.fail("a conversion of an untyped value")
	}
	// A value as its own type is itself: a platform's node as the handle
	// it is held as (passPlacement).
	if from.Equal(to) {
		return operand
	}
	// A widget, a C pointer: the cast C writes between two of them.
	if c.isPointer(to) && (c.isPointer(from) || from.Kind == ir.TypeComponent) {
		return "(" + c.typeName(to) + ")(" + operand + ")"
	}
	// A date, a time and a datetime are their text.
	if textual(from) && (to.Kind == ir.TypeString || textual(to)) || textual(to) && from.Kind == ir.TypeString {
		return operand
	}
	if to.Kind == ir.TypeString {
		switch from.Kind {
		case ir.TypeString:
			return operand
		case ir.TypeInt:
			return "sngl_int_format((int64_t)(" + operand + "))"
		case ir.TypeFloat:
			return "sngl_float_format(" + operand + ")"
		case ir.TypeBool:
			return "sngl_bool_format(" + operand + ")"
		case ir.TypeUnit:
			if ud := unitDef(from); ud != nil {
				return c.unitText(ud, operand)
			}
		}
		if ir.IsColorStruct(from) {
			return c.colorHex(from, operand)
		}
		return c.fail("the text of a %s", from)
	}
	switch to.Kind {
	case ir.TypeInt, ir.TypeFloat:
		// A double past an int's range is undefined to cast in C.
		fromDouble := func(v string) string {
			if to.Kind == ir.TypeInt {
				return "sngl_float_int(" + v + ")"
			}
			return "(" + c.typeName(to) + ")(" + v + ")"
		}
		switch from.Kind {
		case ir.TypeFloat:
			return fromDouble(operand)
		case ir.TypeInt, ir.TypeBool, ir.TypeEnum:
			return "(" + c.typeName(to) + ")(" + operand + ")"
		case ir.TypeUnit:
			// The magnitude of a single-base unit, which is its double.
			if !multiBase(from) {
				return fromDouble(operand)
			}
		}
	case ir.TypeBool:
		if from.Kind == ir.TypeBool {
			return operand
		}
	case ir.TypeUnit:
		// A number as a single-base unit is that many of its base, the
		// double C holds it as: `var zero duration = 0`
		// (testdata/unit_duration_name.sngl).
		if !multiBase(to) && (from.Kind == ir.TypeInt || from.Kind == ir.TypeFloat) {
			return "(double)(" + operand + ")"
		}
	}
	return c.fail("converting %s to %s", from, to)
}

// --- Statements ---

func (c *Context) AssignText(n *ir.Assign, target, value string) string {
	t := n.Target.ExprType()
	op := n.Op
	// A value read only so it is used, as passAsyncOffload reads a binding
	// whose call it moved.
	if id, ok := n.Target.(*ir.Ident); ok && id.Name == "_" && op == ast.AssignSet {
		return "(void)(" + value + ");"
	}
	if op == ast.AssignSet {
		value = c.nullAs(t, n.Value, value)
		if c.holder(n.Target) && owns(t) {
			// A holder's keep is a copy already where it shares nothing,
			// and one sharing a buffer copies it before writing it in place
			// (unshare), so the value's own copy would be made to be thrown
			// away.
			if call, ok := n.Value.(*ir.Call); ok && call.Func != nil && call.Func.Intrinsic == ir.ValueCopyIntrinsic && len(call.Args) == 1 {
				value = c.nullAs(t, call.Args[0].Value, c.EvalExpr(call.Args[0].Value))
			}
			return c.store(t, target, value)
		}
		if c.throughRef(n.Target) && owns(t) {
			return c.turnCopy(t, target, value)
		}
		return target + " = " + value + ";"
	}
	if t != nil && (t.Kind == ir.TypeString || t.Kind == ir.TypeInt && (op == ast.AssignDiv || op == ast.AssignMod)) {
		bin := map[ast.AssignOp]ast.BinaryOp{ast.AssignAdd: ast.BinAdd, ast.AssignDiv: ast.BinDiv, ast.AssignMod: ast.BinMod}[op]
		value = c.Binary(&ir.Binary{Op: bin, Left: n.Target, Right: n.Value, Type: t}, target, value)
		if c.holder(n.Target) && owns(t) {
			return c.store(t, target, value)
		}
		return target + " = " + value + ";"
	}
	if t != nil && t.Kind == ir.TypeFloat && op == ast.AssignMod {
		c.file.include("math.h", true)
		return target + " = fmod(" + target + ", " + value + ");"
	}
	return target + " " + op.String() + " " + value + ";"
}

// throughRef reports whether target is a local a closure writes, which it
// holds the address of.
func (c *Context) throughRef(target ir.Expr) bool {
	id, ok := target.(*ir.Ident)
	return ok && c.refs[id.Name]
}

// turnCopy is target, a local a closure writes, written with value copied
// to the heap and dropped at once, so the copy lasts until the turn ends:
// the closure may run as a C callback, whose call gives back the arena it
// used as it returns (sngl_call_leave), and the local is read after.
func (c *Context) turnCopy(t *ir.Type, target, value string) string {
	kept := fmt.Sprintf("__turn%d", c.file.next())
	tn := c.typeName(t)
	return "{\n\t" + tn + " " + kept + " = " + c.keep(t, value) + ";\n\t" + c.drop(t, kept) + "\n\t" + target + " = " + kept + ";\n}"
}

func (c *Context) ToggleText(_ *ir.Toggle, target string) string {
	return target + " = !" + target + ";"
}

func (c *Context) CallStmtLines(n *ir.CallStmt) []string {
	call := n.Call
	if ir.IsErrorRaiseFunc(call.Func) && len(call.Args) > 0 {
		return c.raise(call)
	}
	if h := ir.CatchingHandler(call); h != nil && h.Func != nil {
		at := fmt.Sprintf("__catch%d", c.file.next())
		lines := c.guard(at, []string{c.EvalExpr(call) + ";"})
		lines = append(lines, "\t\tsngl_catching = "+at+".up;", "\t} else {")
		lines = append(lines, caught(at)...)
		lines = append(lines, irwalk.IndentLines("\t\t", c.handle(h, at+".message", at+".kind"))...)
		return append(lines, "\t}", "}")
	}
	// A C function's answer a statement drops: converted back to SNGL's
	// value, `(f(...) != 0)` for a bool, it is a value C warns is unused.
	if fn := call.Func; fn != nil && fn.Foreign.Path == ir.CPath && !fn.IsCCast() && fn.Return != nil && fn.Return.Kind != ir.TypeVoid {
		return []string{"(void)" + c.EvalExpr(call) + ";"}
	}
	return []string{c.EvalExpr(call) + ";"}
}

// raise is error.raise: a jump to the catch point around it, or the handler
// it resolved to run in its place where it raises with nothing between to
// unwind -- its own @error, or a boundary's around a view body.
func (c *Context) raise(call *ir.Call) []string {
	msg, kind := c.EvalExpr(call.Args[0].Value), "SNGL_STR(\"\")"
	if len(call.Args) > 1 {
		kind = c.EvalExpr(call.Args[1].Value)
	}
	var h *ir.EventHandler
	switch call.ErrorMode {
	case ir.ErrorPerCall:
		if call.ErrorHandler == nil || call.ErrorHandler.Func == nil {
			return []string{"(void)" + msg + ";", "(void)" + kind + ";"}
		}
		h = call.ErrorHandler
	case ir.ErrorInvokeAndTerminate:
		h = call.ResolvedHandler
	}
	if h == nil || h.Func == nil {
		return []string{"sngl_raise(" + msg + ", " + kind + ");"}
	}
	return c.handle(h, msg, kind)
}

// handle is h's body run on the error msg and kind, as a block of its own
// whose return ends the handler alone: it goes to the end of the block.
func (c *Context) handle(h *ir.EventHandler, msg, kind string) []string {
	lines := []string{"{"}
	r := c
	if len(h.Func.Params) > 0 {
		p := h.Func.Params[0]
		name := Local(p.Name)
		lines = append(lines,
			"\t"+c.typeName(p.Type)+" "+name+" = {."+Local("message")+" = "+msg+", ."+Local("kind")+" = "+kind+"};",
			"\t(void)"+name+";")
		r = c.WithLocal(p.Name)
	}
	end := ""
	for _, l := range r.EvalBlock(h.Func.Block) {
		if t := strings.TrimLeft(l, "\t"); t == "return;" {
			if end == "" {
				end = fmt.Sprintf("__handled%d", c.file.next())
			}
			l = l[:len(l)-len(t)] + "goto " + end + ";"
		}
		lines = append(lines, "\t"+l)
	}
	lines = append(lines, "}")
	if end != "" {
		lines = append(lines, end+":;")
	}
	return lines
}

func (c *Context) EmitText(n *ir.Emit, _ []string) string {
	c.fail("emitting %s", n.Name)
	return ";"
}

func (c *Context) LocalVarText(n *ir.LocalVar, init string) string {
	t := n.Type
	if t == nil && n.Init != nil {
		t = n.Init.ExprType()
	}
	decl := c.typeName(t) + " " + Local(n.Name)
	if init == "" {
		return decl + " = " + zero(t, c) + ";"
	}
	return decl + " = " + c.nullAs(t, n.Init, init) + ";"
}

// zero is t's zero value, which C's `{0}` is for every type C writes.
func zero(t *ir.Type, c *Context) string {
	switch t.Kind {
	case ir.TypeBool:
		return "false"
	case ir.TypeInt, ir.TypeEnum:
		return "0"
	case ir.TypeFloat:
		return "0.0"
	case ir.TypeUnit:
		if !multiBase(t) {
			return "0.0"
		}
	}
	if strings.HasSuffix(c.typeName(t), "*") {
		return "NULL"
	}
	return "(" + c.typeName(t) + "){0}"
}

func (c *Context) ReturnText(n *ir.Return, value string) string {
	if value == "" {
		return "return;"
	}
	return "return " + c.nullAs(c.ret, n.Value, value) + ";"
}

// nullAs is v, the rendering of e, where a value of t is wanted: a null
// literal is t's zero, which is a struct on C for an option, a list, a map
// and a function.
// paramFor is the parameter of fn the call's argument a, the i'th, is
// handed to, or nil.
func paramFor(fn *ir.Func, a ir.CallArg, i int) *ir.Param {
	for _, p := range fn.Params {
		if a.Name != "" && p.Name == a.Name {
			return p
		}
	}
	if a.Name == "" && i < len(fn.Params) {
		return fn.Params[i]
	}
	return nil
}

func (c *Context) nullAs(t *ir.Type, e ir.Expr, v string) string {
	if t != nil && ir.IsNullLiteral(e) {
		if c.ptrOption(t) {
			return "NULL"
		}
		if ir.PayloadEnum(t) != nil {
			return "((" + c.typeName(t) + "){0})"
		}
		switch t.Kind {
		case ir.TypeOption, ir.TypeList, ir.TypeMap, ir.TypeFunc:
			return "((" + c.typeName(t) + "){0})"
		}
	}
	return v
}

func (c *Context) BreakText(n *ir.Break) string {
	if n.Label != "" {
		c.fail("a labeled break")
	}
	return "break;"
}

func (c *Context) ContinueText() string { return "continue;" }

func (c *Context) ForHead(n *ir.For, iter string) string {
	switch n.IterKind {
	case ir.IterCondition:
		return "while (" + bare(iter) + ") {"
	case ir.IterForever:
		return "for (;;) {"
	case ir.IterCounted:
		return c.countedHead(n)
	case ir.IterElement, ir.IterIndexed:
		return c.listLoop(n)
	case ir.IterMapEntries:
		return c.mapLoop(n)
	}
	return c.fail("a loop over a %s", n.Iter.ExprType()) + "; {"
}

// countedHead is a loop over a sngl:seq sequence, which is never built: the
// number counts from Start to End by Step, and an ordinal beside it where the
// loop names two variables.
func (c *Context) countedHead(n *ir.For) string {
	k := n.Counted
	start, end := c.EvalExpr(k.Start), c.EvalExpr(k.End)
	cmp := " < "
	if k.Step < 0 {
		cmp = " > "
	}
	step := fmt.Sprintf(" += %d", k.Step)
	if n.Value == "" {
		// A loop naming no variable still counts.
		v := Local(n.Key)
		if n.Key == "" {
			v = fmt.Sprintf("__count%d", c.file.next())
		}
		return "for (int64_t " + v + " = " + start + ", " + v + "__end = " + end + "; " + v + cmp + v + "__end; " + v + step + ") {"
	}
	i, v := Local(n.Key), Local(n.Value)
	return "for (int64_t " + i + " = 0, " + v + " = " + start + ", " + v + "__end = " + end + "; " + v + cmp + v + "__end; " + i + "++, " + v + step + ") {"
}

func (c *Context) IfHead(_ *ir.If, cond string) string { return "if (" + bare(cond) + ") {" }
func (c *Context) ElseHead() string                    { return "} else {" }
func (c *Context) BlockEnd() string                    { return "}" }
func (c *Context) Indent() string                      { return "\t" }

// bare is cond without the parentheses an expression wraps itself in, which
// the head's own parentheses stand for: clang warns of a comparison in two
// (-Wparentheses-equality), taking it for an assignment meant.
func bare(cond string) string {
	if len(cond) < 2 || cond[0] != '(' || cond[len(cond)-1] != ')' {
		return cond
	}
	depth := 0
	for i := 0; i < len(cond); i++ {
		switch cond[i] {
		case '"', '\'':
			// A literal's parentheses are not the expression's.
			q := cond[i]
			for i++; i < len(cond) && cond[i] != q; i++ {
				if cond[i] == '\\' {
					i++
				}
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && i < len(cond)-1 {
				// The first parenthesis closes before the end: (a) == (b).
				return cond
			}
		}
	}
	return cond[1 : len(cond)-1]
}

// Catch is body under a catch point (sngl_catch), which a raise in it jumps
// back to with the error: the point is off the stack by then, and the handler
// runs in its place and the block ends. Body is the whole of a handler's
// body, so nothing leaves it but a return, which takes the point off first.
func (c *Context) Catch(n *ir.If, body []string) []string {
	h := n.Catch
	at := fmt.Sprintf("__catch%d", c.file.next())
	lines := c.guard(at, body)
	lines = append(lines, "\t\tsngl_catching = "+at+".up;", "\t} else {")
	lines = append(lines, caught(at)...)
	if h.Func != nil {
		lines = append(lines, irwalk.IndentLines("\t\t", c.handle(h, at+".message", at+".kind"))...)
	}
	return append(lines, "\t}", "}")
}

// Finally is body and then finally, under a catch point as Catch's is: a
// raise in body runs finally and raises again, on to the point around it.
// Body holds no return (ir.If.Finally).
func (c *Context) Finally(_ *ir.If, body, finally []string) []string {
	at := fmt.Sprintf("__finally%d", c.file.next())
	lines := c.guard(at, body)
	lines = append(lines, "\t\t"+"sngl_catching = "+at+".up;")
	lines = append(lines, irwalk.IndentLines("\t\t", finally)...)
	lines = append(lines, "\t} else {")
	lines = append(lines, caught(at)...)
	lines = append(lines, irwalk.IndentLines("\t\t", finally)...)
	return append(lines, "\t\tsngl_raise("+at+".message, "+at+".kind);", "\t}", "}")
}

// caught is the line a catch point's handler starts with, taking the error
// the raise left: in sngl_raised, not the point, which is a local of the
// function that set it, whose value a longjmp leaves indeterminate where
// anything wrote it after the setjmp.
func caught(at string) []string {
	return []string{"\t\t" + at + ".message = sngl_raised.message;", "\t\t" + at + ".kind = sngl_raised.kind;"}
}

// guard opens a block pushing the catch point at and running body until it
// raises, with each return in body taking the point off before it leaves.
// A return is a line of its own, and the point it takes off is the innermost
// one, so a nested guard's lines come first.
func (c *Context) guard(at string, body []string) []string {
	lines := []string{
		"{",
		"\tsngl_catch " + at + " = {.up = sngl_catching};",
		"\tsngl_catching = &" + at + ";",
		"\tif (setjmp(" + at + ".at) == 0) {",
	}
	for _, l := range body {
		if t := strings.TrimLeft(l, "\t"); t == "return;" || strings.HasPrefix(t, "return ") {
			lines = append(lines, "\t\t"+l[:len(l)-len(t)]+"sngl_catching = "+at+".up;")
		}
		lines = append(lines, "\t\t"+l)
	}
	return lines
}

// Match is nil, which asks for the if chain: a C switch takes integers
// alone, and an enum is the one place it would serve.
func (c *Context) Match(*ir.Match, string, [][]string, [][]string) []string { return nil }

func (c *Context) MutTargetIdent(n *ir.Ident) string { return c.Ident(n) }
func (c *Context) MutTargetField(n *ir.Select) string {
	return Local(n.Field)
}

// MutTargetSelect is a field written: through `->` where what holds it is a
// pointer, as a read is (Select).
func (c *Context) MutTargetSelect(n *ir.Select, operand string) string {
	return c.Select(n, operand)
}

func (c *Context) StmtPrefix(ir.Stmt) []string { return nil }

// StmtStart opens s's frame of the temporaries that keep its expressions'
// order (irwalk.Sequencer).
func (c *Context) StmtStart(ir.Stmt) { c.file.ordered = append(c.file.ordered, nil) }

// StmtEnd declares the temporaries rendering s assigned, ahead of it.
func (c *Context) StmtEnd(ir.Stmt) []string {
	f := c.file
	n := len(f.ordered)
	if n == 0 {
		return nil
	}
	temps := f.ordered[n-1]
	f.ordered = f.ordered[:n-1]
	return temps
}

// sequenced keeps the order of n's operands, left then right, where either
// calls something, which may change what the other reads or print before
// it: C evaluates a call's arguments in no order it promises, and
// gcc evaluated an interpolation's last part first. Each is assigned to a
// temporary of type t, declared ahead of the statement (StmtEnd), the
// assignments joined by the comma operator, which sequences them. It
// answers the two assignments and the two temporaries, and false where
// order cannot matter or no statement is being rendered to declare them
// ahead of.
func (c *Context) sequenced(n *ir.Binary, t, left, right string) ([4]string, bool) {
	f := c.file
	if len(f.ordered) == 0 || !calls(n.Left) && !calls(n.Right) || constant(n.Left) || constant(n.Right) {
		return [4]string{}, false
	}
	var out [4]string
	for i, v := range []string{left, right} {
		f.seq++
		name := fmt.Sprintf("__snglPart%d", f.seq)
		f.ordered[len(f.ordered)-1] = append(f.ordered[len(f.ordered)-1], t+" "+name+";")
		out[i], out[2+i] = name+" = "+v, name
	}
	return out, true
}

// calls reports whether e calls a function, which may do anything.
func calls(e ir.Expr) bool {
	found := false
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		if _, ok := x.(*ir.Call); ok {
			found = true
		}
		return nil
	})
	return found
}

// constant reports whether e is a literal, which nothing changes.
func constant(e ir.Expr) bool {
	_, ok := e.(*ir.Literal)
	return ok
}

func (c *Context) Scoped(name string) irwalk.Renderer { return c.WithLocal(name) }

// --- C ---

// CType is t as C spells it: itself.
func (c *Context) CType(t *ir.CType) string { return t.String() }

// CConvert is the conversion to a C type. A string is already NUL-terminated,
// so it crosses as its bytes, with no copy; a number is a cast.
func (c *Context) CConvert(x *ir.CConvert) string {
	operand := c.EvalExpr(x.Operand)
	switch x.Kind {
	case ir.CConvertString:
		// A copy the callee may keep, which the program frees as it frees
		// Go's C.CString: CMarshal and ReleaseTemps hold it as an owned
		// c.str. A copy the arena could stand in for is an optimization
		// that waits for strings to be owned on C (design/ownership.md).
		return "sngl_cstring(" + operand + ")"
	case ir.CConvertNumber, ir.CConvertPointer:
		// A number made a pointer -- a WM_NOTIFY's LPARAM the NMHDR it
		// points to -- goes through the integer type its parameter names
		// first: a SNGL int is 64 bits, wider than a 32-bit pointer.
		if x.Via != nil && x.Via.CCastFromInt() {
			return "(" + x.To.String() + ")(" + x.Via.CIntOf() + ")(" + operand + ")"
		}
		return "(" + x.To.String() + ")(" + operand + ")"
	case ir.CConvertCallback, ir.CConvertContext, ir.CConvertRelease:
		// CCall writes all three from the closure.
		return operand
	case ir.CConvertUTF16:
		// CMarshal unwrapped an option: what is left is a string or null.
		if t := x.Operand.ExprType(); t == nil || t.Kind != ir.TypeString {
			return "NULL"
		}
		return "(" + x.To.String() + ")sngl_utf16(" + operand + ")"
	case ir.CConvertStruct:
		return c.cStruct(x.Type) + "(" + operand + ")"
	case ir.CConvertHeld:
		// Kept as it crosses, deep, as a holder keeps what it stores: C
		// holds it past the turn, until it calls the release CCall hands
		// it (heldRelease).
		return c.heapCopy(c.typeName(x.Type), c.keep(x.Type, operand))
	}
	return c.fail("crossing to %s", x.To)
}

// cStruct is the helper writing t, a struct of the program's declaring a C
// struct, as that C struct: field by field, each as its alias's typedef or
// the C scalar it is, so the program holds its own struct and C is handed
// its own.
func (c *Context) cStruct(t *ir.Type) string {
	sd := ir.CStructOf(t)
	if sd == nil {
		return c.fail("crossing a %s as a C struct", t)
	}
	from, to := c.typeName(t), sd.Foreign.C.String()
	fn := "sngl_to_" + strings.TrimPrefix(from, "s_")
	var fields []string
	for _, f := range sd.Fields {
		v := "v." + Local(f.Name)
		switch {
		case f.Type.Kind == ir.TypeBool:
			v = "(" + v + " ? 1 : 0)"
		// A string is valid for the call the struct is handed to: its
		// bytes, which a NUL ends, or its UTF-16 in the turn's arena, as an
		// argument's is. Written without const, it fits a field C declares
		// either way (LVITEMW's LPWSTR).
		case f.Type.Kind == ir.TypeString && f.CAlias != nil && f.CAlias.C != nil:
			v = "(unsigned short *)sngl_utf16(" + v + ")"
		case f.Type.Kind == ir.TypeString:
			v = "(char *)" + v + ".ptr"
		// A handle, or an option of a `*` one, is the pointer it holds.
		case ir.IsCPointerOption(f.Type) || ir.CTypeOf(f.Type) != nil && ir.CStructOf(f.Type) == nil:
		// A C struct it holds -- NMDATETIMECHANGE's NMHDR and SYSTEMTIME --
		// is written by its own helper.
		case ir.CStructOf(f.Type) != nil:
			v = c.cStruct(f.Type) + "(" + v + ")"
		case f.CAlias != nil && f.CAlias.C != nil:
			v = "(" + f.CAlias.C.String() + ")" + v
		case f.Type.Kind == ir.TypeFloat:
			v = "(double)" + v
		default:
			v = "(int)" + v
		}
		fields = append(fields, "."+f.Name+" = "+v)
	}
	c.file.helper(fn, "static "+to+" "+fn+"("+from+" v) {\n\treturn ("+to+"){"+strings.Join(fields, ", ")+"};\n}\n")
	return fn
}

// fromCStruct is the helper reading a C struct back as t, the program's
// struct declaring it: field by field, as cStruct wrote it.
func (c *Context) fromCStruct(t *ir.Type) string {
	sd := ir.CStructOf(t)
	if sd == nil {
		return c.fail("crossing a C struct back as a %s", t)
	}
	to, from := c.typeName(t), sd.Foreign.C.String()
	fn := "sngl_from_" + strings.TrimPrefix(to, "s_")
	var fields []string
	kept := false
	for _, f := range sd.Fields {
		v := "v." + f.Name
		switch {
		case f.Type.Kind == ir.TypeBool:
			v = "(" + v + " != 0)"
		// A string is not read back: the program's struct keeps its own,
		// which C had for the call (fromCStructKeeps).
		case f.Type.Kind == ir.TypeString:
			v, kept = "o."+Local(f.Name), true
		// A C struct it holds is read by its own helper, which keeps the
		// strings of the one the program handed in as this one does.
		case ir.CStructOf(f.Type) != nil && fromCStructKeeps(f.Type):
			v, kept = c.fromCStruct(f.Type)+"("+v+", o."+Local(f.Name)+")", true
		case ir.CStructOf(f.Type) != nil:
			v = c.fromCStruct(f.Type) + "(" + v + ")"
		default:
			v = "(" + c.typeName(f.Type) + ")" + v
		}
		fields = append(fields, "."+Local(f.Name)+" = "+v)
	}
	params := from + " v"
	if kept {
		params += ", " + to + " o"
	}
	c.file.helper(fn, "static "+to+" "+fn+"("+params+") {\n\treturn ("+to+"){"+strings.Join(fields, ", ")+"};\n}\n")
	return fn
}

// fromCStructKeeps reports whether fromCStruct's helper for t takes the
// struct the program handed C beside the one C wrote: a string field of it,
// or of a C struct it holds, which is not read back from C.
func fromCStructKeeps(t *ir.Type) bool {
	sd := ir.CStructOf(t)
	return sd != nil && slices.ContainsFunc(sd.Fields, func(f *ir.StructField) bool {
		return f.Type.Kind == ir.TypeString || fromCStructKeeps(f.Type)
	})
}

// CCall is a call of the C function c names, whose number comes back as the
// SNGL number it is. A callback is the dispatcher for its C signature, handed
// the closure on the heap as its context: released by C through the
// `#[c.release]` parameter, kept for good where the parameter is
// `#[retains]`, and freed after the call otherwise.
func (c *Context) CCall(n *ir.Call, args []string) string {
	fn := n.Func
	off := len(args) - len(fn.Params)
	if slices.ContainsFunc(fn.Params, func(p *ir.Param) bool { return p.Type != nil && p.Type.Kind == ir.TypeRef }) {
		return c.refCall(n, args)
	}
	if off >= 0 {
		for i, p := range fn.Params {
			if p.CStatic {
				args = slices.Clone(args)
				args[off+i] = c.staticCallback(fn, i, n.Args[off+i].Value)
			}
		}
	}
	if off >= 0 {
		args = c.intPointerArgs(fn, args[:off:off], args[off:])
	}
	cb := slices.IndexFunc(fn.Params, func(p *ir.Param) bool {
		return !p.CompilerPassed() && !p.CStatic && p.Type != nil && p.Type.Kind == ir.TypeFunc
	})
	ctx := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return p.CContext })
	rel := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return p.CRelease })
	var free string
	if cb >= 0 && off >= 0 {
		if fn.Params[cb].CFuncType == nil || rel >= 0 && fn.Params[rel].CFuncType == nil {
			return c.decline("calling %s, whose function parameters name no C type (#[c.type])", fn.Foreign.Name)
		}
		if ctx < 0 {
			return c.decline("calling %s, whose callback takes no context", fn.Foreign.Name)
		}
		args = slices.Clone(args)
		// The argument is rendered already, as the function value it is: a
		// callback's conversion is its operand.
		vt := n.Args[cb].Value.ExprType()
		if cv, ok := n.Args[cb].Value.(*ir.CConvert); ok {
			vt = cv.Operand.ExprType()
		}
		if vt == nil || vt.Kind != ir.TypeFunc {
			vt = fn.Params[cb].Type
		}
		dispatch, heap := c.cCallback(fn.Params[cb].Type.Sig, args[off+cb], vt)
		args[off+cb] = c.fnPointer(fn, cb, dispatch)
		switch {
		case rel >= 0:
			args[off+ctx] = "(" + c.typeName(fn.Params[ctx].Type) + ")" + heap
			args[off+rel] = c.fnPointer(fn, rel, "sngl_release")
		case fn.Params[cb].Transfer == ir.TransferRetains:
			args[off+ctx] = "(" + c.typeName(fn.Params[ctx].Type) + ")" + heap
		default:
			free = heap
		}
	}
	if held := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return p.CAlias.IsCHeld() }); held >= 0 && rel >= 0 && off >= 0 {
		if fn.Params[rel].CFuncType == nil {
			return c.decline("calling %s, whose release names no C type (#[c.type])", fn.Foreign.Name)
		}
		// The argument's type, as the callback's above: the declaration may be
		// the library's frozen one, whose struct a rename on the build's copy
		// did not reach (canonicalStructs).
		ht := fn.Params[held].Type
		if held < len(n.Args) {
			v := n.Args[held].Value
			if cv, ok := v.(*ir.CConvert); ok {
				v = cv.Operand
			}
			if vt := v.ExprType(); vt != nil && vt.Kind == ir.TypeStruct {
				ht = vt
			}
		}
		args = slices.Clone(args)
		args[off+rel] = c.fnPointer(fn, rel, c.heldRelease(ht))
	}
	call := ir.CCallExpr(fn, args)
	if free != "" {
		return c.fail("a callback %s holds for the call alone", fn.Foreign.Name)
	}
	if fn.ReturnCAlias.IsCHeld() && fn.Return != nil && fn.Return.Kind == ir.TypeOption {
		return c.heldRead(fn.Return) + "(" + call + ")"
	}
	if fn.Return != nil {
		switch fn.Return.Kind {
		case ir.TypeInt, ir.TypeFloat:
			return "(" + c.typeName(fn.Return) + ")" + call
		case ir.TypeBool:
			return "(" + call + " != 0)"
		case ir.TypeString:
			// The C function keeps what it returns, but where it hands it
			// over (#[c.frees]); the copy is the turn's.
			return c.cStringOut(fn.CFrees, false) + "(" + call + ")"
		case ir.TypeOption:
			if fn.Return.Elems[0].Kind == ir.TypeString && fn.ReturnCAlias == nil {
				return c.cStringOut(fn.CFrees, true) + "(" + call + ")"
			}
		case ir.TypeStruct:
			// Read back field by field; one holding a string the checker
			// refuses, there being no string of the program's to keep.
			if ir.CStructOf(fn.Return) != nil {
				if fromCStructKeeps(fn.Return) {
					return c.fail("a C struct holding a string returned by %s", fn.Foreign.Name)
				}
				return c.fromCStruct(fn.Return) + "(" + call + ")"
			}
		}
	}
	return call
}

// intPointerArgs is head, then args, each argument for a parameter of fn
// C takes as an integer typedef (ir.Param.CIntPointer) cast to it.
func (c *Context) intPointerArgs(fn *ir.Func, head, args []string) []string {
	out := append(head, args...)
	for i, p := range fn.Params {
		if p.CIntPointer() {
			out[len(head)+i] = "(" + p.CFuncType.String() + ")" + out[len(head)+i]
		}
	}
	return out
}

// cStringOut is the function copying a `char *` C answered or stored into
// the turn: the empty string for NULL, or for an option none. Where frees
// names the C function freeing it (#[c.frees]), the pointer is handed to it
// once copied, but NULL. A copy alone is the runtime's sngl_string_copy.
func (c *Context) cStringOut(frees string, option bool) string {
	if frees == "" && !option {
		return "sngl_string_copy"
	}
	free := ""
	if frees != "" {
		free = "\tif (p != NULL) {\n\t\t" + frees + "((void *)p);\n\t}\n"
	}
	if !option {
		name := "sngl_freed_" + frees
		c.file.helper(name, "static sngl_string "+name+"(const char *p) {\n\tsngl_string s = sngl_string_copy(p);\n"+free+"\treturn s;\n}\n")
		return name
	}
	t := c.optType(&ir.Type{Kind: ir.TypeOption, Elems: []*ir.Type{ir.TypString}})
	name := "sngl_cstring_opt"
	if frees != "" {
		name = "sngl_freed_opt_" + frees
	}
	c.file.helper(name, "static "+t+" "+name+"(const char *p) {\n\tif (p == NULL) {\n\t\treturn ("+t+"){0};\n\t}\n\t"+t+" o = {true, sngl_string_copy(p)};\n"+free+"\treturn o;\n}\n")
	return name
}

// refCall is a call of a C function taking a `ref`, a pointer it fills: a
// function the file defines, which takes each argument as the call renders
// it, hands C the address of a C temp for each ref, and answers what
// lower.RefParams made of the call -- the one value C wrote, or the record
// of the result and each one. A ref is to a scalar, or to a struct declaring
// a C struct, which crosses into its temp and back field by field; the
// function takes no callback.
func (c *Context) refCall(n *ir.Call, args []string) string {
	fn := n.Func
	off := len(args) - len(fn.Params)
	if off < 0 {
		return c.fail("calling %s, which takes a ref, with a receiver", fn.Foreign.Name)
	}
	name := "sngl_ccall_" + strings.ReplaceAll(fn.Foreign.Name, ".", "_") + "_" + mangle(fn.Name)
	var params, cargs, outs []string
	var body strings.Builder
	for i, p := range fn.Params {
		a := fmt.Sprintf("a%d", i)
		switch {
		case p.Type.Kind == ir.TypeRef:
			elem := p.Type.Elems[0]
			params = append(params, c.typeName(elem)+" "+a)
			cargs = append(cargs, fmt.Sprintf("&t%d", i))
			// A string C stores and hands over (#[c.frees]): C is handed a
			// pointer to NULL to fill.
			if elem.Kind == ir.TypeString {
				fmt.Fprintf(&body, "\tchar *t%d = NULL;\n\t(void)%s;\n", i, a)
				outs = append(outs, fmt.Sprintf("%s(t%d)", c.cStringOut(p.CFrees, false), i))
				continue
			}
			// An option of a pointer handle C stores one through -- GLib's
			// GError ** -- is the pointer, as such an option is: C is handed
			// a pointer to NULL, as GLib asks of an error, and what it stored
			// is the option, null where it stored nothing.
			if ir.IsCPointerOption(elem) {
				fmt.Fprintf(&body, "\t%s t%d = NULL;\n\t(void)%s;\n", ir.CTypeOf(elem.Elems[0]).String(), i, a)
				outs = append(outs, fmt.Sprintf("(%s)t%d", c.typeName(elem), i))
				continue
			}
			if sd := ir.CStructOf(elem); sd != nil {
				fmt.Fprintf(&body, "\t%s t%d = %s(%s);\n", sd.Foreign.C.String(), i, c.cStruct(elem), a)
				if fromCStructKeeps(elem) {
					outs = append(outs, fmt.Sprintf("%s(t%d, %s)", c.fromCStruct(elem), i, a))
				} else {
					outs = append(outs, fmt.Sprintf("%s(t%d)", c.fromCStruct(elem), i))
				}
				continue
			}
			switch elem.Kind {
			case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
			default:
				return c.fail("calling %s, whose ref is to a %s", fn.Foreign.Name, elem)
			}
			ct := staticCType(elem)
			if p.CAlias != nil && p.CAlias.C != nil {
				ct = p.CAlias.C.String()
			}
			fmt.Fprintf(&body, "\t%s t%d = (%s)%s;\n", ct, i, ct, a)
			out := fmt.Sprintf("(%s)t%d", c.typeName(elem), i)
			if elem.Kind == ir.TypeBool {
				out = fmt.Sprintf("(t%d != 0)", i)
			}
			outs = append(outs, out)
		case p.Type.Kind == ir.TypeFunc || p.CompilerPassed() || p.CAlias.IsCHeld():
			return c.fail("calling %s, which takes a ref and a callback or a held value", fn.Foreign.Name)
		default:
			ct := "void *"
			switch t := p.Type; {
			case p.CAlias != nil && p.CAlias.C != nil:
				ct = p.CAlias.C.String()
			case t.Kind == ir.TypeOption && len(t.Elems) == 1 && ir.CTypeOf(t.Elems[0]) != nil:
				ct = ir.CTypeOf(t.Elems[0]).String()
			case t.Kind == ir.TypeOption:
				ct = "const char *"
			case ir.CStructOf(t) != nil:
				// Crossed already, as lower.CMarshal converts it.
				ct = ir.CStructOf(t).Foreign.C.String()
			case t.Kind == ir.TypeStruct || t.Kind == ir.TypeString:
				ct = staticCType(t)
				// A node is the pointer the platform spells it as.
				if ir.CTypeOf(t) == nil && t.Kind == ir.TypeStruct {
					ct = c.typeName(t)
				}
			case t.Kind == ir.TypeInt || t.Kind == ir.TypeFloat || t.Kind == ir.TypeBool:
				ct = staticCType(t)
			default:
				ct = c.typeName(t)
			}
			params = append(params, ct+" "+a)
			cargs = append(cargs, a)
		}
	}
	call := ir.CCallExpr(fn, c.intPointerArgs(fn, nil, cargs))
	void := fn.Return == nil || fn.Return.Kind == ir.TypeVoid
	ret := c.typeName(n.Type)
	switch {
	case void && len(outs) == 1:
		fmt.Fprintf(&body, "\t%s;\n\treturn %s;\n", call, outs[0])
	default:
		def, _ := n.Type.Decl.(*ir.StructDef)
		if def == nil {
			return c.fail("calling %s, whose outs lowering gave no record", fn.Foreign.Name)
		}
		c.defineStruct(def)
		fmt.Fprintf(&body, "\t%s r;\n", ret)
		k := 0
		for _, f := range def.Fields {
			if f.Name == "result" {
				v := call
				switch fn.Return.Kind {
				case ir.TypeBool:
					v = "(" + call + " != 0)"
				case ir.TypeInt, ir.TypeFloat:
					v = "(" + c.typeName(fn.Return) + ")" + call
				case ir.TypeString:
					v = c.cStringOut(fn.CFrees, false) + "(" + call + ")"
				case ir.TypeOption:
					if fn.Return.Elems[0].Kind == ir.TypeString && fn.ReturnCAlias == nil {
						v = c.cStringOut(fn.CFrees, true) + "(" + call + ")"
					}
				}
				fmt.Fprintf(&body, "\tr.%s = %s;\n", Local(f.Name), v)
			}
		}
		if void {
			fmt.Fprintf(&body, "\t%s;\n", call)
		}
		for _, f := range def.Fields {
			if f.Name != "result" {
				fmt.Fprintf(&body, "\tr.%s = %s;\n", Local(f.Name), outs[k])
				k++
			}
		}
		body.WriteString("\treturn r;\n")
	}
	c.file.helper(name, "static "+ret+" "+name+"("+strings.Join(params, ", ")+") {\n"+body.String()+"}\n")
	return name + "(" + strings.Join(args, ", ") + ")"
}

// staticCallback is the function C is handed for parameter i of fn, a
// callback it calls with no context (sngl:x/c's #[static]): one the file
// defines for the package's function arg names, taking each argument as C
// passes it and calling that function with the SNGL value it is. Marked
// #[c.outs], it writes each field of what the function answers through the
// pointer C handed it for one, where that is not NULL.
func (c *Context) staticCallback(fn *ir.Func, i int, arg ir.Expr) string {
	p := fn.Params[i]
	callee := ir.StaticCallee(arg)
	if callee == nil {
		return c.fail("calling %s, whose static callback %s is handed no function of a package", fn.Foreign.Name, p.Name)
	}
	sig := p.Type.Sig
	name := "sngl_static_" + funcName(callee)
	if p.CConvention != "" {
		name = "sngl_static_" + ir.CIdentToken(p.CConvention) + "_" + funcName(callee)
	}
	var params, args []string
	for j, sp := range sig.Params {
		a := fmt.Sprintf("a%d", j)
		params = append(params, aliasedCType(sp.Type, sp.CAlias)+" "+a)
		switch {
		case sp.Type.Kind == ir.TypeString:
			args = append(args, "sngl_string_copy("+a+")")
		case sp.Type.Kind == ir.TypeBool:
			args = append(args, "("+a+" != 0)")
		case ir.CTypeOf(sp.Type) != nil:
			args = append(args, a)
		default:
			args = append(args, "("+c.typeName(sp.Type)+")"+a)
		}
	}
	call := funcName(callee) + "(" + strings.Join(args, ", ") + ")"
	ret, body := "void", "\t"+call+";\n"
	switch r := sig.Return; {
	case p.COuts:
		sd, _ := r.Decl.(*ir.StructDef)
		body = "\t" + c.typeName(r) + " r = " + call + ";\n"
		for k, f := range sd.Fields {
			o := fmt.Sprintf("o%d", k)
			params = append(params, staticCType(f.Type)+" *"+o)
			body += "\tif (" + o + " != NULL) {\n\t\t*" + o + " = (" + staticCType(f.Type) + ")r." + Local(f.Name) + ";\n\t}\n"
		}
	case r != nil && r.Kind != ir.TypeVoid:
		ret = aliasedCType(r, sig.ReturnCAlias)
		body = "\t" + ret + " r = (" + ret + ")" + call + ";\n"
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	// What C calls back is a turn of the program's, as a dispatcher's is.
	body = "\tsngl_mark m = sngl_call_enter();\n" + body + "\tsngl_call_leave(m);\n"
	if ret != "void" {
		body += "\treturn r;\n"
	}
	conv := ""
	if p.CConvention != "" {
		conv = p.CConvention + " "
	}
	c.file.helper(name, "static "+ret+" "+conv+name+"("+strings.Join(params, ", ")+") {\n"+body+"}\n")
	// A function whose convention is written is handed as it is: C holds
	// it to the typedef, convention and parameters, which a cast through
	// `void (*)(void)` would let differ unseen -- a __stdcall window
	// procedure of 32-bit Windows called as cdecl.
	if p.CFuncType != nil && p.CConvention == "" {
		return c.fnPointer(fn, i, name)
	}
	return name
}

// aliasedCType is the C type t crosses a static callback as, written with
// alias where its signature names one (`UINT`), and staticCType's otherwise.
func aliasedCType(t *ir.Type, alias *ir.Alias) string {
	if alias != nil && alias.C != nil {
		return alias.C.String()
	}
	return staticCType(t)
}

// staticCType is the C type t crosses a static callback as: a handle as the
// C type it names, a string as C's own, a bool as an int.
func staticCType(t *ir.Type) string {
	if ct := ir.CTypeOf(t); ct != nil {
		return ct.String()
	}
	switch t.Kind {
	case ir.TypeString:
		return "const char *"
	case ir.TypeFloat:
		return "double"
	}
	return "int"
}

// heldRelease is the function C calls with the pointer a held value of t
// crossed as (ir.CConvertHeld), once it lets the value go: it drops what the
// copy kept, which the turn gives back once it ends, and frees the copy.
func (c *Context) heldRelease(t *ir.Type) string {
	name := "sngl_held_release_" + typeToken(t)
	tn := c.typeName(t)
	body := "\tfree(p);\n"
	if drop := c.drop(t, "(*("+tn+" *)p)"); drop != "" {
		body = "\t" + drop + "\n" + body
	}
	c.file.helper(name, "static void "+name+"(void *p) {\n"+body+"}\n")
	return name
}

// heldRead is the function reading back what a C function answering opt,
// an `option[c.held[T]]`, answers: the T the pointer is to, which C holds
// and the turn reads, or null for NULL.
func (c *Context) heldRead(opt *ir.Type) string {
	t := opt.Elems[0]
	name := "sngl_held_" + typeToken(t)
	tn, on := c.typeName(t), c.typeName(opt)
	body := "\t" + on + " o = {0};\n\tif (p != NULL) {\n\t\to.ok = true;\n\t\to.value = *(" + tn + " *)p;\n\t}\n\treturn o;\n"
	if c.ptrOption(opt) {
		body = "\treturn p != NULL ? *(" + tn + " *)p : NULL;\n"
	}
	c.file.helper(name, "static "+on+" "+name+"(void *p) {\n"+body+"}\n")
	return name
}

// fnPointer is f, a function the file defines, cast to the C type parameter
// i of fn names (#[c.type]): C converts no function pointer to another
// unasked.
//
// Through `void (*)(void)`, which C's compilers take as matching every
// function type: a release C calls with more than the context, as GLib's
// GClosureNotify is, is one taking the context alone, which every C ABI
// calls alike, and GLib relies on.
func (c *Context) fnPointer(fn *ir.Func, i int, f string) string {
	return "(" + fn.Params[i].CFuncType.String() + ")(void (*)(void))" + f
}

// defineStruct defines sd's C struct in the file, after the structs its
// fields hold by value.
func (c *Context) defineStruct(sd *ir.StructDef) {
	name := Decl(&sd.DeclInfo)
	if c.file.defined(name) {
		return
	}
	// Defined before its fields are named, so a field's type naming this
	// struct again finds it: C would refuse such a struct anyway.
	c.file.define(name, "")
	var deps []string
	var b strings.Builder
	fmt.Fprintf(&b, "typedef struct %s {\n", name)
	for _, f := range sd.Fields {
		t := c.typeName(f.Type)
		// A field held through a pointer still names its type, which C
		// wants declared first.
		deps = append(deps, t)
		fmt.Fprintf(&b, "\t%s %s;\n", t, Local(f.Name))
	}
	if len(sd.Fields) == 0 {
		// C refuses a struct with no members.
		b.WriteString("\tchar unused;\n")
	}
	fmt.Fprintf(&b, "} %s;\n", name)
	c.file.redefine(name, b.String(), deps...)
}

// FnType names the C struct a function value of t is.
func (c *Context) FnType(t *ir.Type) string { return c.fnType(t) }

// Define defines the type name in the file, after the types deps names
// that it holds by value.
func (c *Context) Define(name, text string, deps ...string) { c.file.define(name, text, deps...) }

// Helper defines a function the code calls, once by name, ahead of every
// function of the program's.
func (c *Context) Helper(name, text string) { c.file.helper(name, text) }

// Include includes a header, `<name>` where system.
func (c *Context) Include(name string, system bool) { c.file.include(name, system) }

// Callback is value, a function value, as the dispatcher C calls for sig
// and the context it hands back, the closure on the heap; sngl_release, which
// the file defines, frees it.
func (c *Context) Callback(sig *ir.FuncSig, value ir.Expr) (fn, ctx string) {
	t := value.ExprType()
	if t == nil || t.Sig == nil {
		// A value a pass made untyped -- a closure handed an intrinsic --
		// is of the type the callee takes.
		t = &ir.Type{Kind: ir.TypeFunc, Sig: sig}
	}
	return c.cCallback(sig, c.EvalExpr(value), t)
}

// isLocalVar reports whether v is declared by a `var` statement somewhere in
// the program, which C writes as a local of the function it is in.
func (c *Context) isLocalVar(v *ir.Var) bool {
	if c.file.localVars == nil {
		c.file.localVars = map[*ir.Var]bool{}
		if c.Ctx != nil && c.Ctx.Pkg != nil {
			_ = ir.Walk(c.Ctx.Pkg, func(n ir.Node) error {
				if lv, ok := n.(*ir.LocalVar); ok && lv.Sym != nil {
					c.file.localVars[lv.Sym] = true
				}
				return nil
			})
		}
	}
	return c.file.localVars[v]
}
