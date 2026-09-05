package checker

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
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

// anonSignature is the canonical spelling of a field set, in name order, so
// field order is not part of a type's identity.
func anonSignature(canon []*ir.StructField) string {
	parts := make([]string, len(canon))
	for i, f := range canon {
		parts[i] = f.Name + " " + anonTypeKey(f.Type)
	}
	return strings.Join(parts, "; ")
}

// anonTypeKey spells a type for the signature. Type.String() alone would not:
// it prints a named type unqualified, so a program's own `Style` and
// `sngl:ui`'s read alike.
func anonTypeKey(t *ir.Type) string {
	var b strings.Builder
	b.WriteString(t.String())
	writeDeclPkgs(&b, t)
	return b.String()
}

func writeDeclPkgs(b *strings.Builder, t *ir.Type) {
	if t == nil {
		return
	}
	if pkg, ok := declPkgURI(t.Decl); ok {
		b.WriteString("@")
		b.WriteString(pkg)
	}
	for _, e := range t.Elems {
		writeDeclPkgs(b, e)
	}
	if t.Sig != nil {
		for _, p := range t.Sig.Params {
			writeDeclPkgs(b, p.Type)
		}
		writeDeclPkgs(b, t.Sig.Return)
	}
}

// declPkgURI is the package a named declaration records, empty for a program's
// own -- the same blind spot ir.sameDecl's name fallback has.
func declPkgURI(sym ir.Symbol) (string, bool) {
	switch d := sym.(type) {
	case *ir.StructDef:
		return d.Pkg, true
	case *ir.EnumDef:
		return d.Pkg, true
	case *ir.UnitDef:
		return d.Pkg, true
	case *ir.Component:
		return d.Pkg, true
	}
	return "", false
}

// anonStructName names the declaration in generated code. It must be one
// identifier in Go, Kotlin and JS at once, which leaves only letters, digits
// and underscore to build it from.
func anonStructName(canon []*ir.StructField, sig string) string {
	var b strings.Builder
	b.WriteString(anonStructPrefix)
	for _, f := range canon {
		b.WriteString(f.Name)
		b.WriteString("_")
	}
	sum := sha256.Sum256([]byte(sig))
	b.WriteString(hex.EncodeToString(sum[:])[:6])
	return b.String()
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
	sig := anonSignature(canon)
	pkg := c.declPkg()
	if c.anonStructs == nil {
		c.anonStructs = map[*ir.Package]map[string]*ir.StructDef{}
	}
	if c.anonStructs[pkg] == nil {
		c.anonStructs[pkg] = map[string]*ir.StructDef{}
	}
	if sd, ok := c.anonStructs[pkg][sig]; ok {
		return sd
	}
	sd := &ir.StructDef{
		Name:   anonStructName(canon, sig),
		Pkg:    c.libPkgName,
		Fields: canon,
		Anon:   true,
	}
	c.anonStructs[pkg][sig] = sd
	pkg.Structs = append(pkg.Structs, sd)
	return sd
}

// anonFieldsFromInits types an anonymous struct literal by its own values.
// Giving up leaves it the decl-less struct it was. A spread names no field
// set; an unbound type parameter has no one type, and interning it would give
// every instantiation one declaration whose field is `T`.
func (c *checker) anonFieldsFromInits(inits []ir.FieldInit) ([]*ir.StructField, bool) {
	fields := make([]*ir.StructField, 0, len(inits))
	seen := map[string]bool{}
	for _, fi := range inits {
		if fi.Spread || fi.Name == "" {
			return nil, false
		}
		if seen[fi.Name] {
			c.error(fi.NamePos, "duplicate field %q in struct literal", fi.Name)
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
