package docs

import (
	"sort"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/ir"

	// Blank-import the aggregator packages so Targets() observes every
	// registered platform and language, not just the subset the rest of
	// the docs package pulls in for preview rendering.
	_ "duckfam.us/sngl/codegen/lang"
	_ "duckfam.us/sngl/codegen/platform"
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

type PlatformTarget struct {
	Name         string
	Doc          string
	Languages    []string
	Capabilities []Capability
	Options      []OptionDoc
}

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
	// PlatformMatrix and LanguageMatrix are the capability tables as a table
	// takes them: a name column, then one per capability, "—" where a target
	// has none.
	PlatformMatrix CapabilityMatrix
	LanguageMatrix CapabilityMatrix
}

// CapabilityMatrix is a table of targets by capability.
type CapabilityMatrix struct {
	Columns []string
	Rows    [][]string
}

func capabilityMatrix(first string, columns []string, names []string, caps [][]Capability) CapabilityMatrix {
	m := CapabilityMatrix{Columns: append([]string{first}, columns...)}
	for i, name := range names {
		row := []string{name}
		for _, c := range caps[i] {
			v := c.Value
			if v == "" {
				v = "—"
			}
			row = append(row, v)
		}
		m.Rows = append(m.Rows, row)
	}
	return m
}

// Probes every built-in target for the optional codegen interfaces it
// implements. Called at compile time from website.sngl.
//
//sngl:pure
func Targets() TargetCatalog {
	cat := TargetCatalog{
		PlatformCapabilities: platformCapNames(),
		LanguageCapabilities: languageCapNames(),
		GlobalOptions:        sharedOptions(),
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
			Options:      optionsForPackage("platform/" + name),
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
			Options:      optionsForPackage("language/" + name),
		})
	}

	var names []string
	var caps [][]Capability
	for _, p := range cat.Platforms {
		names, caps = append(names, p.Name), append(caps, p.Capabilities)
	}
	cat.PlatformMatrix = capabilityMatrix("Platform", cat.PlatformCapabilities, names, caps)
	names, caps = nil, nil
	for _, l := range cat.Languages {
		names, caps = append(names, l.Name), append(caps, l.Capabilities)
	}
	cat.LanguageMatrix = capabilityMatrix("Language", cat.LanguageCapabilities, names, caps)
	return cat
}

type platformProbe struct {
	name string
	doc  string
	eval func(codegen.PlatformGenerator) string
}

// hasCommand is whether the platform, built with its first language, holds a
// command of the given kind: its own node's, or its language's.
func hasCommand(p codegen.PlatformGenerator, kind ir.BuiltinKind) bool {
	langs := p.SupportedLangs()
	if len(langs) == 0 {
		return false
	}
	return build.HasCommand(build.Target{Platform: p.PlatformIdentifier(), Lang: langs[0]}, kind)
}

func check(ok bool) string {
	if ok {
		return "✓"
	}
	return ""
}

var platformProbes = []platformProbe{
	{"Run", "Execute generated output in place (sngl run).",
		func(p codegen.PlatformGenerator) string { return check(hasCommand(p, ir.BuiltinGenRun)) }},
	{"Build", "Produce a distributable artifact (e.g. APK).",
		func(p codegen.PlatformGenerator) string { return check(hasCommand(p, ir.BuiltinGenBuild)) }},
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

// optionsForPackage is the build options a target accepts: the props of the
// build-directive node its package declares. Nil when the package declares no
// node -- which is what a target that is not registered here looks like.
func optionsForPackage(uri string) []OptionDoc {
	return optionsForNode(checker.TargetNode(uri))
}

// sharedOptions are the props on `output` itself: the ones every target
// accepts, whatever it is.
func sharedOptions() []OptionDoc {
	return optionsForNode(checker.OutputNode())
}

func optionsForNode(comp *ir.Component) []OptionDoc {
	if comp == nil || comp.AST == nil {
		return nil
	}
	return optionDocs(comp.AST)
}

// optionDocs reads a node's declared props, in declaration order. The doc is
// the run of comments written above the prop, which the parser hangs off the
// parameter itself -- a parameter is not a statement, so there is no statement
// list a comment could otherwise live in.
func optionDocs(decl *ast.ComponentDecl) []OptionDoc {
	var out []OptionDoc
	for _, item := range decl.Props.Props {
		prop, ok := item.(ast.Param)
		if !ok {
			continue
		}
		var lines []string
		for _, c := range prop.Leading {
			if c.Block {
				continue
			}
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(c.Text, "//"), " "))
		}
		doc := joinDocLines(lines)
		if doc == "" && prop.Trailing != nil && !prop.Trailing.Block {
			doc = strings.TrimPrefix(strings.TrimPrefix(prop.Trailing.Text, "//"), " ")
		}
		out = append(out, OptionDoc{Name: prop.Name, Type: formatType(prop.Type), Doc: doc})
	}
	return out
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

// Covers the named-type case Options structs use; anything else falls back to
// a generic "type" label.
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
