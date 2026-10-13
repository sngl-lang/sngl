package checker

import (
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

const anonStructPrefix = ir.AnonStructPrefix

// rejectReservedName keeps a program out of the namespace the interned
// declarations are named in. Nothing else can: they are registered in
// pkg.Structs without being bound in any scope, so no lookup ever meets one.
// Both binding funnels ask -- claimTopLevel and checker.declare.
func (c *checker) rejectReservedName(pos ast.Pos, name string) bool {
	if !strings.HasPrefix(name, anonStructPrefix) {
		return false
	}
	c.error(pos, "%q is reserved: names beginning with %q are the compiler's own", name, anonStructPrefix)
	return true
}

// anonKey spells a field set for the intern map: it tells every declaration
// apart (ir.AnonTypeKey), where ir.AnonStructBaseName sees only what is stable
// across runs.
func anonKey(canon []*ir.StructField) string {
	keys := make([]string, len(canon))
	for i, f := range canon {
		keys[i] = f.Name + " " + ir.AnonTypeKey(f.Type, true)
	}
	return strings.Join(keys, "; ")
}

// anonStructName names the declaration in generated code. It must be one
// identifier in Go, Kotlin and JS at once, which leaves only letters, digits
// and underscore to build it from. Two declarations can share a base name --
// a local `Style` and an imported one spell one signature -- so a taken name
// gets a counter rather than a second `type` of that name in the output.
func (t *declTables) anonStructName(pkg *ir.Package, canon []*ir.StructField) string {
	base := ir.AnonStructBaseName(canon)
	if t.anonNames[pkg] == nil {
		t.anonNames[pkg] = map[string]bool{}
	}
	name := base
	for n := 2; t.anonNames[pkg][name]; n++ {
		name = base + "_" + strconv.Itoa(n)
	}
	t.anonNames[pkg][name] = true
	return name
}

// internAnonStruct returns the one *ir.StructDef this package uses for an
// anonymous struct with these fields, registering it in pkg.Structs the first
// time. Keyed by package as well as signature: `sngl:builtin` interns
// `struct {}` for `effect[T = struct {}]` before a program is parsed, and
// sharing that would hand the program a decl its own codegen never emits.
func (c *checker) internAnonStruct(fields []*ir.StructField) *ir.StructDef {
	canon := slices.SortedStableFunc(slices.Values(fields), func(a, b *ir.StructField) int {
		return strings.Compare(a.Name, b.Name)
	})
	key := anonKey(canon)
	pkg := c.declPkg()
	t := c.tables()
	if t.anonStructs == nil {
		t.anonStructs = map[*ir.Package]map[string]*ir.StructDef{}
		t.anonNames = map[*ir.Package]map[string]bool{}
	}
	if t.anonStructs[pkg] == nil {
		t.anonStructs[pkg] = map[string]*ir.StructDef{}
	}
	if sd, ok := t.anonStructs[pkg][key]; ok {
		return sd
	}
	sd := &ir.StructDef{
		Name:   t.anonStructName(pkg, canon),
		Pkg:    c.declPkg(),
		Fields: canon,
		Anon:   true,
	}
	t.anonStructs[pkg][key] = sd
	pkg.Structs = append(pkg.Structs, sd)
	return sd
}

// anonFieldsFromInits types an anonymous struct literal by its own values.
// Giving up leaves it the decl-less struct it was. A spread names no field
// set.
// A field naming a type parameter is kept: anonStructType declares such a
// struct un-interned. A duplicate name is reported by the caller, which sees
// the declared literals too.
func (c *checker) anonFieldsFromInits(inits []ir.FieldInit) ([]*ir.StructField, bool) {
	fields := make([]*ir.StructField, 0, len(inits))
	seen := map[string]bool{}
	for _, fi := range inits {
		if fi.Spread || fi.Name == "" || seen[fi.Name] {
			return nil, false
		}
		seen[fi.Name] = true
		t := exprType(fi.Value)
		if t == nil || t.Kind == ir.TypeInvalid || t.Kind == ir.TypeVoid || t.Kind == ir.TypeDyn {
			return nil, false
		}
		fields = append(fields, &ir.StructField{Name: fi.Name, Type: t})
	}
	return fields, true
}

// anonStructType is the anonymous struct with these fields. One whose fields
// name a type parameter is declared here and interned nowhere: its fields mean
// the parameters in scope where it was written and no other, so two
// declarations each writing `struct { v T }` get one each, and substituting
// it (ir.Type.Substitute) builds another rather than writing a shared one.
// Everything else is the package's interned declaration.
func (c *checker) anonStructType(fields []*ir.StructField) *ir.Type {
	if !slices.ContainsFunc(fields, func(f *ir.StructField) bool { return f.Type.HasTypeParam() }) {
		return c.internAnonStruct(fields).SymType()
	}
	canon := slices.SortedStableFunc(slices.Values(fields), func(a, b *ir.StructField) int {
		return strings.Compare(a.Name, b.Name)
	})
	return (&ir.StructDef{Pkg: c.declPkg(), Fields: canon, Anon: true}).SymType()
}
