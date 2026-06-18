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
// widget (menu / tree / select). It uses the default delegate, sizes the list
// (a zero-sized list renders nothing), and strips the heavy chrome the bubbles
// list shows by default — title, status bar, help footer, pagination, and
// filtering — so the result is a clean scrollable list of rows. Callers that
// want any of that back can flip the corresponding setter afterward.
func NewList(items []list.Item, w, h int) list.Model {
	m := list.New(items, list.NewDefaultDelegate(), w, h)
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
// NOTE: SNGL's `rows dyn` prop lowers to Go `any`. The concrete shape the
// codegen passes here is not yet pinned down (E2+ converts the `table`
// component); [][]string is the natural cell-grid representation and what the
// converter helper is expected to produce. If the realized lowering differs
// (e.g. []any of records), this signature will need a companion accepting that
// shape.
func Rows(rows [][]string) []table.Row {
	out := make([]table.Row, len(rows))
	for i, r := range rows {
		row := make(table.Row, len(r))
		copy(row, r)
		out[i] = row
	}
	return out
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
