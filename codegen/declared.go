package codegen

import (
	"fmt"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"
)

// A target is its package: a lib/ directory -- `lib/platform/<name>`,
// `lib/language/<name>` -- registered here by the layout, as a library scheme
// plugin is. Go behind a target registers by key (RegisterNative) and the
// node names it with #[gen.native]; a target naming none generates nothing,
// and what it is built for is answered by the compiler, which for `none` is
// the interpreter.
func init() {
	for _, pkg := range lib.Packages() {
		if name, ok := strings.CutPrefix(pkg, "platform/"); ok && !strings.Contains(name, "/") {
			registerPlatform(&DeclaredPlatform{name: name})
		}
		if name, ok := strings.CutPrefix(pkg, "language/"); ok && !strings.Contains(name, "/") {
			registerLang(&DeclaredLang{name: name})
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

var (
	nativeMu sync.RWMutex
	natives  = map[string]any{}
)

// RegisterNative makes g the generator a build-target node names with
// #[gen.native(key)]: a PlatformGenerator or a LangTranslator. Called from
// the plugin's init. The target itself is its package in lib/, which is why
// nothing here names one.
func RegisterNative(key string, g any) {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	if _, ok := natives[key]; ok {
		panic("codegen: duplicate native generator: " + key)
	}
	natives[key] = g
}

// nodeNative is the #[gen.native] key the build-target node of the lib
// package uri names, or "". Read off the source rather than the checked
// package: a check asks whether a target is available (Unavailable), and
// checking the target's package to answer would re-enter the check -- and a
// package whose import fails is exactly the one being asked about. The mark is
// placed on a build-target node and held there by the checker, so the first
// one written is the node's.
func nodeNative(uri string) string { return nativeIn(checker.PackageSource(uri)) }

// nativeIn is the first #[gen.native] key the documents write, resolving
// sngl:x/gen's alias per file as the checker does: the written one, the
// path's last segment when none is written, no qualifier under a dot import,
// and no mark at all in a file that does not import the package.
func nativeIn(docs []*ast.Document) string {
	for _, doc := range docs {
		alias, imported := "", false
		for _, st := range doc.Stmts {
			if imp, ok := st.(*ast.Import); ok && imp.Path == "sngl:x/gen" {
				imported = true
				switch imp.Alias {
				case "":
					alias = "gen"
				case ".":
					alias = ""
				default:
					alias = imp.Alias
				}
			}
		}
		if !imported {
			continue
		}
		for _, st := range doc.Stmts {
			decl, ok := st.(*ast.ComponentDecl)
			if !ok {
				continue
			}
			for _, a := range decl.Attrs {
				if a.Name != "native" || a.Alias != alias || len(a.Args) != 1 {
					continue
				}
				if lit, ok := a.Args[0].(*ast.LiteralExpr); ok {
					if v, ok := lit.StringValue(); ok {
						return v
					}
				}
			}
		}
	}
	return ""
}

// ResolveNative is the generator registered under key for the target whose
// package is uri, or a declared target that refuses to generate, naming the
// key, when this binary has none.
func ResolveNative(uri, key string) any {
	nativeMu.RLock()
	g := natives[key]
	nativeMu.RUnlock()
	switch tier, name, _ := strings.Cut(uri, "/"); tier {
	case "platform":
		if p, ok := g.(PlatformGenerator); ok {
			return p
		}
		return &DeclaredPlatform{name: name, missing: key}
	default:
		if l, ok := g.(LangTranslator); ok {
			return l
		}
		return &DeclaredLang{name: name, missing: key}
	}
}

// DeclaredPlatform is a platform whose package is in lib/. Looked up, it is
// the Go generator its node names, and itself -- generating nothing -- when
// the node names none.
type DeclaredPlatform struct {
	name string
	desc declaredDesc
	// missing is the generator the node names that this binary lacks.
	missing string

	once     sync.Once
	resolved PlatformGenerator
}

func (p *DeclaredPlatform) resolve() PlatformGenerator {
	p.once.Do(func() {
		p.resolved = p
		if p.missing != "" {
			return
		}
		if key := nodeNative("platform/" + p.name); key != "" {
			p.resolved = ResolveNative("platform/"+p.name, key).(PlatformGenerator)
		}
	})
	return p.resolved
}

func (p *DeclaredPlatform) PlatformIdentifier() string { return p.name }
func (p *DeclaredPlatform) Description() string {
	return p.desc.get("platform/" + p.name)
}
func (p *DeclaredPlatform) SupportedLangs() []string { return nil }

// Unavailable is the generator's answer, where the node names one that can
// say: the checker asks it of every registered platform to decide whether to
// load the platform's package at all. A generator this binary was built
// without is unavailable too -- a test binary linking some targets loads
// none of the others' overrides, as it did when each target was registered
// by its Go.
func (p *DeclaredPlatform) Unavailable() error {
	if p.missing != "" {
		return p.missingErr()
	}
	switch r := p.resolve().(type) {
	case *DeclaredPlatform:
		if r != p {
			return r.Unavailable()
		}
	case PlatformAvailability:
		return r.Unavailable()
	}
	return nil
}

func (p *DeclaredPlatform) missingErr() error {
	return fmt.Errorf("platform %q is generated by %q, which this sngl was built without", p.name, p.missing)
}

func (p *DeclaredPlatform) Generate(*Request, Sink) error {
	if p.missing != "" {
		return p.missingErr()
	}
	return fmt.Errorf("platform %q generates no code", p.name)
}

// GeneratesNothing reports whether p is a platform with no generator behind
// it -- `none` -- rather than one whose generator this binary lacks.
func GeneratesNothing(p PlatformGenerator) bool {
	d, ok := p.(*DeclaredPlatform)
	return ok && d.missing == ""
}

// DeclaredLang is a language whose package is in lib/, resolved as a
// DeclaredPlatform is. One with no translator -- `none` -- is handed to html's
// static mode as the language it does not translate into, so each method
// answers as if nothing were translated.
type DeclaredLang struct {
	name    string
	desc    declaredDesc
	missing string

	once     sync.Once
	resolved LangTranslator
}

func (l *DeclaredLang) resolve() LangTranslator {
	l.once.Do(func() {
		l.resolved = l
		if l.missing != "" {
			return
		}
		if key := nodeNative("language/" + l.name); key != "" {
			l.resolved = ResolveNative("language/"+l.name, key).(LangTranslator)
		}
	})
	return l.resolved
}

// Translates reports whether a language is translated into: it is not a
// declared language with no translator behind it.
func Translates(lang string) bool {
	d, ok := LookupLang(lang).(*DeclaredLang)
	return lang != "" && !(ok && d.missing == "")
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
