package gir

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const miniGIR = `<?xml version="1.0"?>
<repository version="1.2">
  <namespace name="Gtk" version="4.0">
    <interface name="Orientable" c:type="GtkOrientable">
      <property name="orientation" writable="1">
        <type name="Orientation"/>
      </property>
    </interface>
    <class name="Button" c:type="GtkButton">
      <implements name="Orientable"/>
      <constructor name="new" c:identifier="gtk_button_new"/>
      <constructor name="new_with_label" c:identifier="gtk_button_new_with_label">
        <parameters>
          <parameter name="label">
            <type name="utf8"/>
          </parameter>
        </parameters>
      </constructor>
      <property name="label" writable="1">
        <type name="utf8"/>
      </property>
      <glib:signal name="clicked"/>
    </class>
  </namespace>
</repository>
`

func writeGIR(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Mini-4.0.gir")
	if err := os.WriteFile(path, []byte(miniGIR), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The cache must be transparent: a registry read back from disk has to equal
// the one a fresh parse produces, field for field. DeepEqual rather than a
// hand-written comparison on purpose — a field added to ClassInfo or Prop that
// the cache fails to carry is exactly the bug this guards, and a comparison
// that names its fields would miss the new one too.
func TestLoadGIR_CachedEqualsParsed(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := writeGIR(t)

	want, err := ParseGIR(path)
	if err != nil {
		t.Fatalf("ParseGIR: %v", err)
	}

	// First call populates the cache, second reads it back.
	if _, err := LoadGIR(path); err != nil {
		t.Fatalf("LoadGIR (populate): %v", err)
	}
	got, err := LoadGIR(path)
	if err != nil {
		t.Fatalf("LoadGIR (cached): %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("cached registry differs from parsed:\n got: %s\nwant: %s",
			dumpRegistry(got), dumpRegistry(want))
	}
}

// The real Gtk-4.0.gir when the host has one: the bundled fixture is too small
// to exercise parent-class and interface property merging, which is where a
// dropped field actually showed up.
func TestLoadGIR_CachedEqualsParsed_HostGIR(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := hostGIR(t)

	want, err := ParseGIR(path)
	if err != nil {
		t.Fatalf("ParseGIR: %v", err)
	}
	if _, err := LoadGIR(path); err != nil {
		t.Fatalf("LoadGIR (populate): %v", err)
	}
	got, err := LoadGIR(path)
	if err != nil {
		t.Fatalf("LoadGIR (cached): %v", err)
	}

	if len(got.Classes) != len(want.Classes) {
		t.Fatalf("classes = %d, want %d", len(got.Classes), len(want.Classes))
	}
	for name, wantClass := range want.Classes {
		gotClass, ok := got.Classes[name]
		if !ok {
			t.Fatalf("cached registry lost class %q", name)
		}
		if !reflect.DeepEqual(gotClass, wantClass) {
			t.Fatalf("class %q differs after a cache round trip:\n got: %+v\nwant: %+v",
				name, gotClass, wantClass)
		}
	}
	if !reflect.DeepEqual(got.Interfaces, want.Interfaces) {
		t.Error("interfaces differ after a cache round trip")
	}
}

// A change to the shape of TypeRegistry must invalidate every entry: an old
// gob decodes without complaint and leaves any new field zero.
func TestCacheKey_ChangesWithSchema(t *testing.T) {
	before := schemaFingerprint()
	if before == "" {
		t.Fatal("empty schema fingerprint")
	}

	var h hashRecorder
	writeTypeShape(&h, reflect.TypeFor[ClassInfo](), map[reflect.Type]bool{})
	shape := h.String()
	for _, field := range []string{"Props", "Signals", "Implements", "Parent", "CType"} {
		if !strings.Contains(shape, field) {
			t.Errorf("fingerprint does not cover ClassInfo.%s; a change to it would not invalidate the cache", field)
		}
	}
}

type hashRecorder struct{ b strings.Builder }

func (h *hashRecorder) Write(p []byte) (int, error) { return h.b.Write(p) }
func (h *hashRecorder) String() string              { return h.b.String() }

// hostGIR returns the host's Gtk-4.0.gir, skipping when GTK 4 development
// files are not installed.
func hostGIR(t *testing.T) string {
	t.Helper()
	// Mirrors gtk4.girAutoPaths; that lives in the parent package, which
	// imports this one.
	for _, p := range []string{
		"/usr/share/gir-1.0/Gtk-4.0.gir",
		"/usr/local/share/gir-1.0/Gtk-4.0.gir",
		"/opt/homebrew/share/gir-1.0/Gtk-4.0.gir",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("no host Gtk-4.0.gir — install libgtk-4-dev to exercise this")
	return ""
}

func dumpRegistry(r *TypeRegistry) string {
	var b strings.Builder
	names := make([]string, 0, len(r.Classes))
	for n := range r.Classes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "\n  %s: %+v", n, r.Classes[n])
	}
	return b.String()
}

// Editing the GIR file must invalidate the entry rather than serve the old
// registry — the key carries size and mtime for exactly this.
func TestLoadGIR_InvalidatesOnChange(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := writeGIR(t)

	if _, err := LoadGIR(path); err != nil {
		t.Fatalf("LoadGIR: %v", err)
	}

	edited := miniGIR + "<!-- a second widget would land here -->\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force a distinct mtime even on a coarse-grained filesystem.
	stamp := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	reg, err := LoadGIR(path)
	if err != nil {
		t.Fatalf("LoadGIR after edit: %v", err)
	}
	if _, ok := reg.Classes["Button"]; !ok {
		t.Error("re-parsed registry lost class Button")
	}
}

func TestLoadGIR_MissingFileFallsBackToParseError(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := LoadGIR(filepath.Join(t.TempDir(), "absent.gir")); err == nil {
		t.Error("LoadGIR on a missing file returned no error")
	}
}
