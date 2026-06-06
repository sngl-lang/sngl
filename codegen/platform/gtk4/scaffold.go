package gtk4

import "embed"

// Config controls GTK4 code generation. Fields are populated via --opt flags.
type Config struct {
	Package string `option:"package"` // default: "main"
	Main    bool   `option:"main"`    // default: true — emit main() entrypoint
	GIRPath string `option:"gir"`     // default: "" — autodetect from standard paths

	// Lang globals (codegen/lang/golang/golang.sngl).
	GoVersion  string `option:"goVersion"`  // Go toolchain version for go.mod (default: "1.23")
	GoModExtra string `option:"goModExtra"` // Extra text appended to temp test-module go.mod (e.g. replace directive)

	// Internal (set by CLI, not exposed in .sngl).
	Lang string `option:"lang"` // language identifier used to select the executor
}

func (c Config) withDefaults() Config {
	if c.Package == "" {
		c.Package = "main"
	}
	if c.GoVersion == "" {
		c.GoVersion = "1.23"
	}
	return c
}

//go:embed templates/*
var templateFS embed.FS

// templateData is passed to model.go.tmpl and callbacks.go.tmpl.
type templateData struct {
	Package        string
	Main           bool
	UnitDecls      string // pre-rendered `type X float64 / struct {...}` decls
	LangHelpers    string // pre-rendered must-parse / runtime helpers
	Structs        []structData
	Binds          []bindData
	Computeds      []computedData
	WidgetFields   []widgetFieldData
	FunctionCode   string
	Imports        map[string]bool
	NeedsBoolToInt bool // emit boolToInt helper only when boolToGoInt was used
}

type structData struct {
	Name   string
	Fields []structFieldData
}

type structFieldData struct {
	Name string
	Type string
}

type bindData struct {
	Name        string
	GoType      string
	InitVal     string
	Getter      string
	NoAccessors bool // skip emitting getter/setter (e.g. synthesized __slot<N>, __root)
}

type computedData struct {
	Name   string
	GoType string
	Body   string
}

type widgetFieldData struct {
	Name   string
	GoType string
}
