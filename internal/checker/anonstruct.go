package checker

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// anonStructPrefix is the namespace the interned declarations are named in.
// claimTopLevel refuses it to a program, which is what makes a synthesized
// name unable to collide with a declared one; the compiler's other synthesized
// names (`__cse0`, `__merge_`, `__ran0`) share the double underscore.
const anonStructPrefix = "__anon_"

// anonSignature is the canonical spelling of a set of fields: name and type,
// sorted by name. Two anonymous structs are the same type when they agree on
// it, so field order is not part of a type's identity.
func anonSignature(fields []*ir.StructField) string {
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == nil {
			continue
		}
		parts = append(parts, f.Name+" "+f.Type.String())
	}
	slices.Sort(parts)
	return strings.Join(parts, "; ")
}

// anonStructName is the declaration's name in generated code: the field names
// for a reader, and six hex digits of the signature so that two structs whose
// fields are named alike but typed differently stay apart. It has to be one
// identifier in Go, Kotlin and JS at once, which leaves nothing but letters,
// digits and underscore to build it from.
func anonStructName(fields []*ir.StructField, sig string) string {
	var b strings.Builder
	b.WriteString(anonStructPrefix)
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != nil {
			names = append(names, f.Name)
		}
	}
	slices.Sort(names)
	for _, n := range names {
		b.WriteString(n)
		b.WriteString("_")
	}
	sum := sha256.Sum256([]byte(sig))
	b.WriteString(hex.EncodeToString(sum[:])[:6])
	return b.String()
}

// internAnonStruct returns the one *ir.StructDef this package uses for an
// anonymous struct with these fields, synthesizing and registering it the
// first time the signature is seen.
//
// Interning is what keeps `ir.TypeStruct` meaning "a struct type names a
// declaration": an anonymous struct used to carry a nil Decl, so it had no
// fields to select from, nothing to compare two values by, and no name a
// backend could declare. Registering the result in pkg.Structs is the other
// half — after the checker there is no such thing as an anonymous struct, only
// an ordinary named one, so no backend grows a case for it.
//
// Identity is per package, like every other declaration's: the decl is
// registered in the package being checked, and a second package interning the
// same signature gets its own so that its own codegen emits it.
func (c *checker) internAnonStruct(pos ast.Pos, fields []*ir.StructField) *ir.StructDef {
	canon := slices.Clone(fields)
	slices.SortStableFunc(canon, func(a, b *ir.StructField) int {
		return strings.Compare(a.Name, b.Name)
	})
	sig := anonSignature(canon)
	if c.anonStructs == nil {
		c.anonStructs = map[string]*ir.StructDef{}
	}
	if sd, ok := c.anonStructs[sig]; ok {
		return sd
	}
	sd := &ir.StructDef{
		Name:   anonStructName(canon, sig),
		Pkg:    c.libPkgName,
		Fields: canon,
		Anon:   true,
	}
	c.anonStructs[sig] = sd
	c.declPkg().Structs = append(c.declPkg().Structs, sd)
	return sd
}

// anonFieldsFromInits types an anonymous struct literal by its own values: one
// field per initializer, in written order, which internAnonStruct then sorts.
//
// It reports nothing and gives up instead, leaving the literal the decl-less
// struct it was before interning existed. A spread is the case that matters:
// `style={...style}` in a platform override is written where the prop's own
// declaration supplies the field set, and the field set is exactly what a
// spread does not name. Whatever the value the checker has for a name or a
// type there, none of it is this function's to complain about — a bad one is
// already reported where it was checked.
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
