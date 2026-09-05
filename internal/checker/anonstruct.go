package checker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

const anonStructPrefix = "__anon_"

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

// anonSignature spells a field set in name order, so field order is not part
// of a type's identity. The two spellings answer different questions: key
// tells every declaration apart and is the intern map's, sig is stable across
// runs and is all the generated name is allowed to see.
func anonSignature(canon []*ir.StructField) (sig, key string) {
	sigs := make([]string, len(canon))
	keys := make([]string, len(canon))
	for i, f := range canon {
		sigs[i] = f.Name + " " + anonTypeKey(f.Type, false)
		keys[i] = f.Name + " " + anonTypeKey(f.Type, true)
	}
	return strings.Join(sigs, "; "), strings.Join(keys, "; ")
}

// anonTypeKey spells a type for the signature. Type.String() alone would not:
// it prints a named type unqualified, so a program's own `Style` and
// `sngl:ui`'s read alike.
func anonTypeKey(t *ir.Type, exact bool) string {
	var b strings.Builder
	b.WriteString(t.String())
	writeDeclIDs(&b, t, exact)
	return b.String()
}

func writeDeclIDs(b *strings.Builder, t *ir.Type, exact bool) {
	if t == nil {
		return
	}
	if id, ok := declID(t.Decl, exact); ok {
		b.WriteString("@")
		b.WriteString(id)
	}
	for _, e := range t.Elems {
		writeDeclIDs(b, e, exact)
	}
	if t.Sig != nil {
		for _, p := range t.Sig.Params {
			writeDeclIDs(b, p.Type, exact)
		}
		writeDeclIDs(b, t.Sig.Return, exact)
	}
}

// declID is the declaring package of a named declaration. With exact set, one
// that records no package answers with its address instead: a name is not
// enough, since scope is per file and two files of one package may each
// declare `Style`. Never stable across runs, so only the intern map may see it.
func declID(sym ir.Symbol, exact bool) (string, bool) {
	var pkg string
	switch d := sym.(type) {
	case *ir.StructDef:
		pkg = d.Pkg
	case *ir.EnumDef:
		pkg = d.Pkg
	case *ir.UnitDef:
		pkg = d.Pkg
	case *ir.Component:
		pkg = d.Pkg
	default:
		return "", false
	}
	if pkg == "" && exact {
		return fmt.Sprintf("%p", sym), true
	}
	return pkg, true
}

// anonStructName names the declaration in generated code. It must be one
// identifier in Go, Kotlin and JS at once, which leaves only letters, digits
// and underscore to build it from. Two declarations can share a base name --
// a local `Style` and an imported one spell one signature -- so a taken name
// gets a counter rather than a second `type` of that name in the output.
func (c *checker) anonStructName(pkg *ir.Package, canon []*ir.StructField, sig string) string {
	var b strings.Builder
	b.WriteString(anonStructPrefix)
	for _, f := range canon {
		b.WriteString(f.Name)
		b.WriteString("_")
	}
	sum := sha256.Sum256([]byte(sig))
	b.WriteString(hex.EncodeToString(sum[:])[:6])
	base := b.String()
	if c.anonNames[pkg] == nil {
		c.anonNames[pkg] = map[string]bool{}
	}
	name := base
	for n := 2; c.anonNames[pkg][name]; n++ {
		name = base + "_" + strconv.Itoa(n)
	}
	c.anonNames[pkg][name] = true
	return name
}

// internAnonStruct returns the one *ir.StructDef this package uses for an
// anonymous struct with these fields, registering it in pkg.Structs the first
// time. Keyed by package as well as signature: `sngl:builtin` interns
// `struct {}` for `effect<T = struct {}>` before a program is parsed, and
// sharing that would hand the program a decl its own codegen never emits.
func (c *checker) internAnonStruct(fields []*ir.StructField) *ir.StructDef {
	canon := slices.SortedStableFunc(slices.Values(fields), func(a, b *ir.StructField) int {
		return strings.Compare(a.Name, b.Name)
	})
	sig, key := anonSignature(canon)
	pkg := c.declPkg()
	if c.anonStructs == nil {
		c.anonStructs = map[*ir.Package]map[string]*ir.StructDef{}
		c.anonNames = map[*ir.Package]map[string]bool{}
	}
	if c.anonStructs[pkg] == nil {
		c.anonStructs[pkg] = map[string]*ir.StructDef{}
	}
	if sd, ok := c.anonStructs[pkg][key]; ok {
		return sd
	}
	sd := &ir.StructDef{
		Name:   c.anonStructName(pkg, canon, sig),
		Pkg:    c.libPkgName,
		Fields: canon,
		Anon:   true,
	}
	c.anonStructs[pkg][key] = sd
	pkg.Structs = append(pkg.Structs, sd)
	return sd
}

// anonFieldsFromInits types an anonymous struct literal by its own values.
// Giving up leaves it the decl-less struct it was. A spread names no field
// set; an unbound type parameter has no one type, and interning it would give
// every instantiation one declaration whose field is `T`. A duplicate name is
// reported by the caller, which sees the declared literals too.
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
		if hasTypeParam(t) {
			return nil, false
		}
		fields = append(fields, &ir.StructField{Name: fi.Name, Type: t})
	}
	return fields, true
}

func hasTypeParam(t *ir.Type) bool {
	if t == nil {
		return false
	}
	if t.Kind == ir.TypeTypeParam {
		return true
	}
	if slices.ContainsFunc(t.Elems, hasTypeParam) {
		return true
	}
	if t.Sig != nil {
		if slices.ContainsFunc(t.Sig.Params, func(p *ir.Param) bool { return hasTypeParam(p.Type) }) {
			return true
		}
		return hasTypeParam(t.Sig.Return)
	}
	return false
}
