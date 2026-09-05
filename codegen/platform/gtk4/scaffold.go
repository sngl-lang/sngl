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
	// NeedsGObjectSet emits the g_object_set_property helpers, for the GTK
	// properties that have no C setter of their own.
	NeedsGObjectSet bool
	HasCanvas       bool // emit the cairo draw-func trampoline glue in callbacks.go
	Wrapped         bool // wrapped mode: import pkg/go/gtk4rt, omit the cgo preamble
	// Timers is one entry per schedule the program describes. gtk4 emitted
	// none at all before: the analysis read a list the checker hoisted every
	// timer onto and this platform never looked at it, so a program with a
	// timer compiled clean and never ticked.
	Timers []timerData
}

type timerData struct {
	Index      int
	IntervalMs int
	// Gate is the enabled expression rendered as Go, tested inside the
	// callback rather than around the arming: the source runs for the
	// program's life and each tick asks whether it should do anything, which
	// is what makes flipping the gate take effect with no re-arming wiring.
	Gate string
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
