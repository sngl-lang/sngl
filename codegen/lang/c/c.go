// Package c is the SNGL language translator for C.
//
// The C it writes compiles as both C11 and C17 and includes pkg/c's
// sngl.h, which every program ships beside it. A program's own declarations
// are named with a prefix, `s_`, since a C program shares one namespace with
// every header it includes, and GTK's or Win32's are large. What a program
// names inside a function -- a local, a parameter, a field -- keeps its name:
// passReservedNames renames one that is a C keyword.
package c

import (
	"strings"
	"sync"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

func init() {
	codegen.RegisterNative(langC, &Translator{})
}

// langC is this translator's registry key.
const langC = "c"

// Translator implements codegen.LangTranslator for C.
type Translator struct{}

var _ codegen.LangTranslator = (*Translator)(nil)

func (t *Translator) LanguageIdentifier() string { return langC }
func (t *Translator) Description() string {
	return "Generate C11 source (used with the gtk4 platform)."
}

func (t *Translator) GenerateIdentifier(name *ir.Ident) string { return Local(name.Name) }

func (t *Translator) TranslateIRLiteral(e ir.Expr) string {
	if lit, ok := e.(*ir.Literal); ok {
		return literal(lit)
	}
	return "0"
}

// ExportName is a program's top-level declaration as C names it.
func (t *Translator) ExportName(name string) string { return Global(name) }

func (t *Translator) NewFileEmitter(sink codegen.Sink, opts codegen.FileOptions) codegen.FileEmitter {
	return newFileEmitter(sink, opts)
}

// Global is name, a declaration at a program's top level, as C names it.
func Global(name string) string { return "s_" + mangle(name) }

// Decl is d, a declaration at a program's or a library's top level, as C
// names it: a library's carries its package, since two packages may each
// declare a name and C has one namespace for both -- sngl:x/c's `free`
// beside a program's own.
func Decl(d *ir.DeclInfo) string {
	if path := d.PkgPath(); path != "" {
		return "s_" + mangle(strings.TrimPrefix(path, "sngl:")) + "__" + mangle(d.Name)
	}
	return Global(d.Name)
}

// Method is the function a method of the type named recv is.
func Method(recv, name string) string { return "s_" + mangle(recv) + "_" + mangle(name) }

// Local is name, a local, a parameter or a field, as C names it: itself,
// unless it is a C keyword, which a field keeps (passReservedNames renames
// only what it may) and C refuses.
func Local(name string) string {
	name = mangle(name)
	if keywords()[name] {
		return name + "_"
	}
	return name
}

// mangle makes name a C identifier: SNGL's names are, but for the `.` and
// `$` a pass may build one of.
func mangle(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '_'
	}, name)
}

var keywords = sync.OnceValue(func() map[string]bool {
	set := map[string]bool{}
	for _, w := range codegen.LanguageReserves(langC) {
		set[w] = true
	}
	return set
})
