package fynelayout

import (
	"testing"

	fynetest "fyne.io/fyne/v2/test"
)

// A window's main menu is the entries among its content: a menu per top-level
// entry holding its submenu, the checked one checked, a disabled one disabled,
// and picking one runs its handler. An entry moved after the window is made
// -- the render slot of a loop over state inserting one -- and a changed check
// make the menu again.
func TestAWindowsMenuIsItsEntries(t *testing.T) {
	app = fynetest.NewTempApp(t)
	top := NewToplevel()
	picked := ""
	entry := func(label string, checked, disabled bool, kids ...*MenuEntry) *MenuEntry {
		e := NewMenuEntry()
		e.SetLabel(label)
		e.SetChecked(checked)
		e.SetDisabled(disabled)
		e.OnSelect = func() { picked = label }
		for _, k := range kids {
			e.Add(k)
		}
		return e
	}
	small := entry("Small", false, false)
	large := entry("Large", true, false)
	top.Add(entry("Size", false, false, small, large))
	top.Add(entry("Quit", false, true))
	top.create()

	menu := top.w.MainMenu()
	if menu == nil || len(menu.Items) != 2 {
		t.Fatalf("main menu = %v, want two menus", menu)
	}
	size := menu.Items[0]
	if size.Label != "Size" || len(size.Items) != 2 || size.Items[0].Checked || !size.Items[1].Checked {
		t.Errorf("Size menu = %+v, want Small then Large, Large checked", size.Items)
	}
	if quit := menu.Items[1]; len(quit.Items) != 1 || !quit.Items[0].Disabled {
		t.Errorf("Quit, an entry with no submenu, = %+v, want itself, disabled", quit.Items)
	}
	size.Items[0].Action()
	if picked != "Small" {
		t.Errorf("picking Small ran %q", picked)
	}

	small.SetChecked(true)
	large.SetChecked(false)
	if items := top.w.MainMenu().Items[0].Items; !items[0].Checked || items[1].Checked {
		t.Errorf("after the check moved, Size = %+v", items)
	}
	InsertBefore(top.Container, nil, entry("Help", false, false))
	if n := len(top.w.MainMenu().Items); n != 3 {
		t.Errorf("after an entry was inserted the menu has %d menus, want 3", n)
	}
}
