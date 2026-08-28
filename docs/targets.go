package docs

import (
	"slices"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"

	// Blank-import the aggregator packages so Targets() observes every
	// registered platform and language, not just the subset the rest of
	// the docs package pulls in for preview rendering.
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

// Capability names a single optional codegen interface and reports what the
// target provides. Value is empty when unsupported, "✓" for a plain yes/no
// capability, or a short label (e.g. "Mutation" / "Render") when the cell
// carries variant info.
type Capability struct {
	Name  string
	Doc   string
	Value string
}

// OptionDoc describes one recognized entry in Request.Options.
type OptionDoc struct {
	Name string
	Type string
	Doc  string
}

// PlatformTarget describes a registered platform generator for the docs site.
type PlatformTarget struct {
	Name         string
	Doc          string
	Languages    []string
	Capabilities []Capability
	Options      []OptionDoc
}

// LanguageTarget describes a registered language translator for the docs site.
type LanguageTarget struct {
	Name         string
	Doc          string
	Capabilities []Capability
	Options      []OptionDoc
}

// TargetCatalog is the top-level view model for the /targets page.
type TargetCatalog struct {
	// PlatformCapabilities and LanguageCapabilities list the columns in the
	// same order each row's Capabilities slice uses. Handy for the template
	// to emit matrix headers without peeking into a row.
	PlatformCapabilities []string
	LanguageCapabilities []string
	// GlobalOptions are options declared by the stdlib's Options struct that
	// every target accepts (e.g. name, icon, description, version).
	GlobalOptions []OptionDoc
	Platforms     []PlatformTarget
	Languages     []LanguageTarget
}

// Targets enumerates built-in platforms and languages, probing each for the
// optional codegen interfaces it implements. Called at compile time from
// website.sngl to render the /targets reference page.
//
//sngl:pure
func Targets() TargetCatalog {
	cat := TargetCatalog{
		PlatformCapabilities: platformCapNames(),
		LanguageCapabilities: languageCapNames(),
		GlobalOptions:        optionsForPackage("std"),
	}

	plats := codegen.Platforms()
	sort.Strings(plats)
	for _, name := range plats {
		p := codegen.LookupPlatform(name)
		if p == nil {
			continue
		}
		langs := append([]string(nil), p.SupportedLangs()...)
		sort.Strings(langs)
		cat.Platforms = append(cat.Platforms, PlatformTarget{
			Name:         name,
			Doc:          p.Description(),
			Languages:    langs,
			Capabilities: probePlatform(p),
			Options:      optionsForPackage("platforms/" + name),
		})
	}

	langs := codegen.Langs()
	sort.Strings(langs)
	for _, name := range langs {
		l := codegen.LookupLang(name)
		if l == nil {
			continue
		}
		cat.Languages = append(cat.Languages, LanguageTarget{
			Name:         name,
			Doc:          l.Description(),
			Capabilities: probeLanguage(l),
			Options:      optionsForPackage("languages/" + name),
		})
	}

	return cat
}

// --- capability probes ---

type platformProbe struct {
	name string
	doc  string
	eval func(codegen.PlatformGenerator) string
}

// check returns "✓" when ok, "" otherwise — the boolean probe shortcut.
func check(ok bool) string {
	if ok {
		return "✓"
	}
	return ""
}

var platformProbes = []platformProbe{
	{"Run", "Execute generated output in place (sngl run).",
		func(p codegen.PlatformGenerator) string { _, ok := p.(codegen.Runner); return check(ok) }},
	{"Build", "Produce a distributable artifact (e.g. APK).",
		func(p codegen.PlatformGenerator) string { _, ok := p.(codegen.Builder); return check(ok) }},
	{"Snapshot", "Capture a screenshot of the rendered output (PNG or ANSI text).",
		func(p codegen.PlatformGenerator) string {
			switch p.(type) {
			case codegen.Snapshotter, codegen.BatchSnapshotter,
				codegen.TextSnapshotter, codegen.BatchTextSnapshotter:
				return "✓"
			}
			return ""
		}},
	{"Test Runner", "Execute tests with a platform-specific harness.",
		func(p codegen.PlatformGenerator) string { _, ok := p.(codegen.TestRunner); return check(ok) }},
	{"Preview CSS", "Provide CSS to style the HTML preview for this target.",
		func(p codegen.PlatformGenerator) string { _, ok := p.(codegen.PreviewStyler); return check(ok) }},
	{"Model", "Rendering strategy: Mutation emits a static tree plus targeted updaters; Render re-renders the full view from state.",
		func(p codegen.PlatformGenerator) string {
			if _, ok := p.(codegen.MutationCompilerFactory); ok {
				return "Mutation"
			}
			if _, ok := p.(codegen.RenderCompilerFactory); ok {
				return "Render"
			}
			return ""
		}},
}

type languageProbe struct {
	name string
	doc  string
	eval func(codegen.LangTranslator) string
}

var languageProbes = []languageProbe{
	{"HTTP Compiler", "Generates HTTP server code for html route mode.",
		func(l codegen.LangTranslator) string { _, ok := l.(codegen.HTTPCompiler); return check(ok) }},
	{"WASM Compiler", "Compiles packages to WebAssembly with JS bindings.",
		func(l codegen.LangTranslator) string { _, ok := l.(codegen.WASMCompiler); return check(ok) }},
}

func probePlatform(p codegen.PlatformGenerator) []Capability {
	caps := make([]Capability, len(platformProbes))
	for i, pr := range platformProbes {
		caps[i] = Capability{Name: pr.name, Doc: pr.doc, Value: pr.eval(p)}
	}
	return caps
}

func probeLanguage(l codegen.LangTranslator) []Capability {
	caps := make([]Capability, len(languageProbes))
	for i, pr := range languageProbes {
		caps[i] = Capability{Name: pr.name, Doc: pr.doc, Value: pr.eval(l)}
	}
	return caps
}

func platformCapNames() []string {
	out := make([]string, len(platformProbes))
	for i, p := range platformProbes {
		out[i] = p.name
	}
	return out
}

func languageCapNames() []string {
	out := make([]string, len(languageProbes))
	for i, p := range languageProbes {
		out[i] = p.name
	}
	return out
}

// optionsForPackage extracts the fields of sngl://<uri>'s #[options] struct.
// Each field's type is formatted as source and its doc string is pulled from
// the nearest comments: preceding line comments immediately above the field,
// or a trailing comment on the same line. Returns nil if the package declares
// no options schema.
//
// The mark is on the loaded IR, so the declaration is found there and the
// parsed source is then read for the comments the IR does not carry.
func optionsForPackage(uri string) []OptionDoc {
	sd := checker.OptionsStruct(uri)
	if sd == nil || sd.AST == nil {
		return nil
	}
	for _, doc := range checker.PackageSource(uri) {
		if doc == nil {
			continue
		}
		if slices.Contains(doc.Stmts, ast.Stmt(sd.AST)) {
			return extractOptionsStruct(doc, sd.AST)
		}
	}
	return nil
}

func extractOptionsStruct(doc *ast.Document, target *ast.StructDef) []OptionDoc {

	// Collect comments by line. The parser emits comments inside a struct body
	// as sibling Comment statements in the document (it doesn't attach them to
	// fields), so we scan every Comment in the doc and look up by line.
	commentByLine := map[int]string{}
	for _, s := range doc.Stmts {
		if c, ok := s.(*ast.Comment); ok && !c.Block {
			text := c.Text
			if len(text) >= 2 && text[:2] == "//" {
				text = text[2:]
			}
			if len(text) > 0 && text[0] == ' ' {
				text = text[1:]
			}
			commentByLine[c.Pos.Line] = text
		}
	}

	var out []OptionDoc
	for _, f := range target.Fields() {
		for _, name := range f.Names {
			out = append(out, OptionDoc{
				Name: name,
				Type: formatType(f.Type),
				Doc:  fieldDocString(f.Pos.Line, commentByLine),
			})
		}
	}
	return out
}

// fieldDocString returns the documentation for a field declared on line. It
// prefers a run of preceding comment lines immediately above; falls back to
// an inline comment on the same line.
func fieldDocString(line int, commentByLine map[int]string) string {
	var preceding []string
	for l := line - 1; l > 0; l-- {
		c, ok := commentByLine[l]
		if !ok {
			break
		}
		preceding = append([]string{c}, preceding...)
	}
	if len(preceding) > 0 {
		return joinDocLines(preceding)
	}
	if c, ok := commentByLine[line]; ok {
		return c
	}
	return ""
}

func joinDocLines(lines []string) string {
	var out strings.Builder
	for i, l := range lines {
		if i > 0 {
			out.WriteString(" ")
		}
		out.WriteString(l)
	}
	return out.String()
}

// formatType renders an ast.TypeExpr to a short human-readable string. Covers
// the named-type case used in Options structs; non-trivial shapes fall back
// to a generic "type" label.
func formatType(t ast.TypeExpr) string {
	if t == nil {
		return ""
	}
	if nt, ok := t.(*ast.NamedType); ok {
		if nt.Package != "" {
			return nt.Package + "." + nt.Name
		}
		return nt.Name
	}
	return "type"
}
