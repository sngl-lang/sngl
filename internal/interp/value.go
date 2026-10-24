package interp

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// Struct is the value of a struct. It carries what the checker knew about the
// literal it came from — the declaration, the type, and the order the fields
// were checked in — because a value is turned back into IR when a fold
// succeeds, and a bare map has none of that to give back.
//
// It is also what tells a struct from a map. Both were map[string]any, so
// nothing downstream could say which of the two it held; the struct type name
// traveled in a "__type" entry among the fields, and a struct reached the map
// paths that index by key and read a zero value on a miss.
//
// Fields are a slice rather than a map: order is part of what the checker
// decided (a written field keeps its place, a defaulted one is appended), and
// it reaches emitted Go, JS and Kotlin. A struct has few enough fields that
// finding one by name linearly costs less than keeping a map and an order in
// step.
type Struct struct {
	Def    *ir.StructDef
	Type   *ir.Type
	Fields []Field
	// Handle is the value's identity when its type is owned, nil otherwise.
	Handle *Handle
}

// Field is one field of a Struct.
type Field struct {
	Name  string
	Value any
	// Omitted marks a field the literal that built the struct left out, and
	// Filled is a copy of what it was filled with: its type's zero or its
	// declared default. It reads like any other field; Unwritten says whether
	// it still stands as it was left out.
	Omitted bool
	Filled  any
}

// Unwritten reports whether f is still what its literal's omission filled in.
// A value spelled back as a literal leaves such a field out, since a backend
// reads a field's presence as having been set (ir.DeclaredDefault). Asked of
// the value rather than trusted to a flag, because a write may reach the field
// without passing through its struct: `o.inner.x = 1` writes the struct inner
// holds in place.
func (f Field) Unwritten() bool {
	if !f.Omitted {
		return false
	}
	eq, ok := Equal(f.Value, f.Filled)
	return ok && eq
}

// NewStruct returns an empty struct value of the given declaration and type.
func NewStruct(def *ir.StructDef, typ *ir.Type) *Struct {
	return &Struct{Def: def, Type: typ}
}

// CopyValue copies a struct, as deep as its fields hold structs, and returns
// anything else as it is. It is the untyped half of copyOf: a struct is a
// value, so storing one -- a local, a field, a parameter -- stores a copy, and
// editing it does not edit what the caller still holds.
func CopyValue(v any) any {
	if b, ok := v.(*Builder); ok {
		return b.clone()
	}
	s, ok := v.(*Struct)
	if !ok || s == nil {
		return v
	}
	cp := &Struct{Def: s.Def, Type: s.Type, Fields: make([]Field, len(s.Fields)), Handle: s.Handle}
	for i, f := range s.Fields {
		cp.Fields[i] = Field{Name: f.Name, Value: CopyValue(f.Value), Omitted: f.Omitted, Filled: f.Filled}
	}
	return cp
}

// bindArg is v as parameter p receives it: a copy, since a parameter is
// passed by value, except a method's ambient `this`, which is the receiver's
// own value. A program the interpreter prepares has that written back by the
// call (lower.RefParams); one it folds unlowered relies on the sharing.
func bindArg(v any, p *ir.Param) any {
	if p != nil && p.Receiver && p.Name == ir.ReceiverParam {
		return v
	}
	if p == nil {
		return v
	}
	return copyOf(v, p.Type)
}

// copyOf is v as a store takes it: a struct, a list or a map is a value
// (ir/valuecopy.go), so what is stored is a copy, as deep as t holds one. A
// handle is a map too, which is why the copy goes by the type and not by the
// Go value. With no type it copies a struct, as CopyValue does.
func copyOf(v any, t *ir.Type) any {
	if t == nil {
		return CopyValue(v)
	}
	if !ir.HoldsValue(t) {
		return v
	}
	if b, ok := v.(*Builder); ok {
		return b.clone()
	}
	switch t.Kind {
	case ir.TypeList:
		list, ok := v.([]any)
		if !ok {
			return v
		}
		out := make([]any, len(list))
		for i, x := range list {
			out[i] = copyOf(x, t.Elems[0])
		}
		return out
	case ir.TypeMap:
		m, ok := v.(map[string]any)
		if !ok || len(t.Elems) < 2 {
			return v
		}
		out := make(map[string]any, len(m))
		for k, x := range m {
			out[k] = copyOf(x, t.Elems[1])
		}
		return out
	case ir.TypeOption, ir.TypeReadonly:
		if v == nil {
			return nil
		}
		return copyOf(v, t.Elems[0])
	case ir.TypeStruct:
		s, ok := v.(*Struct)
		if !ok || s == nil {
			return v
		}
		fieldTypes := map[string]*ir.Type{}
		for _, f := range ir.CopiedFields(t) {
			fieldTypes[f.Field.Name] = f.Type
		}
		cp := &Struct{Def: s.Def, Type: s.Type, Fields: make([]Field, len(s.Fields)), Handle: s.Handle}
		for i, f := range s.Fields {
			if ft, ok := fieldTypes[f.Name]; ok {
				cp.Fields[i] = Field{Name: f.Name, Value: copyOf(f.Value, ft), Omitted: f.Omitted, Filled: f.Filled}
			} else {
				cp.Fields[i] = Field{Name: f.Name, Value: CopyValue(f.Value), Omitted: f.Omitted, Filled: f.Filled}
			}
		}
		return cp
	}
	return v
}

