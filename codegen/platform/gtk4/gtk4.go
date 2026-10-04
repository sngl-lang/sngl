// Package gtk4 is the SNGL platform generator for GTK4 applications.
package gtk4

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/internal/gencache"
	"git.duckfam.us/jonathan/sngl/ir"
)

// pickPrimaryConstructorInfo returns the full ConstructorInfo for the first
// constructor in the class, and the zero value when GIR declares none. There
// is no name to derive for that case: GTK ships no gtk_<lower>_new for 46 of
// the 47 classes without one, so a derived name would only move a link error
// into the user's build. OnCreateNode refuses those with a diagnostic.
func pickPrimaryConstructorInfo(info *gir.ClassInfo) gir.ConstructorInfo {
	if len(info.Constructors) == 0 {
		return gir.ConstructorInfo{}
	}
	return info.Constructors[0]
}

// girTypeIsNamedNonPrimitive reports whether the GIR raw type name
// refers to a non-primitive declared type (enum, class, struct) rather
// than a built-in scalar. Used to map GIR property types like
// "Orientation" → cgo "GtkOrientation".
func girTypeIsNamedNonPrimitive(name string) bool {
	if name == "" {
		return false
	}
	switch name {
	case "utf8", "filename", "gchararray",
		"gboolean",
		"gint", "gint32", "gint64",
		"guint", "guint32", "guint64", "gsize",
		"gdouble", "gfloat",
		"none":
		return false
	}
	// Heuristic: GIR enum/class names within the current namespace are
	// bare CamelCase identifiers (no namespace dot). Cross-namespace
	// names contain a "." (e.g. "Gio.File") and need different handling
	// — return false there so the codegen falls back to defaults.
	return !strings.Contains(name, ".") && name[0] >= 'A' && name[0] <= 'Z'
}

// girAutoPaths are the standard locations checked when --opt gir= is not set.
var girAutoPaths = []string{
	"/usr/share/gir-1.0/Gtk-4.0.gir",
	"/usr/local/share/gir-1.0/Gtk-4.0.gir",
	"/opt/homebrew/share/gir-1.0/Gtk-4.0.gir",
}

func init() {
	g := &Generator{}
	codegen.RegisterNative(platformName, g)
	registerGIRScheme(g)
}

// Generator implements codegen.PlatformGenerator for GTK4.
type Generator struct {
	once     sync.Once
	registry *gir.TypeRegistry
	initErr  error // set if autodetect GIR load fails
	minimal  bool  // registry is the bundled subset, not the host's GIR
	girOpt   string
	fsOnce   sync.Once
	fsErr    error
	// store is where the registry and the declarations derived from it are
	// kept between builds. Nil means the process's default store.
	store *gencache.Store
}

func (g *Generator) genStore() *gencache.Store {
	if g == nil || g.store == nil {
		return gencache.Default()
	}
	return g.store
}

// Configure implements codegen.OptionConfigurable. Reads the "gir" option so
// the gir: import sngl:platform/gtk4 makes (resolved during type-check, before
// code generation) can honor the CLI-supplied GIR path. Resets the cached
// registry and the declarations built from it so both re-load against the new
// path.
func (g *Generator) Configure(opts map[string]string) error {
	g.girOpt = opts["gir"]
	g.once = sync.Once{}
	g.fsOnce = sync.Once{}
	g.registry = nil
	g.initErr = nil
	g.fsErr = nil
	return nil
}

// platformName is this platform's registry identifier, and the name a
// diagnostic spells so the reader knows which target rejected their program.
const platformName = "gtk4"

func (g *Generator) PlatformIdentifier() string { return platformName }
func (g *Generator) Description() string {
	return "Native Linux/GNOME desktop GUI using GTK4."
}
func (g *Generator) SupportedLangs() []string { return []string{"go"} }

// Unavailable implements codegen.PlatformAvailability. It is answered from the
// declarations rather than the registry: the checker asks it of every
// registered platform on every build, and the declarations are what such a
// build reads anyway.
func (g *Generator) Unavailable() error {
	g.widgets()
	return g.fsErr
}

// gir lazy-loads the widget registry, caching both the registry and the
// failure. Configure resets the cache when --opt gir= changes.
func (g *Generator) gir() (*gir.TypeRegistry, error) {
	g.once.Do(func() {
		g.registry, g.minimal, g.initErr = girRegistryIn(g.genStore(), g.girOpt)
	})
	return g.registry, g.initErr
}

// useGIR is how everything that needs the registry gets it, including the code
// generator, which used to resolve its own. Passing the option rather than
// reading it means one resolver, one cache and one answer: a caller holding a
// Config built from the same --opt cannot end up with a different registry than
// type-check used, which is what happened when there were two.
//
// A nil Generator answers from the resolver directly, for a test that builds a
// compilation without one.
func (g *Generator) useGIR(opt string) (*gir.TypeRegistry, error) {
	if g == nil {
		reg, _, err := girRegistry(opt)
		return reg, err
	}
	if opt != "" && opt != g.girOpt {
		// A Config naming a GIR the generator was not configured with wins, and
		// re-arms the cache so every later reader agrees with it.
		g.girOpt = opt
		g.once = sync.Once{}
		g.fsOnce = sync.Once{}
		g.registry, g.initErr, g.fsErr = nil, nil, nil
	}
	return g.gir()
}

