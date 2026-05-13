package fyne

import (
	"embed"
)

// Config controls code generation. Field names mirror fyne.sngl options.
type Config struct {
	Package string // Go package name (default: "ui")
	Main    bool   // emit a main() function for standalone apps
	AppName string // application display name; empty falls back to stdlib Name

	// Stdlib globals (lib/options.sngl).
	Name        string
	Icon        string
	Description string
	Version     string

	// Lang globals (codegen/lang/golang/golang.sngl).
	GoVersion  string // Go toolchain version emitted in `sngl run` go.mod (default: "1.23")
	GoModExtra string // Extra text appended to temp test-module go.mod (e.g. replace directive)

	// Internal (set by CLI, not exposed in .sngl).
	Lang string `option:"lang"` // language identifier used to select the executor
}

func (c Config) withDefaults() Config {
	if c.Package == "" {
		if c.Main {
			c.Package = "main"
		} else {
			c.Package = "ui"
		}
	}
	if c.AppName == "" {
		if c.Name != "" {
			c.AppName = c.Name
		} else {
			c.AppName = "SNGL App"
		}
	}
	if c.GoVersion == "" {
		c.GoVersion = "1.23"
	}
	return c
}

//go:embed templates/*
var templateFS embed.FS

// templateData is the data passed to the model.go.tmpl template.
type templateData struct {
	Package    string
	Main       bool
	AppName    string
	NeedsToast bool
	HasTimers  bool

	Structs      []structData
	UnitDecls    string // pre-rendered `type X float64 / struct {...}` decls
	LangHelpers  string // pre-rendered must-parse / runtime helpers
	Binds        []bindData
	Externs      []externData
	Computeds    []computedData
	WidgetFields []widgetFieldData
	Timers       []timerData
	FunctionCode string          // pre-rendered user functions
	Imports      map[string]bool // import set; template iterates in sorted key order and uses `index .Imports "path"` for lookups

	// CgoPreamble holds the cgo comment block + `import "C"` line.
	// Non-empty only when any native import uses NativePkg == "C".
	CgoPreamble string
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
	SetterExtra string // pre-rendered extra setter lines (entry sync, trigger, updaters)
	NoAccessors bool   // skip getter/setter (e.g. synthesized __slot<N> vars)
}

type externData struct {
	Name   string
	GoType string
}

type computedData struct {
	Name   string
	GoType string
	Body   string // pre-rendered expression
}

type widgetFieldData struct {
	Name   string
	GoType string
}

type timerData struct {
	Index      int
	IntervalMs int
	ActiveVar  string
	Body       string // pre-rendered mutation statements
}

// newTemplateData is now in compiler_ir.go as newIRTemplateData.
