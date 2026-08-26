package gir_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/ir"
)

const minimalGIR = `<?xml version="1.0"?>
<repository version="1.2"
  xmlns="http://www.gtk.org/introspection/core/1.0"
  xmlns:c="http://www.gtk.org/introspection/c/1.0"
  xmlns:glib="http://www.gtk.org/introspection/glib/1.0">
  <namespace name="Gtk" version="4.0">
    <class name="Button" c:type="GtkButton">
      <constructor name="new_with_label" c:identifier="gtk_button_new_with_label">
        <parameters>
          <parameter name="label" transfer-ownership="none" nullable="1">
            <type name="utf8" c:type="const gchar*"/>
          </parameter>
        </parameters>
      </constructor>
      <property name="label" writable="1">
        <type name="utf8" c:type="gchar*"/>
      </property>
      <glib:signal name="clicked">
        <return-value transfer-ownership="none">
          <type name="none" c:type="void"/>
        </return-value>
      </glib:signal>
    </class>
    <class name="Label" c:type="GtkLabel">
      <constructor name="new" c:identifier="gtk_label_new">
        <parameters>
          <parameter name="str" transfer-ownership="none" nullable="1">
            <type name="utf8" c:type="const gchar*"/>
          </parameter>
        </parameters>
      </constructor>
      <property name="label" writable="1">
        <type name="utf8" c:type="gchar*"/>
      </property>
    </class>
  </namespace>
</repository>`

func TestParseGIR_Button(t *testing.T) {
	reg, err := gir.ParseGIRBytes([]byte(minimalGIR))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, ok := reg.Classes["Button"]
	if !ok {
		t.Fatal("Button not found")
	}
	if info.CType != "GtkButton" {
		t.Errorf("CType = %q, want GtkButton", info.CType)
	}
	if info.Constructor.Name != "gtk_button_new_with_label" {
		t.Errorf("Constructor.Name = %q, want gtk_button_new_with_label", info.Constructor.Name)
	}
	if len(info.Constructor.Params) != 1 || info.Constructor.Params[0].Name != "label" {
		t.Errorf("Constructor.Params = %v", info.Constructor.Params)
	}
	if len(info.Props) != 1 || info.Props[0].Name != "label" {
		t.Errorf("Props = %v", info.Props)
	}
	if info.Props[0].IRType.Kind != ir.TypeString {
		t.Errorf("Props[0].IRType.Kind = %v, want TypeString", info.Props[0].IRType.Kind)
	}
	if len(info.Signals) != 1 || info.Signals[0].Name != "clicked" {
		t.Errorf("Signals = %v", info.Signals)
	}
}

func TestParseGIR_Label(t *testing.T) {
	reg, err := gir.ParseGIRBytes([]byte(minimalGIR))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, ok := reg.Classes["Label"]
	if !ok {
		t.Fatal("Label not found")
	}
	if info.Constructor.Name != "gtk_label_new" {
		t.Errorf("Constructor.Name = %q, want gtk_label_new", info.Constructor.Name)
	}
}

func TestParseGIR_Unknown(t *testing.T) {
	reg, _ := gir.ParseGIRBytes([]byte(minimalGIR))
	if _, ok := reg.Classes["Nonexistent"]; ok {
		t.Error("unexpected class found")
	}
}

