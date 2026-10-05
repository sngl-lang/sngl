package gtk4

import (
	"git.duckfam.us/jonathan/sngl/internal/buildhost"
	"git.duckfam.us/jonathan/sngl/internal/gencache"
	// The library runner: sngl:platform/gtk4 imports gir:, and a check with
	// no build around it resolves that through the runner internal/plugin
	// registers.
	_ "git.duckfam.us/jonathan/sngl/internal/plugin"
)

// girSetting is the setting the gir: scheme's output records: the GIR the
// "gir" option selects, so a stored package is written again when the option
// changes and not only when the file it named does.
const girSetting = "gtk4.gir"

// The widget declarations are the gir: scheme's package (lib/x/scheme/gir),
// which sngl:platform/gtk4 imports. The scheme is SNGL; parsing GIR is not, so
// its handler calls gir.widgets, which this answers from the gtk4.widgets
// entry of the store -- the file served exactly as the store holds it,
// directive included, so what the checker reads says what it was generated
// from.
func registerGIRScheme(g *Generator) {
	buildhost.RegisterIntrinsic("gir.widgets", g.girWidgets)
	gencache.RegisterSetting(girSetting, g.girOption)
}

func (g *Generator) girWidgets(rec *buildhost.Recorder, _ []any) (any, error) {
	req, err := girRequest(widgetsProducer, g.girOption())
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
	rec.Add(entry, setting)
	return string(data), nil
}

// widgets loads the generated declarations once per option, which is what
// says whether the option names introspection data this platform can read.
// The caller holds mu.
func (g *Generator) widgets() bool {
	if !g.fsLoaded {
		g.fsLoaded = true
		req, err := girRequest(widgetsProducer, g.girOpt)
		if err != nil {
			g.fsErr = err
		} else {
			_, g.fsErr = g.genStore().Get(req)
		}
	}
	return g.fsErr == nil
}

// girOption is the GIR the "gir" option selects now.
func (g *Generator) girOption() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.girOpt
}