// girRegistry resolves the "gir" option to a widget registry, and reports
// whether the result is the bundled subset. It is the one place that decides,
// because the code generator used to resolve its own and the two could disagree
// -- and the generator's copy discarded the parse error, so a path that did not
// load left it with no registry and every widget reported as undeclared.
//
//   - "builtin": the bundled subset, whatever the host has.
//   - any other non-empty value: that path, and a failure to load it is an
//     error, since the caller named it.
//   - empty: the host's Gtk-4.0.gir if the probe finds one, else the bundled
//     subset -- a host without GTK 4 development files can still check and
//     document the widgets codegen/platform/gtk4 wraps.
//
// The answer is the gtk4.registry entry of the default store (girstore.go), so
// an unchanged GIR is parsed once rather than once per build.
func girRegistry(opt string) (*gir.TypeRegistry, bool, error) {
	return girRegistryIn(gencache.Default(), opt)
}

func girRegistryIn(store *gencache.Store, opt string) (*gir.TypeRegistry, bool, error) {
	req, err := girRequest(registryProducer, opt)
	if err != nil {
		return nil, false, err
	}
	data, err := store.Get(req)
	if err != nil {
		return nil, false, err
	}
	return decodeRegistry(data)
}

// usingMinimalGIR reports whether the registry is the bundled subset, which
// is what a test naming host-only vocabulary skips on.
func (g *Generator) usingMinimalGIR() bool {
	_, _ = g.gir()
	return g.minimal
}

