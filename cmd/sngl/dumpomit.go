package main

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"duckfam.us/sngl/ir"
)

// dumpTree is v as the structural formats write it: a tree of its fields with
// JSON's names and omissions, without the fields omit names, and with every
// declaration written once, where it is owned. Anywhere else a declaration is
// a dumpRef naming it, so a parameter's type does not write its component
// again and a call does not write its callee's body. It is built rather than
// the IR being encoded directly because what a dump reaches is shared -- the
// library IR every check reads and the ASTs parsed once per process -- and
// because an IR type may be encoded elsewhere with no notion of ownership.
func dumpTree(v any, omit map[string]bool, spew bool) any {
	t := treeBuilder{omit: omit, open: map[pointerKey]bool{}, stringers: spew}
	if pkg, ok := v.(*ir.Package); ok && pkg != nil {
		t.library = pkg.Library
	}
	tree := t.build(reflect.ValueOf(v), true)
	if spew {
		return spewTree(tree)
	}
	return tree
}

// dumpRef stands for a declaration written where it is owned. A library
// declaration is written nowhere else in a dump, so its Pkg is what finds it.
type dumpRef struct {
	Ref  string
	Name string
	Pkg  string `json:",omitempty"`
}

// dumpCycle stands for a value already being written further up the path.
type dumpCycle struct {
	Cycle string
}

// dumpObject is a struct or map, its fields in the order JSON writes them.
type dumpObject struct {
	typ    string
	fields []dumpField
}

type dumpField struct {
	name  string
	value any
}

func (o *dumpObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range o.fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, _ := json.Marshal(f.name)
		buf.Write(k)
		buf.WriteByte(':')
		b, err := json.Marshal(f.value)
		if err != nil {
			return nil, err
		}
		buf.Write(b)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// spewObject is a dumpObject as spew prints it: an unexported field would
// print, so the type is named in a field and the fields are a map spew sorts.
type spewObject struct {
	Type   string
	Fields map[string]any
}

func spewTree(v any) any {
	switch v := v.(type) {
	case *dumpObject:
		fields := make(map[string]any, len(v.fields))
		for _, f := range v.fields {
			fields[f.name] = spewTree(f.value)
		}
		return &spewObject{Type: v.typ, Fields: fields}
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = spewTree(e)
		}
		return out
	}
	return v
}

var (
	symbolType        = reflect.TypeFor[ir.Symbol]()
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	stringerType      = reflect.TypeFor[fmt.Stringer]()
)

// ownership names the fields that break the default, which is that a list
// or a map holds what is in it and a single pointer refers to something held
// elsewhere: a local's symbol and a lambda's function are its own, and the
// vars a function reads and writes and the functions a points-to site lists
// are not.
var ownership = map[reflect.Type]map[string]bool{
	reflect.TypeFor[ir.LocalVar]():     {"Sym": true},
	reflect.TypeFor[ir.For]():          {"KeySym": true, "ValueSym": true},
	reflect.TypeFor[ir.Lambda]():       {"Func": true},
	reflect.TypeFor[ir.EventHandler](): {"Func": true},
	reflect.TypeFor[ir.Func]():         {"Reads": false, "Writes": false},
	reflect.TypeFor[ir.PointsToInfo](): {"Sites": false},
}

func owns(parent reflect.Type, sf reflect.StructField) bool {
	if own, ok := ownership[parent][sf.Name]; ok {
		return own
	}
	switch sf.Type.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return true
	}
	return false
}

type treeBuilder struct {
	omit map[string]bool
	// open is the pointers on the path being written, keyed by type as well
	// since a struct and its first field share an address.
	open map[pointerKey]bool
	// library says the package dumped is a library's, whose contexts are its
	// own rather than adopted from an import.
	library bool
	// stringers writes a value that has a String method as that string, as
	// spew prints one: a position as file:line:col, an operator as itself.
	stringers bool
}

type pointerKey struct {
	addr uintptr
	typ  reflect.Type
}

