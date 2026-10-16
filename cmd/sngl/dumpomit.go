package main

import (
	"bytes"
	"cmp"
	"encoding"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// dumpTree is v as the structural formats write it: a tree of its fields with
// JSON's names, order and omissions, without the fields omit names, and with
// each declaration written in full once. It is built rather than the IR being
// encoded directly because what a dump reaches is shared -- the library IR
// every check reads and the ASTs parsed once per process -- and because an IR
// type may be encoded elsewhere with no notion of ownership.
//
// A declaration (an ir.Symbol) is written where the walk first meets it in a
// position that owns it, and named by a reference everywhere else. One that
// nothing in the dump owns -- a node's handle, an interned anonymous struct --
// is written at its first reference instead. One another package declares is
// only ever named: a program's dump does not write the library.
func dumpTree(v any, root *ir.Package, omit map[string]bool, spew, pointers bool) any {
	d := &dumper{
		omit:      omit,
		omitted:   map[reflect.Type][]jsonFieldInfo{},
		stringers: spew,
		pointers:  pointers,
		owned:     map[pointerKey]bool{},
		visited:   map[pointerKey]bool{},
		written:   map[pointerKey]bool{},
		ids:       map[pointerKey]int{},
		open:      map[pointerKey]bool{},
	}
	if root != nil {
		d.root = root
		d.contexts = contextPackages(root)
	}
	rv := reflect.ValueOf(v)
	d.mark(rv, true)
	for i := 0; i < len(d.refs); i++ {
		r := d.refs[i]
		if k := keyOf(r); !d.owned[k] && !d.visited[k] {
			d.visited[k] = true
			d.mark(r.Elem(), false)
		}
	}
	tree := d.build(rv, true)
	d.number()
	if spew {
		return spewTree(tree)
	}
	return tree
}

// dumpObject is a struct, a map or a reference, its fields in the order JSON
// writes them. typ is the Go type a struct had, which only spew prints.
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

func (o *dumpObject) add(name string, value any) {
	o.fields = append(o.fields, dumpField{name, value})
}

// spewTree is the tree as spew prints it: an object is a map, which spew
// sorts and counts as one level of --depth, with its Go type under "(type)".
func spewTree(v any) any {
	switch v := v.(type) {
	case *dumpObject:
		m := make(map[string]any, len(v.fields)+1)
		if v.typ != "" {
			m["(type)"] = v.typ
		}
		for _, f := range v.fields {
			m[f.name] = spewTree(f.value)
		}
		return m
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
// elsewhere.
var ownership = map[reflect.Type]map[string]bool{
	reflect.TypeFor[ir.LocalVar]():     {"Sym": true},
	reflect.TypeFor[ir.For]():          {"KeySym": true, "ValueSym": true},
	reflect.TypeFor[ir.Lambda]():       {"Func": true},
	reflect.TypeFor[ir.EventHandler](): {"Func": true},
	reflect.TypeFor[ir.Func]():         {"Reads": false, "Writes": false},
	reflect.TypeFor[ir.PointsToInfo](): {"Sites": false},
}

func owns(parent reflect.Type, f jsonFieldInfo) bool {
	if own, ok := ownership[parent][f.goName]; ok {
		return own
	}
	switch f.typ.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return true
	}
	return false
}

type dumper struct {
	root *ir.Package
	omit map[string]bool
	// omitted is each struct type's fields without the ones omit names.
	omitted  map[reflect.Type][]jsonFieldInfo
	contexts map[*ast.CallStmt]*ir.Package
	// stringers writes a value that has a String method as that string, as
	// spew prints one: a position as file:line:col, an operator as itself.
	stringers bool
	pointers  bool
	// refsOnly names every declaration, for spelling a map key, which writes
	// nothing.
	refsOnly bool
	// withIDs spells a reference with its declaration's id, which is known
	// once the tree is built.
	withIDs bool

	ids        map[pointerKey]int
	refRecords []refRecord
	maps       []mapRecord

	owned   map[pointerKey]bool
	visited map[pointerKey]bool
	refs    []reflect.Value
	written map[pointerKey]bool
	// open is the pointers on the path being written, keyed by type as well
	// since a struct and its first field share an address.
	open map[pointerKey]bool
}

type pointerKey struct {
	addr uintptr
	typ  reflect.Type
}

func keyOf(v reflect.Value) pointerKey { return pointerKey{v.Pointer(), v.Type()} }

func isSymbol(v reflect.Value) bool {
	return v.Kind() == reflect.Pointer && v.Type().Implements(symbolType)
}

// mark finds what build will write: each declaration some written position
// owns, and the references to the rest, which build writes at the first.
func (d *dumper) mark(v reflect.Value, owning bool) {
	if !v.IsValid() {
		return
	}
	if _, ok := d.leaf(v); ok {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		k := keyOf(v)
		if isSymbol(v) {
			if d.foreign(v) {
				return
			}
			if !owning {
				d.refs = append(d.refs, v)
				return
			}
			d.owned[k] = true
		}
		if d.visited[k] {
			return
		}
		d.visited[k] = true
		d.mark(v.Elem(), owning)
	case reflect.Interface:
		if !v.IsNil() {
			d.mark(v.Elem(), owning)
		}
	case reflect.Struct:
		for _, f := range d.typeFields(v.Type()) {
			if fv, ok := fieldValue(v, f.index); ok {
				d.mark(fv, owns(fieldParent(v, f.index), f))
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			d.mark(v.Index(i), owning)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			d.mark(iter.Value(), owning)
		}
	}
}

func (d *dumper) build(v reflect.Value, owning bool) any {
	if !v.IsValid() {
		return nil
	}
	if leaf, ok := d.leaf(v); ok {
		return leaf
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		k := keyOf(v)
		if isSymbol(v) {
			if d.refsOnly || d.written[k] || d.foreign(v) || !owning && d.owned[k] {
				return d.ref(v)
			}
			d.written[k] = true
			d.ids[k] = len(d.ids) + 1
		}
		if d.open[k] {
			o := &dumpObject{}
			o.add("Cycle", v.Type().Elem().String())
			return o
		}
		d.open[k] = true
		defer delete(d.open, k)
		out := d.build(v.Elem(), owning)
		if o, ok := out.(*dumpObject); ok {
			var head []dumpField
			if d.pointers {
				head = append(head, dumpField{"(addr)", fmt.Sprintf("%#x", v.Pointer())})
			}
			if id, ok := d.ids[k]; ok && isSymbol(v) {
				head = append(head, dumpField{"(id)", id})
			}
			o.fields = append(head, o.fields...)
		}
		return out

	case reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return d.build(v.Elem(), owning)

	case reflect.Struct:
		o := &dumpObject{typ: v.Type().String()}
		for _, f := range d.typeFields(v.Type()) {
			fv, ok := fieldValue(v, f.index)
			if !ok || omits(fv, f.opts) {
				continue
			}
			o.add(f.name, d.build(fv, owns(fieldParent(v, f.index), f)))
		}
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
			out[i] = d.build(v.Index(i), owning)
		}
		return out

	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		return d.buildMap(v, owning)
	}
	return v.Interface()
}

// buildMap writes a map's entries in an order its keys' spelling decides,
// which a map keyed by pointers needs: its iteration order is random, and
// which entry first references a declaration decides where it is written,
// and so its id. A run of keys spelled alike -- two declarations of one name
// -- is ordered by the entries' values. Once every declaration has its id
// the keys are spelled again with it (number), which tells such keys apart.
func (d *dumper) buildMap(v reflect.Value, owning bool) *dumpObject {
	type entry struct {
		key, tie string
		k, value reflect.Value
	}
	entries := make([]entry, 0, v.Len())
	iter := v.MapRange()
	for iter.Next() {
		entries = append(entries, entry{key: d.mapKey(iter.Key()), k: iter.Key(), value: iter.Value()})
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.key, b.key) })
	for i := range entries {
		if i > 0 && entries[i].key == entries[i-1].key || i+1 < len(entries) && entries[i].key == entries[i+1].key {
			entries[i].tie = d.spell(entries[i].value)
		}
	}
	slices.SortFunc(entries, func(a, b entry) int {
		return cmp.Or(strings.Compare(a.key, b.key), strings.Compare(a.tie, b.tie))
	})
	o := &dumpObject{}
	rec := mapRecord{obj: o}
	for _, e := range entries {
		o.add(e.key, d.build(e.value, owning))
		rec.keys = append(rec.keys, e.k)
	}
	if !d.refsOnly {
		d.maps = append(d.maps, rec)
	}
	return o
}

