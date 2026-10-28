package fynelayout

import (
	"slices"
	"testing"

	fynetest "fyne.io/fyne/v2/test"
)

func tabPage(title string, shown *[]string) *TabPage {
	p := NewTabPage()
	p.SetTitle(title)
	p.OnChange = func() { *shown = append(*shown, title) }
	return p
}

func titles(t *Tabs) []string {
	var out []string
	for _, it := range t.Bar.Items {
		out = append(out, it.Text)
	}
	return out
}

// The pages a render slot inserts into the tabs, and removes, are the
// AppTabs' items in their order, a disabled one disabled, and the page
// selected before any came is the one shown once it does. The tabs draw the
// AppTabs alone, the pages' box being no widget a window walks into.
func TestTabsAreTheirPages(t *testing.T) {
	fynetest.NewTempApp(t)
	var shown []string
	tabs := NewTabs()
	tabs.SetSelected(2)
	one, two, three := tabPage("One", &shown), tabPage("Two", &shown), tabPage("Three", &shown)
	two.SetDisabled(true)
	tabs.Add(one)
	at := SlotAnchor(tabs.Container, nil)
	InsertBefore(tabs.Container, at, two)
	InsertBefore(tabs.Container, at, three)

	if got := titles(tabs); !slices.Equal(got, []string{"One", "Two", "Three"}) {
		t.Errorf("titles %q", got)
	}
	if !tabs.Bar.Items[1].Disabled() || tabs.Bar.Items[0].Disabled() {
		t.Error("the disabled page's item is not the disabled one")
	}
	if tabs.Bar.Selected() != three.item {
		t.Errorf("shown %v, not the page selected before it came", tabs.Bar.Selected())
	}
	if len(shown) != 0 {
		t.Errorf("the program's own selection fired %q", shown)
	}
	if one.Visible() {
		t.Error("a page is drawn among the box's objects")
	}
	if objs := fynetest.TempWidgetRenderer(t, tabs).Objects(); len(objs) != 1 || objs[0] != tabs.Bar {
		t.Errorf("the tabs draw %v, not their AppTabs alone", objs)
	}

	Remove(tabs.Container, two)
	if got := titles(tabs); !slices.Equal(got, []string{"One", "Three"}) {
		t.Errorf("after remove: titles %q", got)
	}
	one.SetTitle("Uno")
	if got := titles(tabs); !slices.Equal(got, []string{"Uno", "Three"}) {
		t.Errorf("after a new title: titles %q", got)
	}
}

// The user choosing a tab fires its page's OnChange once; the program's write
// of `selected` fires nothing.
func TestTabsReportTheUsersChoiceAlone(t *testing.T) {
	fynetest.NewTempApp(t)
	var shown []string
	tabs := NewTabs()
	one, two := tabPage("One", &shown), tabPage("Two", &shown)
	tabs.Add(one)
	tabs.Add(two)

	tabs.Bar.Select(two.item)
	tabs.SetSelected(1)
	if !slices.Equal(shown, []string{"Two"}) {
		t.Errorf("shown %q", shown)
	}
	tabs.SetSelected(0)
	if tabs.Bar.SelectedIndex() != 0 || !slices.Equal(shown, []string{"Two"}) {
		t.Errorf("the program's write: index %d, shown %q", tabs.Bar.SelectedIndex(), shown)
	}
}
