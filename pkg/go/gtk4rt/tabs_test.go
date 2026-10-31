//go:build !js

package gtk4rt

import (
	"slices"
	"testing"
)

func page(title string, shown *[]string) Handle {
	h := TabPageNew()
	TabPageSetTitle(h, title)
	TabPageOnShown(h, func() { *shown = append(*shown, title) })
	return h
}

// Pages reach the stack however a render slot hands them to the tabs, and the
// switcher shows a button per page in order, a disabled one insensitive.
func TestTabsHoldTheirPagesInTheStack(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	var shown []string
	tabs := TabsNew()
	TabsSetSelected(tabs, 2)
	one, two := page("One", &shown), TabPageNew()
	BoxAppend(tabs, one)
	at := SlotAnchor(tabs, nil)
	InsertBefore(tabs, at, two)
	TabPageSetTitle(two, "Two")
	TabPageSetDisabled(two, true)
	three := page("Three", &shown)
	InsertBefore(tabs, at, three)

	tb := tabsOf[tabs]
	if got := tabsTitles(tb); !slices.Equal(got, []string{"One", "Two", "Three"}) {
		t.Errorf("titles %q", got)
	}
	if got := tabsSensitive(tb); !slices.Equal(got, []bool{true, false, true}) {
		t.Errorf("sensitive %v", got)
	}
	if got := tabsShown(tb); got != three {
		t.Errorf("shown is not the page selected before it came")
	}
	if len(shown) != 0 {
		t.Errorf("the program's own selection fired %q", shown)
	}

	BoxRemove(tabs, two)
	if got := tabsTitles(tb); !slices.Equal(got, []string{"One", "Three"}) {
		t.Errorf("after remove: titles %q", got)
	}
	if got := tabsSensitive(tb); !slices.Equal(got, []bool{true, true}) {
		t.Errorf("after remove: sensitive %v", got)
	}
}

// The user choosing a page fires its @shown once; the program's write of
// `selected` that follows, and any other, fires nothing, and a disabled page
// is not shown.
func TestTabsReportTheUsersChoiceAlone(t *testing.T) {
	if !hasDisplay() {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	Init()
	var shown []string
	tabs := TabsNew()
	one, two, off := page("One", &shown), page("Two", &shown), page("Off", &shown)
	TabPageSetDisabled(off, true)
	for _, p := range []Handle{one, two, off} {
		BoxAppend(tabs, p)
	}
	tb := tabsOf[tabs]

	tabsChoose(tb, two)
	TabsSetSelected(tabs, 1)
	if !slices.Equal(shown, []string{"Two"}) {
		t.Errorf("shown %q", shown)
	}
	TabsSetSelected(tabs, 0)
	if tabsShown(tb) != one || !slices.Equal(shown, []string{"Two"}) {
		t.Errorf("the program's write: shown %q", shown)
	}
	tabsChoose(tb, off)
	if tabsShown(tb) != one || !slices.Equal(shown, []string{"Two"}) {
		t.Errorf("a disabled page was chosen: shown %q", shown)
	}
}
