package golang

import (
	"duckfam.us/sngl/ir"
)

// BaseImports is the Go imports a generated file needs that no emitted
// expression registers where it is written: the sngl-i18n runtime, and the
// package of every `#[go.native]` type a value is declared as.
//
// A source import is not among them. A native call registers its own
// package through AliasFor as it is spelled, so an import reaches the file
// exactly where something names it -- and a `file:` import's path is a
// directory rather than a Go package, and a `go:` import whose every call
// folded at build time is named by nothing, which Go refuses as unused.
func BaseImports(pkg *ir.Package) []string {
	if pkg == nil {
		return nil
	}
	var out []string
	if PackageUsesI18n(pkg) {
		out = append(out, SnglI18nImportPath)
	}
	// A `#[go.native]` *type* is spelled by IRTypeToGo, which is a free
	// function with no context to register an import from -- and unlike a
	// native call, a type may be the only mention its package gets: a var of
	// one becomes a field, a getter and a setter with nothing calling the
	// constructor beside them, which is what fyne's `*time.Timer` schedule is.
	// Collected here so all three Go platforms get it from the one place
	// each already asks.
	out = append(out, foreignTypeImports(pkg)...)
	return out
}

// foreignTypeImports is the import path of every `#[go.native]` type a value in
// pkg is declared as.
//
// Read off the *values* rather than off pkg.Structs, because a platform
// package's declaration is not in a program's struct list at all -- fyne's
// `Schedule` reaches a program only as the type of the `handle` var its timer
// override declares.
//
// The values are an owner's vars and its funcs' signatures, which is what a
// platform override writes today. Three shapes reach the file without their
// import, and IRTypeToGo spells the name anyway -- generated Go that does not
// compile. Nothing produces any of them yet, so this is the shape of the gap
// rather than a live bug:
//
//   - a local of such a type inside a func body, and a const of one, which an
//     ir.LocalVar arm is what adding costs;
//   - a type reached only *through a function type*, `func(Entry)` over a
//     `#[go.native]` struct: the walk below recurses `t.Elems`, and a signature
//     is not there -- ir.Type keeps it in `Sig`. A direct native param or
//     return is covered, because those are read off the func's own signature
//     rather than out of a type; only a func type written as a value's type
//     escapes. A `case TypeFunc` reaching into Sig is what adding costs.
func foreignTypeImports(pkg *ir.Package) []string {
	var out []string
	seen := map[string]bool{}
	// Through the type arguments and through a struct's fields: a
	// `list<Schedule>` spells the element in the field it becomes, and a
	// program struct holding a native-typed field spells that type in the Go
	// struct the backend emits -- with nothing else in the file naming the
	// package, a field being no call.
	walked := map[*ir.StructDef]bool{}
	var add func(t *ir.Type)
	add = func(t *ir.Type) {
		if t == nil {
			return
		}
		for _, e := range t.Elems {
			add(e)
		}
		sd, ok := t.Decl.(*ir.StructDef)
		if !ok {
			return
		}
		if sd.Foreign.Name == "" || sd.Foreign.Marked {
			// The program's own struct: its fields are emitted with it. The
			// guard is for a struct that reaches itself through a list.
			if !walked[sd] {
				walked[sd] = true
				for _, f := range sd.Fields {
					if f != nil {
						add(f.Type)
					}
				}
			}
			return
		}
		path := sd.Foreign.Path
		if path == "" || path == "C" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, path)
	}
	for _, o := range ir.Owners(pkg) {
		for _, v := range o.Vars {
			if v != nil {
				add(v.Type)
			}
		}
		for _, fn := range o.Funcs {
			if fn == nil {
				continue
			}
			add(fn.Return)
			for _, pm := range fn.Params {
				if pm != nil {
					add(pm.Type)
				}
			}
		}
	}
	return out
}
