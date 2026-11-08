package c

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/irwalk"
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

// TypeName is t as C declares a value of it.
func (c *Context) TypeName(t *ir.Type) string { return c.typeName(t) }

func (c *Context) typeName(t *ir.Type) string {
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
		case ir.TypeMap:
			return c.mapType(t)
		case ir.TypeStruct:
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

func (c *Context) Literal(n *ir.Literal) string { return literal(n) }

func literal(n *ir.Literal) string {
	if n.Type == nil {
		return n.Value
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
		if c.locals[n.Name] {
			return Local(n.Name)
		}
		if c.isPackageVar(sym) {
			return Decl(&sym.DeclInfo)
		}
		return Local(n.Name)
	case *ir.Func:
		if n.Type == nil || n.Type.Kind != ir.TypeFunc {
			return c.fail("the function %s used as a value of type %s", n.Name, n.Type)
		}
		return c.funcValue(sym, n.Type)
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
	lt := n.Left.ExprType()
	if lt != nil && lt.Kind == ir.TypeString {
		switch n.Op {
		case ast.BinAdd:
			return "sngl_concat(" + left + ", " + right + ")"
		case ast.BinEq:
			return "sngl_string_eq(" + left + ", " + right + ")"
		case ast.BinNeq:
			return "!sngl_string_eq(" + left + ", " + right + ")"
		case ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte:
			return "(sngl_string_cmp(" + left + ", " + right + ") " + n.Op.String() + " 0)"
		}
	}
	if lt != nil && lt.Kind == ir.TypeStruct && (n.Op == ast.BinEq || n.Op == ast.BinNeq) {
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
// fields each, through a function the file defines for it, since C compares
// no struct.
func (c *Context) equal(t *ir.Type, a, b string) string {
	switch t.Kind {
	case ir.TypeString:
		return "sngl_string_eq(" + a + ", " + b + ")"
	case ir.TypeStruct:
		if !isProgramStruct(t) {
			break
		}
		sd := t.Decl.(*ir.StructDef)
		name := c.typeName(t)
		fn := name + "__eq"
		var parts []string
		for _, f := range sd.Fields {
			parts = append(parts, c.equal(f.Type, "a."+Local(f.Name), "b."+Local(f.Name)))
		}
		if len(parts) == 0 {
			parts = []string{"true"}
		}
		c.file.helper(fn, "static bool "+fn+"("+name+" a, "+name+" b) {\n\treturn "+strings.Join(parts, " && ")+";\n}\n")
		return fn + "(" + a + ", " + b + ")"
	}
	return "(" + a + " == " + b + ")"
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
	if t != nil && c.isPointer(t) {
		// A box, a C pointer, a record a platform holds by pointer.
		return operand + "->" + Local(n.Field)
	}
	if t == nil || t.Kind != ir.TypeStruct {
		return c.fail("selecting %s of %s", n.Field, t)
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
		return "((" + name + " *)" + c.heapCopy(name, c.structLit(n, fields)) + ")"
	}
	return c.structLit(n, fields)
}

func (c *Context) structLit(n *ir.StructLit, fields []string) string {
	name := Decl(&n.Type.Decl.(*ir.StructDef).DeclInfo)
	var parts []string
	for i, f := range n.Fields {
		if f.Spread || f.Name == "" {
			return c.fail("a spread into %s", n.Type)
		}
		parts = append(parts, "."+Local(f.Name)+" = "+fields[i])
	}
	if len(parts) == 0 {
		return "(" + name + "){0}"
	}
	return "(" + name + "){" + strings.Join(parts, ", ") + "}"
}

// Box defines sd, a box lower.NoRef made, as the struct it is.
func (c *Context) Box(sd *ir.StructDef) {
	name := Decl(&sd.DeclInfo)
	elem := c.typeName(sd.Fields[0].Type)
	c.file.define(name, "typedef struct "+name+" {\n\t"+elem+" value;\n} "+name+";\n", elem)
}

func (c *Context) Spread(*ir.Spread, string) string { return c.fail("a spread") }

func (c *Context) MatchExpr(n *ir.MatchExpr) string {
	return c.EvalExpr(n.TernaryChain())
}

// --- Calls ---

func (c *Context) Call(n *ir.Call) string {
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
	if c.Native != nil {
		if out, ok := c.Native(c, n); ok {
			return out
		}
	}
	if n.Func.IsCCast() && len(n.Args) == 1 {
		return c.CConvert(ir.NewCConvert(n.Func, n.Args[0].Value))
	}
	if out, ok := c.listIntrinsic(n); ok {
		return out
	}
	if out, ok := c.stringIntrinsic(n); ok {
		return out
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
	}
	if n.Func.Foreign.Path == ir.CPath && !n.Func.Foreign.Marked {
		return c.CCall(n, args)
	}
	if n.Func.Foreign.Path != "" {
		return c.fail("calling %s, which is %s's", n.Func.Name, n.Func.Foreign.Path)
	}
	if n.Receiver != nil {
		return c.fail("calling %s on a component", n.Func.Name)
	}
	if n.Func.Intrinsic != "" && len(n.Func.Block) == 0 {
		return c.fail("the intrinsic %s", n.Func.Intrinsic)
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
	// A widget, a C pointer: the cast C writes between two of them.
	if c.isPointer(to) && (c.isPointer(from) || from.Kind == ir.TypeComponent) {
		return "(" + c.typeName(to) + ")(" + operand + ")"
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
		}
		return c.fail("the text of a %s", from)
	}
	switch to.Kind {
	case ir.TypeInt, ir.TypeFloat:
		switch from.Kind {
		case ir.TypeInt, ir.TypeFloat, ir.TypeBool, ir.TypeEnum:
			return "(" + c.typeName(to) + ")(" + operand + ")"
		}
	case ir.TypeBool:
		if from.Kind == ir.TypeBool {
			return operand
		}
	}
	return c.fail("converting %s to %s", from, to)
}

// --- Statements ---

func (c *Context) AssignText(n *ir.Assign, target, value string) string {
	t := n.Target.ExprType()
	op := n.Op
	if op == ast.AssignSet {
		return target + " = " + c.nullAs(t, n.Value, value) + ";"
	}
	if t != nil && (t.Kind == ir.TypeString || t.Kind == ir.TypeInt && (op == ast.AssignDiv || op == ast.AssignMod)) {
		bin := map[ast.AssignOp]ast.BinaryOp{ast.AssignAdd: ast.BinAdd, ast.AssignDiv: ast.BinDiv, ast.AssignMod: ast.BinMod}[op]
		return target + " = " + c.Binary(&ir.Binary{Op: bin, Left: n.Target, Right: n.Value, Type: t}, target, value) + ";"
	}
	if t != nil && t.Kind == ir.TypeFloat && op == ast.AssignMod {
		c.file.include("math.h", true)
		return target + " = fmod(" + target + ", " + value + ");"
	}
	return target + " " + op.String() + " " + value + ";"
}

func (c *Context) ToggleText(_ *ir.Toggle, target string) string {
	return target + " = !" + target + ";"
}

func (c *Context) CallStmtLines(n *ir.CallStmt) []string {
	if ir.IsErrorRaiseFunc(n.Call.Func) && len(n.Call.Args) > 0 {
		return []string{"sngl_panic(" + c.EvalExpr(n.Call.Args[0].Value) + ".ptr);"}
	}
	return []string{c.EvalExpr(n.Call) + ";"}
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
func (c *Context) nullAs(t *ir.Type, e ir.Expr, v string) string {
	if t != nil && ir.IsNullLiteral(e) {
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
		return "while (" + iter + ") {"
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

func (c *Context) IfHead(_ *ir.If, cond string) string { return "if (" + cond + ") {" }
func (c *Context) ElseHead() string                    { return "} else {" }
func (c *Context) BlockEnd() string                    { return "}" }
func (c *Context) Indent() string                      { return "\t" }

func (c *Context) Catch(*ir.If, []string) []string {
	return []string{c.fail("catching a raise") + ";"}
}

// Finally is body and then finally, which is how body ends while a raise
// ends the program, since C does not yet catch one (Catch): when it does, a
// raise must run finally on its way out.
func (c *Context) Finally(_ *ir.If, body, finally []string) []string {
	return append(append([]string{"{"}, irwalk.IndentLines("\t", body)...), append(irwalk.IndentLines("\t", finally), "}")...)
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
		return "(" + x.To.String() + ")(" + operand + ")"
	case ir.CConvertCallback, ir.CConvertContext, ir.CConvertRelease:
		// CCall writes all three from the closure.
		return operand
	}
	return c.fail("crossing to %s", x.To)
}

// CCall is a call of the C function c names, whose number comes back as the
// SNGL number it is. A callback is the dispatcher for its C signature, handed
// the closure on the heap as its context: released by C through the
// `#[c.release]` parameter, kept for good where the parameter is
// `#[retains]`, and freed after the call otherwise.
func (c *Context) CCall(n *ir.Call, args []string) string {
	fn := n.Func
	off := len(args) - len(fn.Params)
	for _, p := range fn.Params {
		if p.Type != nil && p.Type.Kind == ir.TypeRef {
			return c.fail("calling %s, which takes a %s", fn.Foreign.Name, p.Type)
		}
	}
	cb := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return !p.CompilerPassed() && p.Type != nil && p.Type.Kind == ir.TypeFunc })
	ctx := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return p.CContext })
	rel := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return p.CRelease })
	var free string
	if cb >= 0 && off >= 0 {
		if fn.Params[cb].CFuncType == nil || rel >= 0 && fn.Params[rel].CFuncType == nil {
			return c.fail("calling %s, whose function parameters name no C type (#[c.type])", fn.Foreign.Name)
		}
		if ctx < 0 {
			return c.fail("calling %s, whose callback takes no context", fn.Foreign.Name)
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
	call := fn.Foreign.Name + "(" + strings.Join(args, ", ") + ")"
	if free != "" {
		return c.fail("a callback %s holds for the call alone", fn.Foreign.Name)
	}
	if fn.Return != nil {
		switch fn.Return.Kind {
		case ir.TypeInt, ir.TypeFloat:
			return "(" + c.typeName(fn.Return) + ")" + call
		case ir.TypeStruct:
			if ir.CStructOf(fn.Return) != nil {
				return c.fail("a C struct returned by %s", fn.Foreign.Name)
			}
		}
	}
	return call
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
	return c.cCallback(sig, c.EvalExpr(value), value.ExprType())
}
