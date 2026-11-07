package ir

import (
	"fmt"
	"strings"
)

// CType is a C type as a `#[cnative]` mark spells it: a type name, whether it
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

// ParseCType reads the declarator grammar a `#[cnative]` type name is held
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
			return nil, fmt.Errorf("C type %q has %q after its pointer; a const pointer or a declarator past a `*` is not one a #[cnative] type spells", s, tok)
		case tok == "const":
			if t.Const || len(words) > 0 {
				return nil, fmt.Errorf("C type %q writes const anywhere but first", s)
			}
			t.Const = true
		case IsCIdent(tok):
			words = append(words, tok)
		default:
			return nil, fmt.Errorf("C type %q holds %q, and a #[cnative] type is a name, const and pointers; an array or a function pointer stays in C", s, tok)
		}
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("C type %q names no type", s)
	}
	t.Name = strings.Join(words, " ")
	return t, nil
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

// IsCIdent reports whether s is a C identifier, which is what a `#[cnative]`
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

// CTypeOf is the C type a SNGL type is declared as: a `#[cnative]` struct's,
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

// CPath is the Foreign.Path every `#[cnative]` declaration carries.
const CPath = "C"

// IsCCast reports whether fn is a `#[cnative(…, cast)]` function: a
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
)

// CConvert is a value converted to a C type, the conversion a call of a C
// function makes of its argument. lower.CMarshal writes one for each
// argument whose parameter's type names a sngl:x/c conversion or a
// `#[cnative]` alias, and for each call of a `#[cnative(…, cast)]` function. A translator renders it as its
// language converts to C (codegen.CInterop).
type CConvert struct {
	Kind    CConvertKind
	To      *CType
	Operand Expr
	// Type is the SNGL type the value has once converted: the `#[cnative]`
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
