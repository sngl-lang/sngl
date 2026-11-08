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
	// State, where set, renders a read of a var the platform holds -- a
	// component's state, which gtk4 keeps in its Model -- and reports
	// whether v was one.
	State func(v *ir.Var) (string, bool)
	// file is what the rendering needs declared ahead of it.
	file *file
	// locals is what the scope declares that shadows the package.
	locals map[string]bool
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

func (c *Context) typeName(t *ir.Type) string {
	s, err := cTypeName(t)
	if err != nil {
		return c.fail("%v", err)
	}
	return s
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

func (c *Context) EvalExpr(e ir.Expr) string   { return irwalk.EvalExpr(c, e) }
func (c *Context) EvalStmt(s ir.Stmt) []string { return irwalk.EvalStmt(c, s) }

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
		if c.locals[n.Name] {
			return Local(n.Name)
		}
		if c.State != nil {
			if s, ok := c.State(sym); ok {
				return s
			}
		}
		if c.isPackageVar(sym) {
			return Global(sym.Name)
		}
		return Local(n.Name)
	case *ir.Func:
		return c.fail("a function used as a value (%s)", n.Name)
	}
	if n.Member != "" {
		return c.fail("an enum member (%s)", n.Member)
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
	if t := n.Operand.ExprType(); t == nil || t.Kind != ir.TypeStruct {
		return c.fail("selecting %s of %s", n.Field, t)
	}
	return operand + "." + Local(n.Field)
}

func (c *Context) Index(n *ir.Index, _, _ string) string {
	return c.fail("indexing a %s", n.Operand.ExprType())
}

func (c *Context) ListLit(n *ir.ListLit, _ []string) string {
	return c.fail("a list literal")
}

func (c *Context) MapLit(n *ir.MapLitIR, _, _ []string) string {
	return c.fail("a map literal")
}

// StructLit is a compound literal naming each field it sets; one it leaves
// out is C's zero, which is SNGL's for every type C writes.
func (c *Context) StructLit(n *ir.StructLit, fields []string) string {
	if !isProgramStruct(n.Type) {
		return c.fail("a literal of %s", n.Type)
	}
	var parts []string
	for i, f := range n.Fields {
		if f.Spread || f.Name == "" {
			return c.fail("a spread into %s", n.Type)
		}
		parts = append(parts, "."+Local(f.Name)+" = "+fields[i])
	}
	if len(parts) == 0 {
		return "(" + c.typeName(n.Type) + "){0}"
	}
	return "(" + c.typeName(n.Type) + "){" + strings.Join(parts, ", ") + "}"
}

func (c *Context) Spread(*ir.Spread, string) string { return c.fail("a spread") }

func (c *Context) Lambda(*ir.Lambda) string { return c.fail("a lambda") }

func (c *Context) MatchExpr(n *ir.MatchExpr) string {
	return c.EvalExpr(n.TernaryChain())
}

// --- Calls ---

func (c *Context) Call(n *ir.Call) string {
	if n.Func == nil {
		return c.fail("calling a function value")
	}
	if n.Func.IsCCast() && len(n.Args) == 1 {
		return c.CConvert(ir.NewCConvert(n.Func, n.Args[0].Value))
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
		return Method(fn.RecvName(), fn.Name)
	}
	return Global(fn.Name)
}

// Conversion converts between SNGL's scalars, and writes a scalar's text.
func (c *Context) Conversion(n *ir.Conversion) string {
	from, to := n.Operand.ExprType(), n.Type
	operand := c.EvalExpr(n.Operand)
	if from == nil || to == nil {
		return c.fail("a conversion of an untyped value")
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
		return target + " = " + value + ";"
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
	return decl + " = " + init + ";"
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
	return "(" + c.typeName(t) + "){0}"
}

func (c *Context) ReturnText(_ *ir.Return, value string) string {
	if value == "" {
		return "return;"
	}
	return "return " + value + ";"
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
		v := Local(n.Key)
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

func (c *Context) Finally(*ir.If, []string, []string) []string {
	return []string{c.fail("a finally block") + ";"}
}

// Match is nil, which asks for the if chain: a C switch takes integers
// alone, and an enum is the one place it would serve.
func (c *Context) Match(*ir.Match, string, [][]string, [][]string) []string { return nil }

func (c *Context) MutTargetIdent(n *ir.Ident) string { return c.Ident(n) }
func (c *Context) MutTargetField(n *ir.Select) string {
	return Local(n.Field)
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
		return "(" + x.To.String() + ")" + operand + ".ptr"
	case ir.CConvertNumber, ir.CConvertPointer:
		return "(" + x.To.String() + ")(" + operand + ")"
	}
	return c.fail("crossing to %s", x.To)
}

// CCall is a call of the C function c names, whose number comes back as the
// SNGL number it is.
func (c *Context) CCall(n *ir.Call, args []string) string {
	fn := n.Func
	for _, p := range fn.Params {
		if p.CompilerPassed() || p.Type != nil && p.Type.Kind == ir.TypeRef {
			return c.fail("calling %s, which takes a %s", fn.Foreign.Name, p.Type)
		}
	}
	call := fn.Foreign.Name + "(" + strings.Join(args, ", ") + ")"
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