type mapRecord struct {
	obj  *dumpObject
	keys []reflect.Value
}

type refRecord struct {
	obj    *dumpObject
	target pointerKey
}

// number gives each reference the id of the declaration it names, and each
// map its keys spelled with those ids, distinct.
func (d *dumper) number() {
	for _, r := range d.refRecords {
		if id, ok := d.ids[r.target]; ok {
			r.obj.add("(id)", id)
		}
	}
	d.withIDs = true
	for _, m := range d.maps {
		for i, k := range m.keys {
			m.obj.fields[i].name = d.mapKey(k)
		}
		slices.SortStableFunc(m.obj.fields, func(a, b dumpField) int { return strings.Compare(a.name, b.name) })
		dedupeKeys(m.obj.fields)
	}
}

// dedupeKeys numbers the second and later of keys still spelled alike.
func dedupeKeys(fields []dumpField) {
	for i := 0; i < len(fields); {
		j := i + 1
		for j < len(fields) && fields[j].name == fields[i].name {
			j++
		}
		for n := i + 1; n < j; n++ {
			fields[n].name += " #" + strconv.Itoa(n-i+1)
		}
		i = j
	}
}

func (d *dumper) mapKey(k reflect.Value) string {
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
	return d.spell(k)
}

// spell is v as compact JSON with every declaration named, writing nothing.
func (d *dumper) spell(v reflect.Value) string {
	s := *d
	s.refsOnly = true
	s.pointers = false
	b, _ := json.Marshal(s.build(v, false))
	return string(b)
}

