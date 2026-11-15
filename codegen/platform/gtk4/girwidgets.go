package gtk4

import (
	"sync"

	"duckfam.us/sngl/internal/buildhost"
	"duckfam.us/sngl/internal/gencache"
	"duckfam.us/sngl/ir"
	// The library runner: a program importing gir: is checked through the
	// runner internal/plugin registers, which a check with no build around
	// it needs too.
	_ "duckfam.us/sngl/internal/plugin"
)

// girSetting is the setting the gir: scheme's output records: the GIR the
// "gir" option selects, so a stored package is written again when the option
// changes and not only when the file it named does.
const girSetting = "gtk4.gir"

// girState is the "gir" option, which selects the GIR a program's gir:
// import is declared from. The platform's own widgets are committed
// (widgets.sngl) and read nothing of it.
type girState struct {
	// mu guards opt: the generator is one value shared by every check and
	// build in the process, and the option is read by the scheme's setting
	// and intrinsic while Configure writes it.
	mu  sync.Mutex
	opt string
}

func (s *girState) option() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opt
}

func (s *girState) setOption(opt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opt = opt
}

// The gir: scheme is SNGL (lib/x/scheme/gir); parsing GIR is not, so its
// handler calls gir.widgets, which this answers from the gtk4.widgets entry
// of the store -- the file served exactly as the store holds it, directive
// included, so what the checker reads says what it was generated from.
func registerGIRScheme(s *girState) {
	buildhost.RegisterIntrinsic("gir.widgets", func(rec *buildhost.Recorder, _ *ir.Call, _ []any) (any, error) {
		req, err := widgetsRequest(s.option())
		if err != nil {
			return nil, err
		}
		entry, data, err := gencache.Default().Entry(req)
		if err != nil {
			return nil, err
		}
		setting, err := gencache.Setting(girSetting)
		if err != nil {
			return nil, err
		}
		rec.Add(entry, setting)
		return string(data), nil
	})
	gencache.RegisterSetting(girSetting, s.option)
}
