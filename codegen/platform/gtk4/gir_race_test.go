package gtk4

import (
	"sync"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/buildhost"
	"git.duckfam.us/jonathan/sngl/internal/gencache"
)

// The generator is one value shared by every check and build in the process
// -- the LSP checks while a build generates -- so the GIR option it holds is
// read by the gir: scheme's setting and intrinsic while Configure and a build's
// own option write it. Run under -race.
func TestGIROptionIsSafeConcurrently(t *testing.T) {
	g := &Generator{store: gencache.Open(t.TempDir())}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 20 {
				switch (i + j) % 5 {
				case 0:
					_ = g.Configure(map[string]string{"gir": girBuiltin})
				case 1:
					_, _ = g.useGIR(girBuiltin)
				case 2:
					_, _ = g.girWidgets(buildhost.NewRecorder(), nil)
				case 3:
					_ = g.Unavailable()
				case 4:
					_ = g.girOption()
				}
			}
		})
	}
	wg.Wait()
}