// Generate writes gtk4 source files directly into sink. This is the
// sink-based path platforms migrate to during the codegen unification.
func (g *Generator) Generate(req *codegen.Request, sink codegen.Sink) error {
	// Targeting gtk4 without the GIR file is the one case that must be loud:
	// the widget overrides were withheld, so generation would otherwise
	// silently emit an empty UI.
	if _, err := g.gir(); err != nil {
		return fmt.Errorf("platform gtk4 is unavailable here: %w", err)
	}
	// Agent-mode test build: suppress the user's main() loop and force
	// the package name to "main" so the agent's func main() compiles
	// alongside it. The launcher's `go build .` step expects a main
	// package on disk.
	agentMode := codegen.OptionString(req.Options, "testMode") == "agent"
	if agentMode {
		if req.Options == nil {
			req.Options = &ir.StructLit{}
		}
		codegen.SetOptionField(req.Options, "main", false)
		codegen.SetOptionField(req.Options, "package", "main")
	}

	// gtk4 has no error-boundary emitter. Left to travel on, the boundary
	// reaches the Go renderer, which panics on a statement kind no platform
	// was meant to hand it.
	if err := codegen.FirstUnimplementedNode(req.Pkg, platformName, string(ir.BuiltinErrorBoundary)); err != nil {
		return err
	}

	c := &compilation{gen: g}
	// gtk4NoWrap is a manual escape hatch that pins the inline-cgo path. Agent
	// and Snapshot builds no longer force it: they emit a harness that matches
	// the model's chosen mode (wrapped harnesses call BuildUI over gtk4rt.Handle
	// and snapshot via gtk4rt). After EmitFromMutation, c.wrapped reflects the
	// model's actual mode (the emitIR scan sets it), so the harness can match.
	c.disableWrapped = codegen.OptionBool(req.Options, "gtk4NoWrap")
	m, err := c.BuildMutationModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return err
	}
	if err := c.EmitFromMutation(m, req, sink); err != nil {
		return err
	}
	if golang.PackageUsesI18n(req.Pkg) {
		if err := golang.EmitI18nManifestEmbed(sink, c.cfg.Package, req.ProjectFS, ""); err != nil {
			return fmt.Errorf("gtk4: i18n manifest embed: %w", err)
		}
	}

	if codegen.OptionBool(req.Options, "test") {
		testFns, suffixes, methodFields := codegen.CollectTestFuncs(req.Pkg)
		if len(testFns) > 0 {
			if agentMode {
				src := golang.LowerTestFile(c.cfg.Package, req.Pkg, testFns, suffixes, methodFields, golang.TestEmitAgent)
				if err := writeRawFile(sink, "testagent_main.go", []byte(src)); err != nil {
					return err
				}
				// gtk4's New() returns *Model; the agent helper file boots
				// GTK in headless mode and calls buildWidgetTree() to
				// materialize widgets so per-id event invokers fire against
				// real GTK objects. BuildUI is intentionally NOT called here —
				// sngl_test_activate calls it with the snapshot app so that
				// m.__root has no prior parent when gtk_window_set_child runs.
				mainSrc := []byte(`package ` + c.cfg.Package + `

/*
#include <gtk/gtk.h>
*/
import "C"

import (
	"sync"

	"git.duckfam.us/jonathan/sngl/pkg/go/testagent"
)

var gtkInit sync.Once

var currentModel *Model

func setCurrentTestModel(m *Model) { currentModel = m }

func newTestComponent() *Model {
	gtkInit.Do(func() { C.gtk_init() })
	m := New()
	m.buildWidgetTree()
	return m
}

func main() { testagent.Main() }
`)
				if err := writeRawFile(sink, "agent_main.go", agentMainBytes(c.cfg.Package, c.wrapped, mainSrc)); err != nil {
					return err
				}
				// snapshot.go: reuses the gtk4SnapshotCgo preamble.
				// Uses g_application_register (not g_application_run) so
				// BuildUI and sngl_pump_until_mapped run in the Go call
				// stack — outside any signal callback — making it safe to
				// call blocking g_main_context_iteration calls there.
				snapshotSrc := []byte("package " + c.cfg.Package + "\n\n" + gtk4SnapshotCgo + `
import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"git.duckfam.us/jonathan/sngl/pkg/go/testagent"
)

func currentTestModel() *Model { return currentModel }

func snapshotBytesGtk(m *Model) (string, []byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if m == nil {
		return "", nil, fmt.Errorf("no current model registered")
	}

	f, err := os.CreateTemp("", "sngl-snap-*.png")
	if err != nil {
		return "", nil, err
	}
	f.Close()
	defer os.Remove(f.Name())

	cAppID := C.CString("dev.sngl.test.snapshot")
	defer C.free(unsafe.Pointer(cAppID))
	app := C.gtk_application_new(cAppID, C.G_APPLICATION_NON_UNIQUE)
	defer C.g_object_unref(C.gpointer(unsafe.Pointer(app)))

	// Register without running the main loop so BuildUI and the pump
	// execute in the current (locked) OS thread without re-entrancy.
	var gerr *C.GError
	if C.g_application_register((*C.GApplication)(unsafe.Pointer(app)), nil, &gerr) == 0 {
		if gerr != nil {
			C.g_error_free(gerr)
		}
		return "", nil, fmt.Errorf("g_application_register failed")
	}

	win := m.BuildUI(app)
	if win == nil {
		return "", nil, fmt.Errorf("BuildUI returned nil")
	}
	C.gtk_window_set_default_size((*C.GtkWindow)(unsafe.Pointer(win)), 800, 600)
	C.gtk_window_present((*C.GtkWindow)(unsafe.Pointer(win)))
	C.sngl_pump_until_mapped(win, 1000)

	cPath := C.CString(f.Name())
	defer C.free(unsafe.Pointer(cPath))
	if rc := C.sngl_snapshot(win, 800, 600, cPath); rc != 0 {
		return "", nil, fmt.Errorf("sngl_snapshot rc=%d", int(rc))
	}

	data, err := os.ReadFile(f.Name())
	if err != nil {
		return "", nil, err
	}
	return "image/png", data, nil
}

func init() {
	testagent.RegisterSnapshot(func() (string, []byte, error) {
		return snapshotBytesGtk(currentTestModel())
	})
}
`)
				if err := writeRawFile(sink, "snapshot.go", agentSnapshotBytes(c.cfg.Package, c.wrapped, snapshotSrc)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// writeRawFile writes pre-formatted content directly to sink without
// running it through the language file emitter (which would re-attach
// source maps and headers we don't want on synthetic test/agent files).
func writeRawFile(sink codegen.Sink, name string, content []byte) error {
	wc, err := sink.Create(name)
	if err != nil {
		return err
	}
	if _, err := wc.Write(content); err != nil {
		wc.Close()
		return err
	}
	return wc.Close()
}

// girBuiltin selects the bundled subset explicitly, for a test that wants a
// registry that does not vary with the host's GTK version.
const girBuiltin = "builtin"

// resolveGIRPath returns the path to the Gtk-4.0.gir file.
// If girPath is non-empty it validates that path. Otherwise it probes the
// standard autodetect locations.
func resolveGIRPath(girPath string) (string, error) {
	if girPath != "" {
		if _, err := os.Stat(girPath); err != nil {
			return "", fmt.Errorf("--opt gir=%s: %w", girPath, err)
		}
		return girPath, nil
	}
	for _, p := range girAutoPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("GTK 4 introspection data (Gtk-4.0.gir) not found in " +
		strings.Join(girAutoPaths, ", ") +
		" — install the GTK 4 development package (Debian/Ubuntu: libgtk-4-dev, " +
		"Fedora: gtk4-devel, Arch: gtk4, macOS: brew install gtk4) " +
		"or point at the file with --opt gir=/path/to/Gtk-4.0.gir")
}
