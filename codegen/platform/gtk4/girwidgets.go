package gtk4

import (
	"strconv"

	"duckfam.us/sngl/internal/buildhost"
	"duckfam.us/sngl/internal/gencache"
	"duckfam.us/sngl/ir"
	// The library runner: sngl:platform/gtk4 imports gir:, and a check with
	// no build around it resolves that through the runner internal/plugin
	// registers.
	_ "duckfam.us/sngl/internal/plugin"
)

// girSetting is the setting the gir: scheme's output records: the GIR the
// "gir" option selects, so a stored package is written again when the option
// changes and not only when the file it named does.
const girSetting = "gtk4.gir"

// cgoSetting is the "cgo" option, which decides what the declarations call.
const cgoSetting = "gtk4.cgo"

// The widget declarations are the gir: scheme's package (lib/x/scheme/gir),
// which sngl:platform/gtk4 imports. The scheme is SNGL; parsing GIR is not, so
// its handler calls gir.widgets, which this answers from the gtk4.widgets
// entry of the store -- the file served exactly as the store holds it,
// directive included, so what the checker reads says what it was generated
// from.
func registerGIRScheme(g *Generator) {
	buildhost.RegisterIntrinsic("gir.widgets", g.girWidgets)
	gencache.RegisterSetting(girSetting, g.girOption)
	gencache.RegisterSetting(cgoSetting, func() string { return strconv.FormatBool(g.cgoOption()) })
}

func (g *Generator) girWidgets(rec *buildhost.Recorder, _ *ir.Call, _ []any) (any, error) {
	req, err := widgetsRequest(g.girOption(), g.cgoOption())
	if err != nil {
		return nil, err
	}
	entry, data, err := g.genStore().Entry(req)
	if err != nil {
		return nil, err
	}
	setting, err := gencache.Setting(girSetting)
	if err != nil {
		return nil, err
	}
	cgo, err := gencache.Setting(cgoSetting)
	if err != nil {
		return nil, err
	}
	rec.Add(entry, setting, cgo)
	return string(data), nil
}

// widgets loads the generated declarations once per option, which is what
// says whether the option names introspection data this platform can read.
// The caller holds mu.
func (g *Generator) widgets() bool {
	if !g.fsLoaded {
		g.fsLoaded = true
		req, err := widgetsRequest(g.girOpt, g.cgoOpt)
		if err != nil {
			g.fsErr = err
		} else {
			_, g.fsErr = g.genStore().Get(req)
		}
	}
	return g.fsErr == nil
}

// cgoOption is what the "cgo" option says now.
func (g *Generator) cgoOption() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cgoOpt
}

// girOption is the GIR the "gir" option selects now.
func (g *Generator) girOption() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.girOpt
}
