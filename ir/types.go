package ir

import "fmt"

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
	TypeOption    // Elem set
	TypeStruct    // Decl set
	TypeEnum      // Decl set
	TypeUnit      // Decl set
	TypeFunc      // Sig set
	TypeComponent // Decl set
	TypeColor
	TypeDate
	TypeTime
	TypeDateTime
	TypeDuration
	TypeURL
	TypeEmail
	TypeUUID
	TypeRegex
	TypeBase64
	TypeIPV4
	TypeIPV6
	TypeHostname
	TypeDecimal
	TypeNull      // type of null literal
	TypeTypeParam // unresolved generic param; ParamName set
	TypeVoid      // void — a call that yields no value; not usable as an expression
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
}

// Predefined singleton types for primitives.
var (
	TypDyn      = &Type{Kind: TypeDyn}
	TypBool     = &Type{Kind: TypeBool}
	TypInt      = &Type{Kind: TypeInt}
	TypFloat    = &Type{Kind: TypeFloat}
	TypString   = &Type{Kind: TypeString}
	TypColor    = &Type{Kind: TypeColor}
	TypNull     = &Type{Kind: TypeNull}
	TypDate     = &Type{Kind: TypeDate}
	TypTime     = &Type{Kind: TypeTime}
	TypDateTime = &Type{Kind: TypeDateTime}
	TypDuration = &Type{Kind: TypeDuration}
	TypURL      = &Type{Kind: TypeURL}
	TypEmail    = &Type{Kind: TypeEmail}
	TypUUID     = &Type{Kind: TypeUUID}
	TypRegex    = &Type{Kind: TypeRegex}
	TypBase64   = &Type{Kind: TypeBase64}
	TypIPV4     = &Type{Kind: TypeIPV4}
	TypIPV6     = &Type{Kind: TypeIPV6}
	TypHostname = &Type{Kind: TypeHostname}
	TypDecimal  = &Type{Kind: TypeDecimal}
	TypVoid     = &Type{Kind: TypeVoid}
)

// ListOf returns a list type with the given element type.
func ListOf(elem *Type) *Type {
	return &Type{Kind: TypeList, Elems: []*Type{elem}}
}

// OptionOf returns an option type wrapping the given type.
func OptionOf(inner *Type) *Type {
	return &Type{Kind: TypeOption, Elems: []*Type{inner}}
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
		return "int"
	case TypeFloat:
		return "float"
	case TypeString:
		return "string"
	case TypeList:
		if len(t.Elems) > 0 {
			return fmt.Sprintf("list<%s>", t.Elems[0])
		}
		return "list"
	case TypeOption:
		if len(t.Elems) > 0 {
			return fmt.Sprintf("option<%s>", t.Elems[0])
		}
		return "option"
	case TypeStruct:
		if t.Decl != nil {
			return t.Decl.SymName()
		}
		return "struct"
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
	case TypeColor:
		return "color"
	case TypeDate:
		return "date"
	case TypeTime:
		return "time"
	case TypeDateTime:
		return "dateTime"
	case TypeDuration:
		return "duration"
	case TypeURL:
		return "url"
	case TypeEmail:
		return "email"
	case TypeUUID:
		return "uuid"
	case TypeRegex:
		return "regex"
	case TypeBase64:
		return "base64"
	case TypeIPV4:
		return "ipv4"
	case TypeIPV6:
		return "ipv6"
	case TypeHostname:
		return "hostname"
	case TypeDecimal:
		return "decimal"
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
		if s.IsBase {
			bases++
		}
	}
	return bases == 1
}

// SameUnitType reports whether two unit types refer to the same UnitDef.
func (t *Type) SameUnitType(other *Type) bool {
	return t.Kind == TypeUnit && other.Kind == TypeUnit && t.Decl != nil && t.Decl == other.Decl
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
	case TypeList, TypeOption:
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
			params[i] = &Param{Name: p.Name, Type: nt, Default: p.Default}
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
	return &FuncSig{Params: params, Return: ret, TypeParams: s.TypeParams, Purity: s.Purity}
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
	case TypeList, TypeOption:
		if len(t.Elems) != len(other.Elems) {
			return false
		}
		for i, e := range t.Elems {
			if !e.Equal(other.Elems[i]) {
				return false
			}
		}
		return true
	case TypeStruct, TypeEnum, TypeUnit, TypeComponent:
		return t.Decl == other.Decl
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
	if t.Kind == TypeInt && target.Kind == TypeFloat {
		return true
	}
	if t.Kind == TypeString && isStringDomain(target.Kind) {
		return true
	}
	if isStringDomain(t.Kind) && target.Kind == TypeString {
		return true
	}
	if t.Kind == TypeList && target.Kind == TypeList {
		return t.Elems[0].IsAssignableTo(target.Elems[0])
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
	switch k {
	case TypeColor, TypeDate, TypeTime, TypeDateTime, TypeDuration,
		TypeURL, TypeEmail, TypeUUID, TypeRegex, TypeBase64,
		TypeIPV4, TypeIPV6, TypeHostname, TypeDecimal:
		return true
	}
	return false
}

// FuncSig describes a function signature.
type FuncSig struct {
	Params     []*Param
	Return     *Type // nil for void/action
	TypeParams []string
	Purity     Purity
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
