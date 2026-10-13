package ir

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// AnonStructPrefix begins every name the compiler gives an anonymous struct.
const AnonStructPrefix = "__anon_"

// AnonStructBaseName is the name an anonymous struct with these fields, in
// name order, is declared under in generated code, before a counter tells two
// apart that spell alike. It must be one identifier in Go, Kotlin and JS at
// once, which leaves only letters, digits and underscore to build it from,
// and it sees only what is stable across runs: the checker names what it
// interns with it, and the lowering what a substitution made concrete, and the
// two must agree.
func AnonStructBaseName(canon []*StructField) string {
	sigs := make([]string, len(canon))
	var b strings.Builder
	b.WriteString(AnonStructPrefix)
	for i, f := range canon {
		sigs[i] = f.Name + " " + AnonTypeKey(f.Type, false)
		b.WriteString(f.Name)
		b.WriteString("_")
	}
	sum := sha256.Sum256([]byte(strings.Join(sigs, "; ")))
	b.WriteString(hex.EncodeToString(sum[:])[:6])
	return b.String()
}

// AnonTypeKey spells a type for an anonymous struct's signature. String()
// alone would not: it prints a named type unqualified, so a program's own
// `Style` and `sngl:ui`'s read alike.
func AnonTypeKey(t *Type, exact bool) string {
	var b strings.Builder
	b.WriteString(t.String())
	writeDeclIDs(&b, t, exact)
	return b.String()
}

func writeDeclIDs(b *strings.Builder, t *Type, exact bool) {
	if t == nil {
		return
	}
	// An anonymous struct is its fields: keyed by its address, the
	// `struct { v int }` a substitution made and the one `{v=1}` interned
	// would key two declarations of one type. Its fields' own declarations
	// are what tell it apart, and String() has already spelled the rest.
	if sd := AnonStructDef(t); sd != nil && exact {
		b.WriteString("@{")
		for _, f := range sd.Fields {
			writeDeclIDs(b, f.Type, exact)
			b.WriteString(";")
		}
		b.WriteString("}")
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
// that records no package answers with its address instead -- the same thing
// sameDecl identifies those by, and not their name, which two packages of one
// program may both use. Never stable across runs, so only the checker's intern
// map may see it; see #175 before making a name enough.
func declID(sym Symbol, exact bool) (string, bool) {
	var pkg string
	switch d := sym.(type) {
	case *StructDef:
		pkg = d.PkgPath()
	case *EnumDef:
		pkg = d.PkgPath()
	case *UnitDef:
		pkg = d.PkgPath()
	case *Component:
		pkg = d.PkgPath()
	default:
		return "", false
	}
	if pkg == "" && exact {
		return fmt.Sprintf("%p", sym), true
	}
	return pkg, true
}
