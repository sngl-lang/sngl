package ir

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

//go:generate go tool stringer -type=TypeKind -trimprefix Type

// TypeKind classifies the shape of a type.
type TypeKind int

const (
	TypeInvalid TypeKind = iota // error sentinel
	TypeDyn                     // unknown/dynamic
	TypeBool
	TypeInt
	TypeFloat
	TypeString
	TypeList      // Elem set
	TypeMap       // Elems = [K, V]
	TypeOption    // Elem set
	TypeStruct    // Decl set
	TypeEnum      // Decl set
	TypeUnit      // Decl set
	TypeFunc      // Sig set
	TypeComponent // Decl set
	TypeShape     // virtual; IsShape components satisfy it
	TypeColor
	TypeNull      // type of null literal
	TypeTypeParam // unresolved generic param; ParamName set
	TypeVoid      // void — a call that yields no value; not usable as an expression
	TypeRef       // Elem set — ref<T>, used by NoLambda for mutable captures
	TypeIter      // Elems = [T] for iter<T>
	TypeNative    // platform-provided foreign type; Meta carries the platform-specific descriptor
)

// Type is the unified representation of all SNGL types.
// Zero value is TypeInvalid.
type Type struct {
	Kind      TypeKind
	Elems     []*Type  // type arguments: List<T>, Option<T>, Map<K,V>, etc.
	Decl      Symbol   // struct, enum, unit, component declaration
	Sig       *FuncSig // function types
	ParamName string   // generic type param name ("T")
	Package   string   // import origin for qualified types
	Meta      any      // importer-provided side-channel (e.g. Go types.Type) read by language codegen

	// Bits is the explicit width of a sized numeric type (TypeInt: 8/16/32/64;
	// TypeFloat: 32/64). Bits==0 means unspecified width — the default int/
	// float, where the compiler picks the width and the interpreter does
	// 64-bit math. Unsigned is meaningful only for TypeInt with Bits!=0.
	Bits     uint8
	Unsigned bool
}

// Predefined singleton types for primitives.
var (
	TypDyn    = &Type{Kind: TypeDyn}
	TypBool   = &Type{Kind: TypeBool}
	TypInt    = &Type{Kind: TypeInt}
	TypFloat  = &Type{Kind: TypeFloat}
	TypString = &Type{Kind: TypeString}
	TypNull   = &Type{Kind: TypeNull}
	TypVoid   = &Type{Kind: TypeVoid}
	TypShape  = &Type{Kind: TypeShape}
)

// Sized numeric singletons. Plain int/float (TypInt/TypFloat) keep Bits==0.
var (
	TypInt8    = &Type{Kind: TypeInt, Bits: 8}
	TypInt16   = &Type{Kind: TypeInt, Bits: 16}
	TypInt32   = &Type{Kind: TypeInt, Bits: 32}
	TypInt64   = &Type{Kind: TypeInt, Bits: 64}
	TypUint8   = &Type{Kind: TypeInt, Bits: 8, Unsigned: true}
	TypUint16  = &Type{Kind: TypeInt, Bits: 16, Unsigned: true}
	TypUint32  = &Type{Kind: TypeInt, Bits: 32, Unsigned: true}
	TypUint64  = &Type{Kind: TypeInt, Bits: 64, Unsigned: true}
	TypFloat32 = &Type{Kind: TypeFloat, Bits: 32}
	TypFloat64 = &Type{Kind: TypeFloat, Bits: 64}
)

// IsSized reports whether t is an explicitly-sized numeric type (Bits!=0).
func (t *Type) IsSized() bool {
	return t != nil && (t.Kind == TypeInt || t.Kind == TypeFloat) && t.Bits != 0
}

// ListOf returns a list type with the given element type.
func ListOf(elem *Type) *Type {
	return &Type{Kind: TypeList, Elems: []*Type{elem}}
}

// MapOf returns a map<K, V> type.
func MapOf(k, v *Type) *Type {
	return &Type{Kind: TypeMap, Elems: []*Type{k, v}}
}

// OptionOf returns an option type wrapping the given type.
func OptionOf(inner *Type) *Type {
	return &Type{Kind: TypeOption, Elems: []*Type{inner}}
}

