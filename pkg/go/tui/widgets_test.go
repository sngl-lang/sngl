package tui

import (
	"reflect"
	"testing"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/table"
)

func TestStringItem(t *testing.T) {
	var it list.Item = StringItem("hello")
	if it.FilterValue() != "hello" {
		t.Errorf("FilterValue = %q, want %q", it.FilterValue(), "hello")
	}
	di, ok := it.(list.DefaultItem)
	if !ok {
		t.Fatalf("StringItem does not satisfy list.DefaultItem")
	}
	if di.Title() != "hello" {
		t.Errorf("Title = %q, want %q", di.Title(), "hello")
	}
	if di.Description() != "" {
		t.Errorf("Description = %q, want empty", di.Description())
	}
}

func TestStringItems(t *testing.T) {
	got := StringItems([]string{"a", "b", "c"})
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i].FilterValue() != want {
			t.Errorf("item %d = %q, want %q", i, got[i].FilterValue(), want)
		}
	}
	// Empty input yields an empty (non-nil) slice.
	if got := StringItems(nil); got == nil || len(got) != 0 {
		t.Errorf("StringItems(nil) = %v, want empty slice", got)
	}
}

func TestColumns(t *testing.T) {
	got := Columns([]string{"Name", "ID"})
	want := []table.Column{
		{Title: "Name", Width: 6},
		{Title: "ID", Width: 4},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Columns = %+v, want %+v", got, want)
	}
}

func TestRows(t *testing.T) {
	got := Rows([][]string{{"a", "1"}, {"b", "2"}})
	want := []table.Row{{"a", "1"}, {"b", "2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Rows = %+v, want %+v", got, want)
	}
	if got := Rows(nil); got == nil || len(got) != 0 {
		t.Errorf("Rows(nil) = %v, want empty slice", got)
	}
}
