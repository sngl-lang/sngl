package gtk4

import "embed"

// Config controls GTK4 code generation. Fields are populated via --opt flags.
type Config struct {
	Package string `option:"package"` // default: "main"
	Main    bool   `option:"main"`    // default: true — emit main() entrypoint
	GIRPath string `option:"gir"`     // default: "" — autodetect from standard paths

	// Lang globals (codegen/lang/golang/golang.sngl).
	GoVersion string `option:"goVersion"` // Go toolchain version for go.mod (default: "1.23")

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
	Package      string
	Main         bool
	Structs      []structData
	Binds        []bindData
	Computeds    []computedData
	WidgetFields []widgetFieldData
	UpdaterNames []string
	FunctionCode string
	Imports      map[string]bool
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
	SetterExtra string
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