// RefOf builds a ref<elem> Type.
func RefOf(elem *Type) *Type {
	return &Type{Kind: TypeRef, Elems: []*Type{elem}}
}

// IterOf returns an iter<T> type.
func IterOf(elem *Type) *Type {
	return &Type{Kind: TypeIter, Elems: []*Type{elem}}
}

func (t *Type) String() string {
	if t == nil {
		return "<nil>"
	}
	switch t.Kind {
	case TypeInvalid:
		return "<invalid>"
	case TypeDyn:
		return "dyn"
	case TypeBool:
		return "bool"
	case TypeInt:
		if t.Bits != 0 {
			if t.Unsigned {
				return fmt.Sprintf("uint%d", t.Bits)
			}
			return fmt.Sprintf("int%d", t.Bits)
		}
		return "int"
	case TypeFloat:
		if t.Bits != 0 {
			return fmt.Sprintf("float%d", t.Bits)
		}
		return "float"
	case TypeString:
		return "string"
	case TypeList:
		if len(t.Elems) > 0 {
			return fmt.Sprintf("list<%s>", t.Elems[0])
		}
		return "list"
	case TypeMap:
		if len(t.Elems) == 2 {
			return fmt.Sprintf("map<%s, %s>", t.Elems[0], t.Elems[1])
		}
		return "map<?>"
	case TypeOption:
		if len(t.Elems) > 0 {
			return fmt.Sprintf("option<%s>", t.Elems[0])
		}
		return "option"
	case TypeRef:
		if len(t.Elems) > 0 {
			return fmt.Sprintf("ref<%s>", t.Elems[0])
		}
		return "ref"
	case TypeIter:
		if len(t.Elems) == 1 {
			return fmt.Sprintf("iter<%s>", t.Elems[0])
		}
		return "iter<?>"
	case TypeStruct:
		name := "struct"
		if t.Decl != nil {
			name = t.Decl.SymName()
		}
		// A generic instantiation prints its arguments: two Box types that
		// differ only in them are different types, and a diagnostic that
		// called both "Box" said nothing.
		if len(t.Elems) > 0 {
			args := make([]string, len(t.Elems))
			for i, e := range t.Elems {
				args[i] = e.String()
			}
			return name + "<" + strings.Join(args, ", ") + ">"
		}
		return name
	case TypeEnum:
		if t.Decl != nil {
			return t.Decl.SymName()
		}
		return "enum"
	case TypeUnit:
		if t.Decl != nil {
			return t.Decl.SymName()
		}
		return "unit"
	case TypeFunc:
		return "func"
	case TypeComponent:
		if t.Decl != nil {
			return t.Decl.SymName()
		}
		return "component"
	case TypeShape:
		return "shape"
	case TypeColor:
		return "color"
	case TypeNull:
		return "null"
	case TypeTypeParam:
		return t.ParamName
	case TypeVoid:
		return "void"
	}
	return "<unknown>"
}

// IsNumeric reports whether the type is int or float.
func (t *Type) IsNumeric() bool {
	return t.Kind == TypeInt || t.Kind == TypeFloat
}

// IsNumericOrUnit reports whether the type is int, float, or a unit type.
func (t *Type) IsNumericOrUnit() bool {
	return t.Kind == TypeInt || t.Kind == TypeFloat || t.Kind == TypeUnit
}

// IsSingleBaseUnit reports whether a unit type has exactly one base suffix,
// meaning all suffixes are convertible to a common base.
func (t *Type) IsSingleBaseUnit() bool {
	if t.Kind != TypeUnit || t.Decl == nil {
		return false
	}
	ud, ok := t.Decl.(*UnitDef)
	if !ok {
		return false
	}
	bases := 0
	for _, s := range ud.Suffixes {
		if s.IsBase() {
			bases++
		}
	}
	return bases == 1
}

// SameUnitType reports whether two unit types refer to the same UnitDef.
func (t *Type) SameUnitType(other *Type) bool {
	return t.Kind == TypeUnit && other.Kind == TypeUnit && t.Decl != nil && t.Decl == other.Decl
}

