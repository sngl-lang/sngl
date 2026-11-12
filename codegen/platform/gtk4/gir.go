package gtk4

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"duckfam.us/sngl/codegen/platform/gtk4/gir"
)

// pickPrimaryConstructorInfo is the first constructor in the class, and the
// zero value where GIR declares none: GTK ships no gtk_<lower>_new for most
// classes without one, so there is no name to derive.
func pickPrimaryConstructorInfo(info *gir.ClassInfo) gir.ConstructorInfo {
	if len(info.Constructors) == 0 {
		return gir.ConstructorInfo{}
	}
	return info.Constructors[0]
}

// adderReceiver is the C type a child adder's receiver is cast to: the class
// that declares it, or the container's own where GIR named none.
func adderReceiver(adder gir.ChildAdder, cType string) string {
	if adder.Owner != "" {
		return adder.Owner
	}
	return cType
}

// gObjectSetFn is the runtime's set of a property by its name, for each kind
// of value, which a property GTK ships no setter for is written through.
var gObjectSetFn = map[propValueKind]string{
	propString: "sngl_set_prop_string",
	propBool:   "sngl_set_prop_bool",
	propInt:    "sngl_set_prop_int",
	propFloat:  "sngl_set_prop_double",
	propEnum:   "sngl_set_prop_int",
}

// gtkSetterEntry is the C setter for one prop on one widget class. RecvType
// is set where the setter takes an interface (GtkEditable) or an ancestor
// rather than the class.
type gtkSetterEntry struct {
	Setter   string
	RecvType string
}

// girBuiltin selects the bundled subset explicitly, for a program that wants
// a gir: package that does not vary with the host's GTK version.
const girBuiltin = "builtin"

// girAutoPaths are the standard locations checked when --opt gir= is not set.
var girAutoPaths = []string{
	"/usr/share/gir-1.0/Gtk-4.0.gir",
	"/usr/local/share/gir-1.0/Gtk-4.0.gir",
	"/opt/homebrew/share/gir-1.0/Gtk-4.0.gir",
}

// resolveGIRPath is the path to the Gtk-4.0.gir file: girPath where it is
// set, and the first of the standard locations otherwise.
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
