package gtk4

import "duckfam.us/sngl/codegen/lang/golang"

// Config controls GTK4 code generation. Fields are populated via --opt flags.
type Config struct {
	Package string `option:"package"` // default: "main"
	Main    bool   `option:"main"`    // emit main() entrypoint
	GIRPath string `option:"gir"`     // default: "" — autodetect from standard paths
	// Set by internal/build for a build that becomes a binary.
	Executable bool `option:"executable"`

	// Lang globals (lib/language/go/golang.sngl).
	GoVersion  string `option:"goVersion"`  // Go toolchain version for go.mod (default: "1.23")
	GoModExtra string `option:"goModExtra"` // Extra text appended to temp test-module go.mod (e.g. replace directive)

	// Internal (set by CLI, not exposed in .sngl).
	Lang string `option:"lang"` // language identifier used to select the executor
}

func (c Config) withDefaults() Config {
	if c.Package == "" {
		c.Package = "main"
	}
	c.Package = golang.PackageName(c.Package, c.Main, c.Executable)
	if c.GoVersion == "" {
		c.GoVersion = "1.23"
	}
	return c
}

// scaffoldData is what model.go's and callbacks.go's heads are written from.
type scaffoldData struct {
	Package        string
	Main           bool
	UnitDecls      string // pre-rendered `type X float64 / struct {...}` decls
	LangHelpers    string // pre-rendered must-parse / runtime helpers
	Structs        []structData
	Binds          []bindData
	Computeds      []computedData
	WidgetFields   []widgetFieldData
	FunctionCode   string
	Imports        []string // model.go's; repeats are fine, the FileEmitter dedupes
	NeedsBoolToInt bool     // emit boolToInt helper only when boolToGoInt was used
	// NeedsGObjectSet emits the g_object_set_property helpers, for the GTK
	// properties that have no C setter of their own.
	NeedsGObjectSet bool
	NeedsSlotAnchor bool
	// NeedsCalendarDate emits snglCalendarDate, which a datepicker's change
	// handler reads its payload through.
	NeedsCalendarDate bool
	HasCanvas         bool // emit the cairo draw-func trampoline glue in callbacks.go
	Wrapped           bool // wrapped mode: import pkg/go/gtk4rt, omit the cgo preamble
	// CLinks is the #cgo flags and #include lines of the c.link directives
	// the program reaches, for the `#[cnative]` functions it calls. Its
	// calls are cgo, so a program carrying one is built in inline mode.
	CLinks string
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