// nativeIdentity names a declaration by the package it was read from and the
// name it has there, which is stable across resolutions of that package. The
// import path is required: a native name is qualified by the short package
// name, which two packages can share, and one importer records no qualifier
// at all. A declaration with no recorded path compares by pointer instead,
// rather than matching too much.
func nativeIdentity(sym Symbol) (string, bool) {
	d, ok := sym.(*StructDef)
	if !ok || d.NativePkg == "" || d.Native == "" {
		return "", false
	}
	return d.NativePkg + "\x00" + d.Native, true
}

// sameDecl reports whether two named types name the same declaration. The
// pointer settles it when both came from the same load. They need not have:
// two files importing one Go package each resolve it, so `SearchEntry` is a
// different *StructDef on each side though it is one type — and a value of it
// could not be passed where it was expected. Falling back to the package and
// name is what makes those two the same type again.
func sameDecl(t, other *Type) bool {
	if t.Decl == other.Decl {
		return true
	}
	a, aok := nativeIdentity(t.Decl)
	b, bok := nativeIdentity(other.Decl)
	return aok && bok && a == b
}

// Substitute replaces TypeTypeParam nodes with concrete types from bindings.
// Returns t unchanged if no substitution is needed.
func (t *Type) Substitute(bindings map[string]*Type) *Type {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case TypeTypeParam:
		if bound, ok := bindings[t.ParamName]; ok {
			return bound
		}
		return t
	case TypeList, TypeMap, TypeOption, TypeRef, TypeIter, TypeStruct:
		elems := make([]*Type, len(t.Elems))
		changed := false
		for i, e := range t.Elems {
			elems[i] = e.Substitute(bindings)
			if elems[i] != e {
				changed = true
			}
		}
		if !changed {
			return t
		}
		return &Type{Kind: t.Kind, Elems: elems, Decl: t.Decl}
	case TypeFunc:
		if t.Sig == nil {
			return t
		}
		sig := t.Sig.Substitute(bindings)
		if sig == t.Sig {
			return t
		}
		return &Type{Kind: TypeFunc, Sig: sig}
	}
	return t
}

// Substitute replaces TypeTypeParam in params and return type.
func (s *FuncSig) Substitute(bindings map[string]*Type) *FuncSig {
	if s == nil || len(bindings) == 0 {
		return s
	}
	params := make([]*Param, len(s.Params))
	changed := false
	for i, p := range s.Params {
		nt := p.Type.Substitute(bindings)
		if nt != p.Type {
			changed = true
			params[i] = &Param{Name: p.Name, Type: nt, Default: p.Default, Receiver: p.Receiver}
		} else {
			params[i] = p
		}
	}
	ret := s.Return.Substitute(bindings)
	if ret != s.Return {
		changed = true
	}
	if !changed {
		return s
	}
	return &FuncSig{Params: params, Return: ret, TypeParams: s.TypeParams, RecvTypeParams: s.RecvTypeParams, Purity: s.Purity, Color: s.Color, PolyParam: s.PolyParam}
}

// Equal reports structural type equality.
func (t *Type) Equal(other *Type) bool {
	if t == other {
		return true
	}
	if t == nil || other == nil {
		return false
	}
	if t.Kind != other.Kind {
		return false
	}
	switch t.Kind {
	case TypeInt, TypeFloat:
		// Width and signedness distinguish sized numerics; Bits==0 (plain
		// int/float) is a distinct type from any sized width.
		return t.Bits == other.Bits && t.Unsigned == other.Unsigned
	case TypeList, TypeMap, TypeOption, TypeRef, TypeIter:
		if len(t.Elems) != len(other.Elems) {
			return false
		}
		for i, e := range t.Elems {
			if !e.Equal(other.Elems[i]) {
				return false
			}
		}
		return true
	case TypeStruct:
		// Two struct types are equal when they share the same declaration AND
		// their type arguments (if any) are pairwise equal. This covers both
		// non-generic structs (no Elems) and generic instantiations like Box<int>
		// vs Box<string>.
		if !sameDecl(t, other) {
			return false
		}
		if len(t.Elems) != len(other.Elems) {
			return false
		}
		for i, e := range t.Elems {
			if !e.Equal(other.Elems[i]) {
				return false
			}
		}
		return true
	case TypeEnum, TypeUnit, TypeComponent:
		return sameDecl(t, other)
	case TypeFunc:
		return t.Sig.Equal(other.Sig)
	case TypeTypeParam:
		return t.ParamName == other.ParamName
	}
	return true
}