// DeepCopy copies a value nothing says the type of: a list and a struct are
// copied, as deep as they go, and anything else is returned as it is. It is
// the value.copy intrinsic for a caller holding no IR type -- the optimizer's
// fold and a lowered body run here.
func DeepCopy(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = DeepCopy(e)
		}
		return out
	case *Struct:
		if x == nil {
			return x
		}
		cp := &Struct{Def: x.Def, Type: x.Type, Fields: make([]Field, len(x.Fields)), Handle: x.Handle}
		for i, f := range x.Fields {
			cp.Fields[i] = Field{Name: f.Name, Value: DeepCopy(f.Value), Omitted: f.Omitted, Filled: f.Filled}
		}
		return cp
	}
	return v
}

// Name returns the declared type's name, or "" for an anonymous literal.
func (s *Struct) Name() string {
	if s == nil || s.Def == nil {
		return ""
	}
	return s.Def.Name
}

// Get returns the value of a field and whether the struct has it.
func (s *Struct) Get(name string) (any, bool) {
	if s == nil {
		return nil, false
	}
	for _, f := range s.Fields {
		if f.Name == name {
			return f.Value, true
		}
	}
	return nil, false
}

// Set assigns a field, appending it when the struct does not have it yet. A
// field written twice keeps the place of its first write, which is the place
// the checker gave it.
func (s *Struct) Set(name string, v any) {
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			s.Fields[i].Value = v
			s.Fields[i].Omitted, s.Fields[i].Filled = false, nil
			return
		}
	}
	s.Fields = append(s.Fields, Field{Name: name, Value: v})
}

// Merge assigns every field of src, which is how a spread supplies fields it
// holds at runtime.
func (s *Struct) Merge(src *Struct) {
	if src == nil {
		return
	}
	for _, f := range src.Fields {
		s.Set(f.Name, f.Value)
		// What src's own literal left out, this one did not write either --
		// where the two are one declaration, whose omission means the same.
		if f.Omitted && src.Def != nil && src.Def == s.Def {
			i := slices.IndexFunc(s.Fields, func(g Field) bool { return g.Name == f.Name })
			s.Fields[i].Omitted, s.Fields[i].Filled = true, f.Filled
		}
	}
}

// String renders the value the way a struct literal is written. A value gets
// stringified by interpolation and by the equality test, so the form has to be
// deterministic; the field order is the checker's, which is the order every
// backend emits.
func (s *Struct) String() string {
	if s == nil {
		return "null"
	}
	var b strings.Builder
	b.WriteString("{")
	for i, f := range s.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s = %v", f.Name, f.Value)
	}
	b.WriteString("}")
	return b.String()
}

// Clone returns a copy that shares no mutable state with s. Values that are
// themselves composite are copied too: a callee assigning through one must not
// reach the caller's value, and IR is spliced from folded values at more than
// one site.
func (s *Struct) Clone() *Struct {
	if s == nil {
		return nil
	}
	out := &Struct{Def: s.Def, Type: s.Type, Fields: make([]Field, len(s.Fields)), Handle: s.Handle}
	for i, f := range s.Fields {
		out.Fields[i] = Field{Name: f.Name, Value: CloneValue(f.Value), Omitted: f.Omitted, Filled: f.Filled}
	}
	return out
}

// CloneValue deep-copies a runtime value. Scalars are returned as they are.
func CloneValue(v any) any {
	switch x := v.(type) {
	case *Struct:
		return x.Clone()
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = CloneValue(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, el := range x {
			out[i] = CloneValue(el)
		}
		return out
	}
	return v
}
