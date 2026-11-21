package ir

import (
	"fmt"
	"slices"
	"strings"
)

// CType is a C type as a `#[c.native]` mark spells it: a type name, whether it
// is const, and how many pointers deep. `const char *` is {Name: "char",
// Const: true, Ptr: 1}. The spelling is C's, never a language's: `C.` and
// `unsafe.Pointer` are the Go translator's to write (codegen.CInterop).
type CType struct {
	// Name is the type's name: a typedef (`cairo_t`), a builtin type, which
	// may take several words (`unsigned int`), or `void`.
	Name  string
	Const bool
	Ptr   int
}

// ParseCType reads the declarator grammar a `#[c.native]` type name is held
// to: an optional `const`, a name of one or more words, and any number of
// `*`. An array, a function pointer and a const pointer are refused: a
// callback is a SNGL func a translator writes the trampoline for, and an
// array crosses only inside a platform's C runtime.
func ParseCType(s string) (*CType, error) {
	t := &CType{}
	var words []string
	for _, tok := range cTokens(s) {
		switch {
		case tok == "*":
			if len(words) == 0 {
				return nil, fmt.Errorf("C type %q names no type before its pointer", s)
			}
			t.Ptr++
		case t.Ptr > 0:
			return nil, fmt.Errorf("C type %q has %q after its pointer; a const pointer or a declarator past a `*` is not one a #[c.native] type spells", s, tok)
		case tok == "const":
			if t.Const || len(words) > 0 {
				return nil, fmt.Errorf("C type %q writes const anywhere but first", s)
			}
			t.Const = true
		case IsCIdent(tok):
			words = append(words, tok)
		default:
			return nil, fmt.Errorf("C type %q holds %q, and a #[c.native] type is a name, const and pointers; an array or a function pointer stays in C", s, tok)
		}
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("C type %q names no type", s)
	}
	name, err := builtinCName(words)
	if err != nil {
		return nil, fmt.Errorf("C type %q: %w", s, err)
	}
	t.Name = name
	return t, nil
}

// cBuiltinWords are the words C spells its builtin arithmetic types with.
var cBuiltinWords = map[string]bool{"signed": true, "unsigned": true, "short": true, "long": true, "int": true, "char": true, "float": true, "double": true}

// cBuiltinNames are the builtin arithmetic types as one spelling each, the
// one cgo names (codegen/lang/golang's cgoNames), keyed by the words in
// order with `int` and a redundant `signed` dropped.
var cBuiltinNames = map[string]string{
	"char": "char", "signed char": "signed char", "unsigned char": "unsigned char",
	"short": "short", "unsigned short": "unsigned short",
	"": "int", "unsigned": "unsigned int",
	"long": "long", "unsigned long": "unsigned long",
	"long long": "long long", "unsigned long long": "unsigned long long",
	"float": "float", "double": "double",
}

// builtinCName is words as one name: a typedef's own, or a builtin type's
// one spelling, `short int` and `signed short` both `short`.
func builtinCName(words []string) (string, error) {
	for _, w := range words {
		if !cBuiltinWords[w] {
			if len(words) > 1 {
				return "", fmt.Errorf("%q is no word of a C builtin type, and a typedef is one word", w)
			}
			return w, nil
		}
	}
	var kept []string
	ints := 0
	for _, w := range words {
		if w == "int" {
			ints++
			continue
		}
		kept = append(kept, w)
	}
	key := strings.Join(kept, " ")
	if ints > 1 {
		key = "!"
	}
	if !slices.Contains(kept, "char") {
		key = strings.TrimPrefix(strings.TrimPrefix(key, "signed"), " ")
	}
	if ints > 0 && (slices.Contains(kept, "char") || slices.Contains(kept, "float") || slices.Contains(kept, "double")) {
		key = "!"
	}
	name, ok := cBuiltinNames[key]
	if !ok {
		return "", fmt.Errorf("%q is no C type a SNGL value crosses as", strings.Join(words, " "))
	}
	return name, nil
}