// IsAssignableTo reports whether a value of type t can be assigned to target.
// Dyn works like Go's any/interface{}: any value is assignable TO dyn,
// but dyn is not assignable to concrete types without explicit conversion.
func (t *Type) IsAssignableTo(target *Type) bool {
	if t.Equal(target) {
		return true
	}
	if target.Kind == TypeDyn {
		return true
	}
	if t.Kind == TypeNull && target.Kind == TypeOption {
		return true
	}
	if t.Kind == TypeNull && target.Kind == TypeFunc {
		return true
	}
	if t.Kind == TypeString && isStringDomain(target.Kind) {
		return true
	}
	if isStringDomain(t.Kind) && target.Kind == TypeString {
		return true
	}
	// The color/date/time/datetime value types are carried as stdlib-
	// StructDef-backed TypeStructs with a canonical string form. They
	// participate in string<->string implicit conversion just like the
	// kind-backed string-domain types above.
	if t.Kind == TypeString && StringReprStruct(target) {
		return true
	}
	if StringReprStruct(t) && target.Kind == TypeString {
		return true
	}
	if t.Kind == TypeList && target.Kind == TypeList {
		return t.Elems[0].IsAssignableTo(target.Elems[0])
	}
	// list<T> implicitly converts to iter<T>.
	if t.Kind == TypeList && target.Kind == TypeIter && len(t.Elems) == 1 && len(target.Elems) == 1 {
		return t.Elems[0].IsAssignableTo(target.Elems[0])
	}
	// map<K, V> → map<K', V'> where V is assignable to V' and K is
	// assignable to K' under the same rules (Equal, or K' is dyn).
	// Allowing K → dyn lets stdlib wrappers forward typed maps to
	// intrinsics whose key types are deliberately erased (e.g. i18n's
	// PluralKey-keyed maps → map<dyn, string> at the runtime boundary).
	// Concrete-key conversions (like int → string) are still rejected.
	if t.Kind == TypeMap && target.Kind == TypeMap && len(t.Elems) == 2 && len(target.Elems) == 2 {
		keyOK := t.Elems[0].Equal(target.Elems[0]) || target.Elems[0].Kind == TypeDyn
		return keyOK && t.Elems[1].IsAssignableTo(target.Elems[1])
	}
	if t.Kind == TypeOption && target.Kind == TypeOption {
		return t.Elems[0].IsAssignableTo(target.Elems[0])
	}
	if target.Kind == TypeOption {
		return t.IsAssignableTo(target.Elems[0])
	}
	return false
}

func isStringDomain(k TypeKind) bool {
	return k == TypeColor
}

// builtinOf returns the ast.BuiltinKind of t's backing StructDef, or
// BuiltinNone. The mark is stamped by the #[builtin] macro, so string-repr
// and generic behaviour travel with the type rather than with a hardcoded name.
func builtinOf(t *Type) ast.BuiltinKind {
	if t == nil || t.Kind != TypeStruct {
		return ast.BuiltinNone
	}
	sd, ok := t.Decl.(*StructDef)
	if !ok {
		return ast.BuiltinNone
	}
	return sd.Builtin
}

// IsColorStruct reports whether t is the color value type. The color value is
// carried uniformly as a TypeStruct backed by its StructDef (no separate
// TypeColor kind is produced); call this to detect the shape.
func IsColorStruct(t *Type) bool { return builtinOf(t) == ast.BuiltinColor }

// StringReprStruct reports whether t is a struct with a canonical string form
// (coerces to/from string): color, date, time, datetime.
func StringReprStruct(t *Type) bool { return builtinOf(t).IsStringRepr() }

// IsDateStruct/IsTimeStruct/IsDateTimeStruct report whether t is the date/
// time/datetime value type. These three were formerly the TypeDate/TypeTime/
// TypeDateTime kinds; they are now carried uniformly as TypeStruct backed by
// the StructDef (like color).
func IsDateStruct(t *Type) bool     { return builtinOf(t) == ast.BuiltinDate }
func IsTimeStruct(t *Type) bool     { return builtinOf(t) == ast.BuiltinTime }
func IsDateTimeStruct(t *Type) bool { return builtinOf(t) == ast.BuiltinDateTime }