func (t treeBuilder) build(v reflect.Value, owned bool) any {
	if !v.IsValid() {
		return nil
	}
	if marshals(v.Type()) {
		return v.Interface()
	}
	if t.stringers && v.Kind() != reflect.Interface && v.Type().Implements(stringerType) && !(v.Kind() == reflect.Pointer && v.IsNil()) {
		return v.Interface().(fmt.Stringer).String()
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		if (!owned || t.adopted(v)) && v.Type().Implements(symbolType) {
			return declRef(v)
		}
		key := pointerKey{v.Pointer(), v.Type()}
		if t.open[key] {
			return dumpCycle{Cycle: v.Type().Elem().String()}
		}
		t.open[key] = true
		defer delete(t.open, key)
		return t.build(v.Elem(), owned)

	case reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return t.build(v.Elem(), owned)

	case reflect.Struct:
		o := &dumpObject{typ: v.Type().String()}
		t.fields(o, v, map[string]bool{})
		return o

	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return v.Interface()
		}
		out := make([]any, v.Len())
		for i := range v.Len() {
			out[i] = t.build(v.Index(i), owned)
		}
		return out

	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		o := &dumpObject{typ: v.Type().String()}
		iter := v.MapRange()
		for iter.Next() {
			o.fields = append(o.fields, dumpField{t.mapKey(iter.Key()), t.build(iter.Value(), owned)})
		}
		sort.Slice(o.fields, func(i, j int) bool { return o.fields[i].name < o.fields[j].name })
		return o
	}
	return v.Interface()
}

// fields appends v's fields to o as JSON writes them, an embedded struct's
// promoted unless o already has the name. seen is the names written so far.
func (t treeBuilder) fields(o *dumpObject, v reflect.Value, seen map[string]bool) {
	typ := v.Type()
	var embedded []int
	for i := range typ.NumField() {
		sf := typ.Field(i)
		name, opts, skip := jsonField(sf)
		if skip || t.omit[sf.Name] {
			continue
		}
		f := v.Field(i)
		if sf.Anonymous && name == "" && promotes(sf.Type) {
			embedded = append(embedded, i)
			continue
		}
		if name == "" {
			name = sf.Name
		}
		if seen[name] || omits(f, opts) {
			continue
		}
		seen[name] = true
		o.fields = append(o.fields, dumpField{name, t.build(f, owns(typ, sf))})
	}
	for _, i := range embedded {
		f := v.Field(i)
		if f.Kind() == reflect.Pointer {
			if f.IsNil() {
				continue
			}
			f = f.Elem()
		}
		t.fields(o, f, seen)
	}
}

func (t treeBuilder) mapKey(k reflect.Value) string {
	if k.Kind() == reflect.String {
		return k.String()
	}
	if k.Type().Implements(textMarshalerType) {
		if b, err := k.Interface().(encoding.TextMarshaler).MarshalText(); err == nil {
			return string(b)
		}
	}
	switch k.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(k.Uint(), 10)
	}
	b, _ := json.Marshal(t.build(k, false))
	return string(b)
}

func declRef(v reflect.Value) dumpRef {
	r := dumpRef{Ref: v.Type().Elem().String(), Name: v.Interface().(ir.Symbol).SymName()}
	if d := v.Elem().FieldByName("DeclInfo"); d.IsValid() {
		if pkg, _ := d.FieldByName("Pkg").Interface().(*ir.Package); pkg != nil {
			r.Pkg = pkg.Path
		}
	}
	return r
}

func marshals(t reflect.Type) bool {
	return t.Implements(jsonMarshalerType) || t.Implements(textMarshalerType)
}

func promotes(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct
}

func jsonField(sf reflect.StructField) (name, opts string, skip bool) {
	tag := sf.Tag.Get("json")
	if !sf.IsExported() || tag == "-" {
		return "", "", true
	}
	name, opts, _ = strings.Cut(tag, ",")
	return name, opts, false
}

func omits(v reflect.Value, opts string) bool {
	for opt := range strings.SplitSeq(opts, ",") {
		switch opt {
		case "omitempty":
			switch v.Kind() {
			case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
				if v.Len() == 0 {
					return true
				}
			case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
				reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
				if v.IsZero() {
					return true
				}
			}
		case "omitzero":
			if v.Kind() == reflect.Pointer && v.IsNil() {
				return true
			}
			if z, ok := v.Interface().(interface{ IsZero() bool }); ok {
				if z.IsZero() {
					return true
				}
			} else if v.IsZero() {
				return true
			}
		}
	}
	return false
}

// adopted says v is a context a program's package holds because a library it
// imports declared it: the library's to write, as Convert leaves it out.
func (t treeBuilder) adopted(v reflect.Value) bool {
	ctx, ok := v.Interface().(*ir.Context)
	return ok && ctx.Stdlib && !t.library
}
