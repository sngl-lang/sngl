package tui

import (
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/table"
)

// StringItem adapts a plain string to the bubbles list.Item interface (and the
// richer list.DefaultItem used by list.NewDefaultDelegate). Title and
// FilterValue both return the underlying string; Description is empty.
type StringItem string

// Title returns the item's display title (the underlying string).
func (s StringItem) Title() string { return string(s) }

// Description returns the item's secondary line (empty for plain strings).
func (s StringItem) Description() string { return "" }

// FilterValue returns the value used when filtering the list.
func (s StringItem) FilterValue() string { return string(s) }

// StringItems converts a slice of strings into a slice of list.Item suitable
// for list.New. Each string becomes a StringItem.
func StringItems(ss []string) []list.Item {
	out := make([]list.Item, len(ss))
	for i, s := range ss {
		out[i] = StringItem(s)
	}
	return out
}

// NewList builds a list.Model pre-configured for a simple SNGL list-backed
// widget (menu / tree / select). It uses a single-line default delegate
// (description hidden, zero inter-item spacing — SNGL list rows carry no
// description, so the default two-line item would waste a blank line per row),
// sizes the list (a zero-sized list renders nothing), and strips the heavy
// chrome the bubbles list shows by default — title, status bar, help footer,
// pagination, and filtering — so the result is a clean scrollable list of
// rows. Callers that want any of that back can flip the corresponding setter
// afterward.
func NewList(items []list.Item, w, h int) list.Model {
	d := list.NewDefaultDelegate()
	d.ShowDescription = false
	d.SetSpacing(0)
	m := list.New(items, d, w, h)
	m.SetShowTitle(false)
	m.SetShowStatusBar(false)
	m.SetShowHelp(false)
	m.SetShowPagination(false)
	m.SetShowFilter(false)
	m.SetFilteringEnabled(false)
	return m
}

// SelectedString returns the underlying string of the list's current
// selection. It is nil-safe: a list with no items (or no selection) has a nil
// SelectedItem, so this returns "" rather than panicking. The selected item is
// expected to be a StringItem (or any list.Item), and its FilterValue carries
// the row's string.
func SelectedString(m list.Model) string {
	it := m.SelectedItem()
	if it == nil {
		return ""
	}
	return it.FilterValue()
}

// Columns converts a slice of column names into table.Column values. Each
// column's width defaults to the header length plus padding so the title is
// always visible; callers can re-set widths afterwards if needed.
func Columns(names []string) []table.Column {
	out := make([]table.Column, len(names))
	for i, name := range names {
		out[i] = table.Column{Title: name, Width: len(name) + 2}
	}
	return out
}

// Rows converts row data into table.Row values. table.Row is a []string, so
// each input row is copied into a fresh table.Row.
//
// SNGL's `rows dyn` prop, given a list-of-list-of-string, lowers to Go
// [][]string (verified against generated bubbletea code), which matches this
// signature directly: the codegen emits `tui.Rows(<rows expr>)` with no
// intermediate conversion.
func Rows(rows [][]string) []table.Row {
	out := make([]table.Row, len(rows))
	for i, r := range rows {
		row := make(table.Row, len(r))
		copy(row, r)
		out[i] = row
	}
	return out
}

// NewTable builds a table.Model pre-configured for a SNGL `table` widget. It
// centralizes the bubbles table construction: columns, rows, a viewport height
// (a zero-height table renders no rows), a viewport width wide enough for the
// columns (the bubbles v2 table renders only the header row until its viewport
// has a non-zero width), and focus enabled so the table responds to
// cursor-movement keys when the SNGL focus engine routes input to it. Callers
// can re-set any of these via the table.Model setters afterward.
func NewTable(cols []table.Column, rows []table.Row, height int) table.Model {
	width := tableWidth(cols)
	return table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithHeight(height),
		table.WithWidth(width),
		table.WithFocused(true),
	)
}

// tableWidth sums the column widths plus inter-column padding so the table's
// viewport is wide enough to show every column (and therefore its rows). A
// table with no columns still gets a minimum width so an empty table renders a
// visible (if blank) frame rather than collapsing to nothing.
func tableWidth(cols []table.Column) int {
	const colPadding = 2 // matches the per-column cell padding the table adds
	w := 0
	for _, c := range cols {
		w += c.Width + colPadding
	}
	if w < 1 {
		w = 1
	}
	return w
}

// Cursor returns the table's current 0-based cursor row, for read-back into a
// SNGL `selected int` bind. It mirrors table.Model.Cursor so the codegen can
// emit a `tui.`-prefixed getter consistent with the other widget helpers.
func Cursor(m table.Model) int {
	return m.Cursor()
}

// Percent computes value/max as a fraction in [0, 1] for progress.Model.ViewAs.
// It guards against a zero (or negative) max — a div-by-zero would otherwise
// yield NaN/Inf and corrupt the rendered bar — returning 0 in that case. The
// result is not clamped at the top: callers passing value > max get a fraction
// above 1.0, which ViewAs renders as a full bar.
func Percent(value, max float64) float64 {
	if max <= 0 {
		return 0
	}
	return value / max
}