// cTokens splits s into words and `*`, and anything else one rune at a time
// so ParseCType can name it.
func cTokens(s string) []string {
	var out []string
	word := -1
	flush := func(i int) {
		if word >= 0 {
			out = append(out, s[word:i])
			word = -1
		}
	}
	for i, r := range s {
		switch {
		case r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			if word < 0 {
				word = i
			}
		case r == ' ' || r == '\t':
			flush(i)
		default:
			flush(i)
			out = append(out, string(r))
		}
	}
	flush(len(s))
	return out
}

// IsCIdent reports whether s is a C identifier, which is what a `#[c.native]`
// function's name is.
func IsCIdent(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for _, r := range s {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// CIdentToken is s, free C text such as a calling convention
// (`__attribute__((stdcall))`), as a part of a C identifier: each run of
// what an identifier cannot hold is one `_`, and none leads or trails.
func CIdentToken(s string) string {
	var b strings.Builder
	sep := false
	for _, r := range s {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			if sep && b.Len() > 0 {
				b.WriteByte('_')
			}
			sep = false
			b.WriteRune(r)
			continue
		}
		sep = true
	}
	return b.String()
}

// String is t as C writes it: `const char *`, `cairo_t *`, `int`.
func (t *CType) String() string {
	if t == nil {
		return ""
	}
	s := t.Name
	if t.Const {
		s = "const " + s
	}
	if t.Ptr > 0 {
		s += " " + strings.Repeat("*", t.Ptr)
	}
	return s
}

// Equal reports whether t and u spell one C type.
func (t *CType) Equal(u *CType) bool {
	if t == nil || u == nil {
		return t == u
	}
	return *t == *u
}

// IsVoidPointer reports whether t is `void *`, the pointer every other
// pointer converts through.
func (t *CType) IsVoidPointer() bool {
	return t != nil && t.Name == "void" && t.Ptr == 1
}

// IsCharPointer reports whether t is `char *` or `const char *`, which a
// `string` crosses as.
func (t *CType) IsCharPointer() bool {
	return t != nil && t.Name == "char" && t.Ptr == 1
}

// IsUTF16Pointer reports whether t points to a 16-bit unit, which a `string`
// written as an alias of t crosses as UTF-16. `unsigned short` is the one
// spelling every C compiler has without a header; Win32's `WCHAR` names it.
func (t *CType) IsUTF16Pointer() bool {
	return t != nil && t.Ptr == 1 && utf16Units[t.Name]
}

var utf16Units = map[string]bool{"unsigned short": true, "char16_t": true, "uint16_t": true, "WCHAR": true}

// CTypeOf is the C type a SNGL type is declared as: a `#[c.native]` struct's,
// nil for anything else.
func CTypeOf(t *Type) *CType {
	if t == nil || t.Kind != TypeStruct {
		return nil
	}
	if sd, ok := t.Decl.(*StructDef); ok && sd.Foreign.Path == CPath {
		return sd.Foreign.C
	}
	return nil
}

// IsCPointerOption reports whether t is an option of a C handle whose C type
// is a pointer -- written with a `*`, or a typedef its declaration says is
// one (`#[c.native(…, pointer)]`) -- which crosses to C as that pointer,
// NULL for none.
func IsCPointerOption(t *Type) bool {
	if t == nil || t.Kind != TypeOption || len(t.Elems) != 1 {
		return false
	}
	elem := t.Elems[0]
	ct := CTypeOf(elem)
	if ct == nil || CStructOf(elem) != nil {
		return false
	}
	sd, _ := elem.Decl.(*StructDef)
	return ct.Ptr > 0 || sd.Foreign.CPointer
}

// CPath is the Foreign.Path every `#[c.native]` declaration carries.
const CPath = "C"

// IsCStruct reports whether sd is a C struct the program holds: a
// `#[c.native]` struct declaring fields, which is a SNGL struct with a C
// layout, converted field by field where a C call takes or fills one. One
// declaring none is a C type SNGL holds opaquely, as the C type itself.
func (sd *StructDef) IsCStruct() bool {
	return sd != nil && sd.Foreign.Path == CPath && len(sd.Fields) > 0
}

// CStructOf is t's declaration where t is a C struct (IsCStruct), nil for
// anything else.
func CStructOf(t *Type) *StructDef {
	if t == nil || t.Kind != TypeStruct {
		return nil
	}
	if sd, ok := t.Decl.(*StructDef); ok && sd.IsCStruct() {
		return sd
	}
	return nil
}

// IsCCast reports whether fn is a `#[c.native(…, cast)]` function: a
// conversion to the C type its name spells rather than a C function to call.
func (fn *Func) IsCCast() bool {
	return fn != nil && fn.Foreign.Path == CPath && fn.Foreign.C != nil
}

// CConvertKind is what a CConvert does to its operand.
type CConvertKind int

const (
	// CConvertString copies a `string` into a NUL-terminated `char *` the
	// program owns.
	CConvertString CConvertKind = iota
	// CConvertNumber is a number written as the C scalar type To names: an
	// `int`, a `double`, a typedef'd enum.
	CConvertNumber
	// CConvertPointer is a pointer, or a node, as the pointer type To names.
	CConvertPointer
	// CConvertUTF16 encodes a `string` as the NUL-terminated UTF-16 a
	// pointer to a 16-bit unit To names, valid for the call: an argument
	// written `c.utf16`, which is how Win32 takes a string.
	CConvertUTF16
	// CConvertCallback is a closure handed to C as the function pointer a
	// `func` parameter of a C function declares. Operand is the closure and
	// Type its func type; the translator writes the C function C calls,
	// which finds the closure through the context the call hands over.
	CConvertCallback
	// CConvertContext is the `void *` a `#[c.context]` parameter is handed:
	// what the callback the same call converts finds its closure by. It
	// has no operand of its own; the translator writes it from the
	// callback's, and Operand is the placeholder the parameter defaults to.
	CConvertContext
	// CConvertRelease is the function a `#[c.release]` parameter is handed,
	// which C calls with the context to let the closure go, or with the
	// pointer a held value crosses as to let the value go. Operand is the
	// parameter's placeholder.
	CConvertRelease
	// CConvertStruct is a C struct (StructDef.IsCStruct) written field by
	// field as the C struct To names, each field as its alias or the scalar
	// it is.
	CConvertStruct
	// CConvertHeld is a SNGL value handed to C as the `void *` a parameter
	// written `c.held[T]` takes: a copy of Operand the program keeps until C
	// calls the function the call's `#[c.release]` parameter is handed.
	// Type is the value's type, T.
	CConvertHeld
)

// CConvert is a value converted to a C type, the conversion a call of a C
// function makes of its argument. lower.CMarshal writes one for each
// argument whose parameter's type names a sngl:x/c conversion or a
// `#[c.native]` alias, and for each call of a `#[c.native(…, cast)]` function. A translator renders it as its
// language converts to C (codegen.CInterop).
type CConvert struct {
	Kind    CConvertKind
	To      *CType
	Operand Expr
	// Type is the SNGL type the value has once converted: the `#[c.native]`
	// struct To declares, or the type an alias names, which is what the
	// argument is in SNGL.
	Type *Type
	// Via is the cast declaration the conversion was written through, and
	// Alias the parameter's alias where there is none. Either is how a dump
	// prints it back.
	Via   *Func
	Alias *Alias `json:",omitempty"`
}

func (*CConvert) exprNode()         {}
func (x *CConvert) ExprType() *Type { return x.Type }

// NewCConvert is operand converted to cast's C type: a string to `char *`
// is a copy, a value to a type that is no pointer a scalar conversion, and
// anything else a pointer cast.
func NewCConvert(cast *Func, operand Expr) *CConvert {
	to := cast.Foreign.C
	kind := CConvertPointer
	switch t := operand.ExprType(); {
	case to.IsCharPointer() && t != nil && t.Kind == TypeString:
		kind = CConvertString
	case to.Ptr == 0:
		kind = CConvertNumber
	}
	return &CConvert{Kind: kind, To: to, Operand: operand, Type: cast.Return, Via: cast}
}

// NewCAliasConvert is operand converted to the C typedef a, as a parameter
// written with a declares: a number or a 0-or-1 `int` cast to it, and a
// string encoded as UTF-16.
func NewCAliasConvert(a *Alias, operand Expr) *CConvert {
	kind := CConvertNumber
	if a.C.IsUTF16Pointer() {
		kind = CConvertUTF16
	}
	return &CConvert{Kind: kind, To: a.C, Operand: operand, Type: a.Type, Alias: a}
}

// StaticCallee is the function e names where e is what a callback C calls
// with no context (sngl:x/c's #[static]) may be handed: a package's
// function by name, no method and no lambda. A conversion a pass wrapped the
// argument in is looked through.
func StaticCallee(e Expr) *Func {
	if cv, ok := e.(*CConvert); ok {
		e = cv.Operand
	}
	id, ok := e.(*Ident)
	if !ok {
		return nil
	}
	fn, ok := id.Sym.(*Func)
	if !ok || fn.IsMethod() || fn.Foreign.Path != "" {
		return nil
	}
	return fn
}

// CStatic is a function C is handed for a callback it calls with no context:
// the package's function, and the parameter taking it, which says how it
// crosses.
type CStatic struct {
	Callee *Func
	Param  *Param
}

// CalledCStatics is each function pkg hands a C function as a callback it
// calls with no context (sngl:x/c's #[static]), once, in the order the calls
// are met: what a translator writes a function of its own for. A call of a
// function skip answers for is passed over: one a Go wrapper package makes,
// which exports the callback itself.
func CalledCStatics(pkg *Package, skip func(*Func) bool) []CStatic {
	var out []CStatic
	_ = Walk(pkg, func(n Node) error {
		c, ok := n.(*Call)
		if !ok || c.Func == nil || c.Func.Foreign.Path != CPath || skip != nil && skip(c.Func) {
			return nil
		}
		off := len(c.Args) - len(c.Func.Params)
		if off < 0 {
			return nil
		}
		for i, p := range c.Func.Params {
			if !p.CStatic {
				continue
			}
			callee := StaticCallee(c.Args[off+i].Value)
			// One handed with a convention is another function C calls.
			if callee != nil && !slices.ContainsFunc(out, func(s CStatic) bool { return s.Callee == callee && s.Param.CConvention == p.CConvention }) {
				out = append(out, CStatic{Callee: callee, Param: p})
			}
		}
		return nil
	})
	return out
}

// CalledCShimmed is each C function pkg calls, once, in the order the calls
// are met, that a language unable to call it as C does calls through a C
// function of the declaration's one shape: one taking a variable argument
// list (`#[c.native(…, variadic)]`), a member of a C struct read or called
// (`#[c.native(…, field)]`), and one handing over a string the call frees
// (`#[c.frees]`), whose freeing function takes a pointer of a type only C
// converts to.
func CalledCShimmed(pkg *Package) []*Func {
	var out []*Func
	_ = Walk(pkg, func(n Node) error {
		if c, ok := n.(*Call); ok && c.Func != nil && c.Func.Foreign.Path == CPath && c.Func.CShimmed() && !slices.Contains(out, c.Func) {
			out = append(out, c.Func)
		}
		return nil
	})
	return out
}

// CCallExpr is the C expression a call of fn, a C function, is with args
// rendered: its name called with them, or for a
// `#[c.native("Struct.member", field)]` function the member read through
// the first argument, or, handed more, the function pointer it holds
// called with the rest.
func CCallExpr(fn *Func, args []string) string {
	if !fn.Foreign.Field || len(args) == 0 {
		return fn.Foreign.Name + "(" + strings.Join(args, ", ") + ")"
	}
	_, member, _ := strings.Cut(fn.Foreign.Name, ".")
	read := "(" + args[0] + ")->" + member
	if len(args) == 1 {
		return read
	}
	return read + "(" + strings.Join(args[1:], ", ") + ")"
}

// CShimmed reports whether fn, a C function, is one CalledCShimmed collects.
func (fn *Func) CShimmed() bool {
	return fn.Foreign.Variadic || fn.Foreign.Field || len(fn.CFreed()) > 0
}

// CFreed is each C function freeing a string fn, a C function, hands over
// (`#[c.frees]`), its result's first, once each.
func (fn *Func) CFreed() []string {
	var out []string
	if fn.CFrees != "" {
		out = append(out, fn.CFrees)
	}
	for _, p := range fn.Params {
		if p.CFrees != "" && !slices.Contains(out, p.CFrees) {
			out = append(out, p.CFrees)
		}
	}
	return out
}
