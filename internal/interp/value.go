package interp

import (
	"fmt"
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
// travelled in a "__type" entry among the fields, and a struct reached the map
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
}

// Field is one field of a Struct.
type Field struct {
	Name  string
	Value any
}

// NewStruct returns an empty struct value of the given declaration and type.
func NewStruct(def *ir.StructDef, typ *ir.Type) *Struct {
	return &Struct{Def: def, Type: typ}
}

// CopyValue is what declaring a local from a struct means: a struct is a value
// type, so `var next = this` gets its own, and editing it does not edit what
// the caller still holds. Anything else is returned as it is -- a list is a
// reference by design, which is what makes `items.push(4)` and
// `todos[i].done!!` write through.
//
// Only a local declaration copies. A parameter does not: `func translate(dx
// int, dy int) { this.x += dx }` is a method that edits its receiver, and the
// caller is meant to see it.
//
// Nested structs are copied too, since a field of a struct is as much a value
// as the struct is.
func CopyValue(v any) any {
	s, ok := v.(*Struct)
	if !ok || s == nil {
		return v
	}
	cp := &Struct{Def: s.Def, Type: s.Type, Fields: make([]Field, len(s.Fields))}
	for i, f := range s.Fields {
		cp.Fields[i] = Field{Name: f.Name, Value: CopyValue(f.Value)}
	}
	return cp
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
	out := &Struct{Def: s.Def, Type: s.Type, Fields: make([]Field, len(s.Fields))}
	for i, f := range s.Fields {
		out.Fields[i] = Field{Name: f.Name, Value: CloneValue(f.Value)}
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
