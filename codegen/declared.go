package codegen

import (
	"fmt"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"
)

// A target is its package. One served by a Go plugin registers itself; one
// whose package is a lib/ directory -- `lib/platform/<name>`,
// `lib/language/<name>` -- needs no Go at all, and is registered here by the
// layout, as a library scheme plugin is. Such a target generates nothing:
// what it is built for is answered by the compiler, which for `none` is the
// interpreter (build.IsInterpreted).
//
// A Go plugin registering a name lib/ already serves is the duplicate it
// would be against another plugin, since a target has one package.
func init() {
	for _, pkg := range lib.Packages() {
		if name, ok := strings.CutPrefix(pkg, "platform/"); ok && !strings.Contains(name, "/") {
			RegisterPlatform(&DeclaredPlatform{name: name})
		}
		if name, ok := strings.CutPrefix(pkg, "language/"); ok && !strings.Contains(name, "/") {
			RegisterLang(&DeclaredLang{name: name})
		}
	}
	// The interpreter answers navigation itself (internal/interp/nav.go):
	// `go` and `back` on a stack, and the `follow` sngl:platform/none's link
	// override calls. Each is a statement about a tree the interpreter holds,
	// not an expression an emitter could render, which is what declaring the
	// package rather than an emitter per id is for. The interpreter is the
	// compiler's, so the claim is too.
	DeclarePlatformImplements(interp.InterpreterPlatform, "sngl:ui/nav")
	DeclarePlatformImplements(interp.InterpreterPlatform, "sngl:platform/"+interp.InterpreterPlatform)
}

// DeclaredPlatform is a platform whose package is in lib/ and which has no
// generator.
type DeclaredPlatform struct {
	name string
	desc declaredDesc
}

func (p *DeclaredPlatform) PlatformIdentifier() string { return p.name }
func (p *DeclaredPlatform) Description() string {
	return p.desc.get("platform/" + p.name)
}
func (p *DeclaredPlatform) SupportedLangs() []string { return nil }

func (p *DeclaredPlatform) Generate(*Request, Sink) error {
	return fmt.Errorf("platform %q generates no code", p.name)
}

// DeclaredLang is a language whose package is in lib/ and which has no
// translator. `none` is the one, and html's static mode is handed it as the
// language it does not translate into, so each method answers as if nothing
// were translated.
type DeclaredLang struct {
	name string
	desc declaredDesc
}

func (l *DeclaredLang) LanguageIdentifier() string { return l.name }
func (l *DeclaredLang) Description() string {
	return l.desc.get("language/" + l.name)
}
func (l *DeclaredLang) GenerateIdentifier(name *ir.Ident) string { return name.Name }
func (l *DeclaredLang) TranslateIRLiteral(ir.Expr) string        { return "" }
func (l *DeclaredLang) ExportName(name string) string            { return name }
func (l *DeclaredLang) NewFileEmitter(Sink, FileOptions) FileEmitter {
	return &UnimplementedFileEmitter{Lang: l.name}
}

// declaredDesc is a declared target's one-line description: the first
// sentence of its package comment, read on first use, since registration runs
// before anything is parsed.
type declaredDesc struct {
	once sync.Once
	text string
}

func (d *declaredDesc) get(uri string) string {
	d.once.Do(func() {
		for _, doc := range checker.PackageSource(uri) {
			if s := checker.PackageDoc(doc); s != "" {
				d.text, _, _ = strings.Cut(s, ". ")
				d.text = strings.TrimSuffix(d.text, ".") + "."
				return
			}
		}
	})
	return d.text
}
