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
	GoVersion string // Go toolchain version emitted in `sngl run` go.mod (default: "1.23")
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
	Package     string
	Main        bool
	AppName     string
	NeedsTime   bool
	NeedsURL    bool
	NeedsCanvas bool
	NeedsToast  bool
	HasTimers   bool

	Structs      []structData
	Binds        []bindData
	Externs      []externData
	Computeds    []computedData
	Entries      []entryData
	WidgetFields []widgetFieldData
	Timers       []timerData
	UpdaterNames []string
	FunctionCode string   // pre-rendered user functions
	GoImports    []string // native Go import paths from Resolved fields
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

type entryData struct {
	FieldName         string
	MultiLine         bool
	Password          bool
	Placeholder       string
	PlaceholderQuoted string
	BindTarget        string
	OnChangedBody     string // pre-rendered updater calls
	Rows              int
}

type widgetFieldData struct {
	Name   string
	GoType string
}

type timerData struct {
	Index            int
	IntervalMs       int
	ActiveVar        string
	Body             string // pre-rendered mutation statements
	AffectedUpdaters string // pre-rendered updater calls
}

// newTemplateData is now in compiler_ir.go as newIRTemplateData.