func TestParseGIR_MultipleConstructors(t *testing.T) {
	// Second constructor must be ignored; first one wins.
	const src = `<?xml version="1.0"?>
<repository version="1.2"
  xmlns="http://www.gtk.org/introspection/core/1.0"
  xmlns:c="http://www.gtk.org/introspection/c/1.0"
  xmlns:glib="http://www.gtk.org/introspection/glib/1.0">
  <namespace name="Gtk" version="4.0">
    <class name="Button" c:type="GtkButton">
      <constructor name="new_with_label" c:identifier="gtk_button_new_with_label">
        <parameters>
          <parameter name="label" transfer-ownership="none">
            <type name="utf8" c:type="const gchar*"/>
          </parameter>
        </parameters>
      </constructor>
      <constructor name="new" c:identifier="gtk_button_new">
        <parameters>
          <parameter name="ignored_param" transfer-ownership="none">
            <type name="utf8" c:type="const gchar*"/>
          </parameter>
        </parameters>
      </constructor>
    </class>
  </namespace>
</repository>`
	reg, err := gir.ParseGIRBytes([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, ok := reg.Classes["Button"]
	if !ok {
		t.Fatal("Button not found")
	}
	// First constructor must be kept.
	if info.Constructor.Name != "gtk_button_new_with_label" {
		t.Errorf("Constructor.Name = %q, want gtk_button_new_with_label", info.Constructor.Name)
	}
	// Params from second constructor must not bleed into first.
	if len(info.Constructor.Params) != 1 {
		t.Errorf("Constructor.Params len = %d, want 1; params = %v", len(info.Constructor.Params), info.Constructor.Params)
	} else if info.Constructor.Params[0].Name != "label" {
		t.Errorf("Constructor.Params[0].Name = %q, want label", info.Constructor.Params[0].Name)
	}
}

func TestParseGIR_InterfaceMerge(t *testing.T) {
	const src = `<?xml version="1.0"?>
<repository version="1.2"
  xmlns="http://www.gtk.org/introspection/core/1.0"
  xmlns:c="http://www.gtk.org/introspection/c/1.0"
  xmlns:glib="http://www.gtk.org/introspection/glib/1.0">
  <namespace name="Gtk" version="4.0">
    <interface name="Orientable">
      <property name="orientation" writable="1">
        <type name="Orientation" c:type="GtkOrientation"/>
      </property>
    </interface>
    <class name="Box" c:type="GtkBox">
      <implements name="Orientable"/>
      <property name="spacing" writable="1">
        <type name="gint" c:type="int"/>
      </property>
    </class>
    <class name="Plain" c:type="GtkPlain">
      <property name="label" writable="1">
        <type name="utf8" c:type="gchar*"/>
      </property>
    </class>
  </namespace>
</repository>`
	reg, err := gir.ParseGIRBytes([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	iface, ok := reg.Interfaces["Orientable"]
	if !ok {
		t.Fatal("Orientable interface not captured")
	}
	if len(iface.Props) != 1 || iface.Props[0].Name != "orientation" {
		t.Errorf("Orientable.Props = %v", iface.Props)
	}
	box := reg.Classes["Box"]
	if box == nil {
		t.Fatal("Box class not found")
	}
	if len(box.Implements) != 1 || box.Implements[0] != "Orientable" {
		t.Errorf("Box.Implements = %v", box.Implements)
	}
	names := map[string]bool{}
	for _, p := range box.Props {
		names[p.Name] = true
	}
	if !names["spacing"] {
		t.Error("Box missing direct prop spacing")
	}
	if !names["orientation"] {
		t.Error("Box missing merged interface prop orientation")
	}
	// Plain class without implements must not gain interface props.
	plain := reg.Classes["Plain"]
	if plain == nil {
		t.Fatal("Plain class not found")
	}
	for _, p := range plain.Props {
		if p.Name == "orientation" {
			t.Error("Plain unexpectedly received orientation from interface")
		}
	}
}

func TestParseGIR_InterfaceMergeDirectWins(t *testing.T) {
	// When a class declares the same property as its interface, the class's
	// declaration must not be duplicated.
	const src = `<?xml version="1.0"?>
<repository version="1.2"
  xmlns="http://www.gtk.org/introspection/core/1.0"
  xmlns:c="http://www.gtk.org/introspection/c/1.0"
  xmlns:glib="http://www.gtk.org/introspection/glib/1.0">
  <namespace name="Gtk" version="4.0">
    <interface name="Orientable">
      <property name="orientation" writable="1">
        <type name="Orientation"/>
      </property>
    </interface>
    <class name="Box" c:type="GtkBox">
      <implements name="Orientable"/>
      <property name="orientation" writable="1">
        <type name="Orientation"/>
      </property>
    </class>
  </namespace>
</repository>`
	reg, err := gir.ParseGIRBytes([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	box := reg.Classes["Box"]
	count := 0
	for _, p := range box.Props {
		if p.Name == "orientation" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("orientation appears %d times, want 1", count)
	}
}

func TestParseGIR_UnmappableType(t *testing.T) {
	const src = `<?xml version="1.0"?>
<repository version="1.2"
  xmlns="http://www.gtk.org/introspection/core/1.0"
  xmlns:c="http://www.gtk.org/introspection/c/1.0"
  xmlns:glib="http://www.gtk.org/introspection/glib/1.0">
  <namespace name="Gtk" version="4.0">
    <class name="Weird" c:type="GtkWeird">
      <constructor name="new" c:identifier="gtk_weird_new"/>
      <property name="custom" writable="1">
        <type name="SomeUnknownType"/>
      </property>
    </class>
  </namespace>
</repository>`
	reg, err := gir.ParseGIRBytes([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info := reg.Classes["Weird"]
	if info == nil {
		t.Fatal("Weird not found")
	}
	if len(info.Props) != 1 || info.Props[0].IRType.Kind != ir.TypeDyn {
		t.Errorf("expected TypeDyn fallback, got %v", info.Props)
	}
}

// setterGIR exercises everything a property's setter reference has to carry:
// a setter whose name is not derivable from the class and property names, a
// setter whose value parameter is typed differently from the property, a
// property with no setter at all, an enumeration, and an array-typed property.
const setterGIR = `<?xml version="1.0"?>
<repository version="1.2"
  xmlns="http://www.gtk.org/introspection/core/1.0"
  xmlns:c="http://www.gtk.org/introspection/c/1.0"
  xmlns:glib="http://www.gtk.org/introspection/glib/1.0">
  <namespace name="Gtk" version="4.0">
    <enumeration name="Orientation" c:type="GtkOrientation">
      <member name="horizontal" value="0" c:identifier="GTK_ORIENTATION_HORIZONTAL"/>
      <member name="vertical" value="1" c:identifier="GTK_ORIENTATION_VERTICAL"/>
    </enumeration>
    <class name="Image" c:type="GtkImage">
      <constructor name="new" c:identifier="gtk_image_new"/>
      <method name="set_from_icon_name" c:identifier="gtk_image_set_from_icon_name">
        <parameters>
          <instance-parameter name="image">
            <type name="Image" c:type="GtkImage*"/>
          </instance-parameter>
          <parameter name="icon_name" transfer-ownership="none">
            <type name="utf8" c:type="const char*"/>
          </parameter>
        </parameters>
      </method>
      <method name="set_column_spacing" c:identifier="gtk_image_set_column_spacing">
        <parameters>
          <instance-parameter name="image">
            <type name="Image" c:type="GtkImage*"/>
          </instance-parameter>
          <parameter name="spacing" transfer-ownership="none">
            <type name="guint" c:type="guint"/>
          </parameter>
        </parameters>
      </method>
      <property name="icon-name" writable="1" setter="set_from_icon_name">
        <type name="utf8" c:type="gchar*"/>
      </property>
      <property name="column-spacing" writable="1" setter="set_column_spacing">
        <type name="gint" c:type="gint"/>
      </property>
      <property name="file" writable="1">
        <type name="utf8" c:type="gchar*"/>
      </property>
      <property name="orientation" writable="1" setter="set_missing">
        <type name="Orientation" c:type="GtkOrientation"/>
      </property>
      <property name="artists" writable="1">
        <array c:type="char**">
          <type name="utf8" c:type="char*"/>
        </array>
      </property>
    </class>
  </namespace>
</repository>`

// TestParseGIR_SetterIsRead pins that a property's setter is the C function
// GIR names it to be. gtk_image_set_from_icon_name is the standing example of
// a setter no rule over the class and property names produces.
func TestParseGIR_SetterIsRead(t *testing.T) {
	reg, err := gir.ParseGIRBytes([]byte(setterGIR))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	props := map[string]gir.Prop{}
	for _, p := range reg.ByCType["GtkImage"].Props {
		props[p.Name] = p
	}
	if got := props["icon-name"].Setter; got != "gtk_image_set_from_icon_name" {
		t.Errorf("icon-name setter = %q; want gtk_image_set_from_icon_name", got)
	}
	// The setter's value parameter, not the property, is what a call has to
	// satisfy — here a guint against a gint property.
	if got := props["column-spacing"].SetterCType; got != "guint" {
		t.Errorf("column-spacing setter value C type = %q; want guint", got)
	}
	// A property GIR names no setter for has none, and one whose reference
	// resolves to no method has none either.
	if got := props["file"].Setter; got != "" {
		t.Errorf("file setter = %q; want none", got)
	}
	if got := props["orientation"].Setter; got != "" {
		t.Errorf("orientation setter = %q; want none — set_missing is not declared", got)
	}
	// An array property's inner <type> names its element, so the property
	// keeps no GIR type that could be read as that element.
	if got := props["artists"].GIRType; got != "" {
		t.Errorf("artists GIR type = %q; want none", got)
	}
}

// TestParseGIR_Enumerations pins the member table an enum-typed property is
// set through: a SNGL string names a member, and the C identifier is what the
// emitted call carries.
func TestParseGIR_Enumerations(t *testing.T) {
	reg, err := gir.ParseGIRBytes([]byte(setterGIR))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	e := reg.Enums["Orientation"]
	if e == nil {
		t.Fatal("Orientation enumeration not captured")
	}
	if e.CType != "GtkOrientation" {
		t.Errorf("CType = %q; want GtkOrientation", e.CType)
	}
	if got := e.Members["vertical"]; got != "GTK_ORIENTATION_VERTICAL" {
		t.Errorf("Members[vertical] = %q; want GTK_ORIENTATION_VERTICAL", got)
	}
}
