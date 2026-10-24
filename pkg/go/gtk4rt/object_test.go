//go:build !js

package gtk4rt

import "testing"

func TestObject_MakesSetsAndAddsByName(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()

	check := New("GtkCheckButton")
	SetBool(check, "active", true)
	if !CheckButtonGetActive(check) {
		t.Error(`SetBool(check, "active", true) left it inactive`)
	}

	entry := New("GtkEntry")
	SetString(entry, "text", "typed")
	if got := EditableGetText(entry); got != "typed" {
		t.Errorf(`SetString(entry, "text") reads back %q`, got)
	}

	// A progress bar is no class gtk4rt wraps: its type is registered by name.
	bar := New("GtkProgressBar")
	SetFloat(bar, "fraction", 0.5)
	// PANGO_ELLIPSIZE_END: an enumeration takes its member's value.
	SetInt(bar, "ellipsize", 3)

	frame := New("GtkFrame")
	ChildCall("gtk_frame_set_child", frame, bar)
	if ParentOf(bar) != frame {
		t.Error(`ChildCall("gtk_frame_set_child") did not put the bar in the frame`)
	}
	stack := New("GtkStack")
	label := New("GtkLabel")
	ChildCall("gtk_stack_add_child", stack, label)
	if ParentOf(label) != stack {
		t.Error(`ChildCall("gtk_stack_add_child") did not put the label in the stack`)
	}
	Retain(label)
	defer Release(label)
	ChildCall("gtk_stack_remove", stack, label)
	if ParentOf(label) != nil {
		t.Error(`ChildCall("gtk_stack_remove") left the label in the stack`)
	}
}