// Registered stdlib datetime struct type. Populated by the checker once
// lib/types.sngl is parsed, so non-checker phases (foreign-type importers,
// etc.) can synthesize a canonical datetime value type without their own scope
// access. Nil before registration; the accessor falls back to TypDyn.
var stdlibDateTimeType *Type

// RegisterStringReprStructs records the resolved stdlib datetime struct type so
// the DateTimeType accessor can hand it out. Idempotent.
func RegisterStringReprStructs(date, time, dateTime *Type) {
	if dateTime != nil {
		stdlibDateTimeType = dateTime
	}
}

// DateTimeType returns the registered stdlib struct type,
// falling back to dyn when the stdlib has not been loaded yet.
func DateTimeType() *Type {
	if stdlibDateTimeType != nil {
		return stdlibDateTimeType
	}
	return TypDyn
}

// FuncSig describes a function signature.
type FuncSig struct {
	Params         []*Param
	Return         *Type // nil for void/action
	TypeParams     []string
	RecvTypeParams []string // receiver-level type parameters; consumed (set to nil) after substitution
	Purity         Purity
	Color          Color // Sync (default), Async, or Param.
	PolyParam      int   // when Color == ColorParam: index of the funcvar param the color depends on.
}

// IsPoly reports whether this signature's color depends on a funcvar
// parameter. False for concrete Sync/Async sigs.
func (s *FuncSig) IsPoly() bool {
	return s != nil && s.Color == ColorParam
}

// Equal reports structural signature equality (ignoring parameter names).
func (s *FuncSig) Equal(other *FuncSig) bool {
	if s == other {
		return true
	}
	if s == nil || other == nil {
		return false
	}
	if len(s.Params) != len(other.Params) {
		return false
	}
	for i, p := range s.Params {
		if !p.Type.Equal(other.Params[i].Type) {
			return false
		}
	}
	if s.Return == nil && other.Return == nil {
		return true
	}
	if s.Return == nil || other.Return == nil {
		return false
	}
	return s.Return.Equal(other.Return)
}

//go:generate go tool stringer -type=Purity -trimprefix Purity

// Purity describes the side-effect level of a function.
type Purity int

const (
	PurityUnknown  Purity = iota
	PurityPure            // no reads or mutations of mutable state
	PurityReadonly        // reads mutable state but doesn't modify
	PurityMutates         // modifies mutable state
)

// NativeTypeRef describes a foreign-language type referenced by a
// platform translator. The CgoC flag selects the rendering style:
// true → cgo C type (*C.X with unsafe.Pointer casts); false → plain
// Go-package type (*pkg.X with regular type-conversion casts).
type NativeTypeRef struct {
	CgoC bool   // true → cgo type, false → Go-package type
	Name string // "GtkLabel" or "fyne.Container"
	// Bare renders Name as-is (no leading "*"/"C."), for named Go types that
	// are already pointer-like (e.g. an opaque handle "gtk4rt.Handle").
	Bare bool
}

// NativePointerOf returns an *ir.Type representing *C.<name>.
// Cgo-only — use NativeGoPointerOf for Go-package types.
func NativePointerOf(name string) *Type {
	return &Type{Kind: TypeNative, Meta: NativeTypeRef{CgoC: true, Name: name}}
}

// NativeGoPointerOf returns an *ir.Type representing *<name>
// where name is a fully-qualified Go-package type
// (e.g. "fyne.Container", "widget.Label").
func NativeGoPointerOf(name string) *Type {
	return &Type{Kind: TypeNative, Meta: NativeTypeRef{CgoC: false, Name: name}}
}

// NativeGoNamed returns an *ir.Type that renders as the bare Go type name
// (no leading "*"), for already-pointer-like named types such as an opaque
// handle (e.g. "gtk4rt.Handle").
func NativeGoNamed(name string) *Type {
	return &Type{Kind: TypeNative, Meta: NativeTypeRef{CgoC: false, Name: name, Bare: true}}
}