// ref names a declaration: its kind, its name, the package that declares it
// where that is not the one dumped, and where it is written in source.
func (d *dumper) ref(v reflect.Value) *dumpObject {
	o := &dumpObject{}
	if d.pointers {
		o.add("(addr)", fmt.Sprintf("%#x", v.Pointer()))
	}
	o.add("(ref)", v.Type().Elem().String())
	o.add("Name", v.Interface().(ir.Symbol).SymName())
	if pkg := d.pkgName(v); pkg != "" {
		o.add("Pkg", pkg)
	}
	if at := declPos(v); at != "" && !d.omit["Pos"] && !d.omit["At"] {
		o.add("At", at)
	}
	switch k := keyOf(v); {
	case d.withIDs:
		if id, ok := d.ids[k]; ok {
			o.add("(id)", id)
		}
	case !d.refsOnly:
		d.refRecords = append(d.refRecords, refRecord{o, k})
	}
	return o
}

func (d *dumper) declPkg(v reflect.Value) *ir.Package {
	if ctx, ok := v.Interface().(*ir.Context); ok {
		return d.contexts[ctx.AST]
	}
	if info := v.Elem().FieldByName("DeclInfo"); info.IsValid() {
		pkg, _ := info.FieldByName("Pkg").Interface().(*ir.Package)
		return pkg
	}
	return nil
}

func (d *dumper) pkgName(v reflect.Value) string {
	pkg := d.declPkg(v)
	if ctx, ok := v.Interface().(*ir.Context); ok && pkg == nil && d.foreign(v) && ctx.AST != nil {
		// A context a target package declares is adopted without the program
		// importing it; lib/<path> is sngl:<path>, and its files are named so.
		return "sngl:" + path.Dir(ctx.AST.Pos.File)
	}
	switch {
	case pkg == nil || pkg == d.root:
		return ""
	case pkg.Path != "":
		return pkg.Path
	case pkg.Origin != nil && pkg.Origin.URI != "":
		return pkg.Origin.URI
	case pkg.Origin != nil:
		return "dir:" + pkg.Origin.Dir
	}
	return "(unnamed package)"
}

// foreign says another package declares v, so this dump only names it.
// Nothing is, with no package being dumped. A context a target package
// declares reaches the program with no import to find it through, and says
// it came from the library by Stdlib alone.
func (d *dumper) foreign(v reflect.Value) bool {
	if d.root == nil {
		return false
	}
	if ctx, ok := v.Interface().(*ir.Context); ok {
		return d.contexts[ctx.AST] != nil || ctx.Stdlib && !d.root.Library
	}
	pkg := d.declPkg(v)
	return pkg != nil && pkg != d.root
}

// contextPackages is which package declares each context the import graph
// reaches, by the statement that declares it: the IR keeps no link from a
// context to its package, and a directory package's contexts are on the
// program's list rather than its own, so its files are what say. A library
// package also holds the contexts it adopted from its imports, so each
// package claims only after its imports have claimed theirs.
func contextPackages(root *ir.Package) map[*ast.CallStmt]*ir.Package {
	out := map[*ast.CallStmt]*ir.Package{}
	claim := func(s *ast.CallStmt, p *ir.Package) {
		if _, ok := out[s]; s != nil && !ok {
			out[s] = p
		}
	}
	seen := map[*ir.Package]bool{root: true}
	var walk func(p *ir.Package)
	walk = func(p *ir.Package) {
		for _, imp := range p.Imports {
			if imp == nil || imp.Pkg == nil || seen[imp.Pkg] {
				continue
			}
			seen[imp.Pkg] = true
			walk(imp.Pkg)
			for _, ctx := range imp.Pkg.Contexts {
				claim(ctx.AST, imp.Pkg)
			}
			if o := imp.Pkg.Origin; o != nil {
				for _, doc := range o.Docs {
					for _, s := range doc.Stmts {
						if call, ok := s.(*ast.CallStmt); ok {
							claim(call, imp.Pkg)
						}
					}
				}
			}
		}
	}
	walk(root)
	return out
}

