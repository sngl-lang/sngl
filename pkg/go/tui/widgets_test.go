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

func TestPercent(t *testing.T) {
	tests := []struct {
		name       string
		value, max float64
		want       float64
	}{
		{"zero max guards div-by-zero", 50, 0, 0},
		{"negative max guards", 50, -10, 0},
		{"half", 5, 10, 0.5},
		{"over 100 percent not clamped", 15, 10, 1.5},
		{"zero value", 0, 10, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Percent(tt.value, tt.max); got != tt.want {
				t.Errorf("Percent(%v, %v) = %v, want %v", tt.value, tt.max, got, tt.want)
			}
		})
	}
}

func TestSelectedStringEmpty(t *testing.T) {
	// A list with no items has a nil SelectedItem; SelectedString must not
	// panic and must return "".
	m := NewList(StringItems(nil), 40, 10)
	if got := SelectedString(m); got != "" {
		t.Errorf("SelectedString(empty list) = %q, want %q", got, "")
	}
}

func TestCursorFreshTable(t *testing.T) {
	// A freshly constructed table starts with its cursor at row 0.
	m := NewTable(Columns([]string{"Name", "ID"}), Rows([][]string{{"a", "1"}}), 10)
	if got := Cursor(m); got != 0 {
		t.Errorf("Cursor(fresh table) = %d, want 0", got)
	}
}

func TestTableWidth(t *testing.T) {
	tests := []struct {
		name string
		cols []table.Column
		want int
	}{
		{
			// Columns(["Name","ID"]) yields widths 6 and 4 (len+2); tableWidth
			// adds 2 padding each → (6+2)+(4+2) = 14.
			name: "sums column widths plus padding",
			cols: Columns([]string{"Name", "ID"}),
			want: 14,
		},
		{
			name: "single column",
			cols: []table.Column{{Title: "X", Width: 3}},
			want: 5,
		},
		{
			name: "no columns clamps to min width 1",
			cols: nil,
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tableWidth(tt.cols); got != tt.want {
				t.Errorf("tableWidth = %d, want %d", got, tt.want)
			}
		})
	}
}