// declPos is where v's AST says it was written, or "" for a declaration
// with none.
func declPos(v reflect.Value) string {
	a := v.Elem().FieldByName("AST")
	for a.IsValid() && (a.Kind() == reflect.Interface || a.Kind() == reflect.Pointer) {
		if a.IsNil() {
			return ""
		}
		a = a.Elem()
	}
	if !a.IsValid() || a.Kind() != reflect.Struct {
		return ""
	}
	pos := a.FieldByName("Pos")
	if !pos.IsValid() {
		return ""
	}
	if p, ok := pos.Interface().(fmt.Stringer); ok {
		if s := p.String(); s != "" && !strings.HasPrefix(s, ":") {
			return s
		}
	}
	return ""
}

// leaf is what v is written as when nothing inside it is: a value with its
// own JSON or text encoding, and under spew one with a String method.
func (d *dumper) leaf(v reflect.Value) (any, bool) {
	t := v.Type()
	if t.Implements(jsonMarshalerType) || t.Implements(textMarshalerType) {
		return v.Interface(), true
	}
	if d.stringers && v.Kind() != reflect.Interface && t.Implements(stringerType) && !(v.Kind() == reflect.Pointer && v.IsNil()) {
		return v.Interface().(fmt.Stringer).String(), true
	}
	return nil, false
}

// jsonFieldInfo is one field JSON writes of a struct, promoted fields
// included: its key, its path from the struct, and its options.
type jsonFieldInfo struct {
	name   string
	goName string
	index  []int
	tagged bool
	opts   string
	typ    reflect.Type
}

var typeFieldsCache sync.Map

// typeFields is encoding/json's: each field it writes, a promoted one where
// the embedding field stands, a name claimed at two depths going to the
// shallower and at one depth twice to neither unless one is tagged.
func (d *dumper) typeFields(t reflect.Type) []jsonFieldInfo {
	var all []jsonFieldInfo
	if c, ok := typeFieldsCache.Load(t); ok {
		all = c.([]jsonFieldInfo)
	} else {
		all = jsonTypeFields(t)
		typeFieldsCache.Store(t, all)
	}
	if len(d.omit) == 0 {
		return all
	}
	if out, ok := d.omitted[t]; ok {
		return out
	}
	out := make([]jsonFieldInfo, 0, len(all))
	for _, f := range all {
		if !d.omit[f.goName] {
			out = append(out, f)
		}
	}
	d.omitted[t] = out
	return out
}

func jsonTypeFields(t reflect.Type) []jsonFieldInfo {
	type level struct {
		typ   reflect.Type
		index []int
	}
	var fields []jsonFieldInfo
	next := []level{{typ: t}}
	var count, nextCount map[reflect.Type]int
	visited := map[reflect.Type]bool{}
	for len(next) > 0 {
		current := next
		next = nil
		count, nextCount = nextCount, map[reflect.Type]int{}
		for _, f := range current {
			if visited[f.typ] {
				continue
			}
			visited[f.typ] = true
			for i := range f.typ.NumField() {
				sf := f.typ.Field(i)
				if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, opts, _ := strings.Cut(tag, ",")
				index := append(slices.Clone(f.index), i)
				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					field := jsonFieldInfo{name: name, goName: sf.Name, index: index, tagged: name != "", opts: opts, typ: sf.Type}
					if field.name == "" {
						field.name = sf.Name
					}
					fields = append(fields, field)
					if count[f.typ] > 1 {
						fields = append(fields, field)
					}
					continue
				}
				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, level{typ: ft, index: index})
				}
			}
		}
	}
	slices.SortFunc(fields, func(a, b jsonFieldInfo) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.index), len(b.index)); c != 0 {
			return c
		}
		if a.tagged != b.tagged {
			if a.tagged {
				return -1
			}
			return 1
		}
		return slices.Compare(a.index, b.index)
	})
	var out []jsonFieldInfo
	for i := 0; i < len(fields); {
		j := i + 1
		for j < len(fields) && fields[j].name == fields[i].name {
			j++
		}
		group := fields[i:j]
		if len(group) == 1 || len(group[0].index) != len(group[1].index) || group[0].tagged != group[1].tagged {
			out = append(out, group[0])
		}
		i = j
	}
	slices.SortFunc(out, func(a, b jsonFieldInfo) int { return slices.Compare(a.index, b.index) })
	return out
}

// fieldValue is the field at index, or false where an embedded pointer on
// the way is nil, which JSON writes nothing for.
func fieldValue(v reflect.Value, index []int) (reflect.Value, bool) {
	for i, x := range index {
		if i > 0 && v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}, false
			}
			v = v.Elem()
		}
		v = v.Field(x)
	}
	return v, true
}

// fieldParent is the struct type that declares the field at index, which is
// what ownership is keyed by.
func fieldParent(v reflect.Value, index []int) reflect.Type {
	t := v.Type()
	for _, x := range index[:len(index)-1] {
		t = t.Field(x).Type
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
	}
	return t
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
