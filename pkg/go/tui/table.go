package tui

import (
	"slices"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
)

// Table is a table's headings and rows over bubbles' table. Both arrive with
// each view and update, as a Select's choices do: a program may change either
// while the widget stands -- rows that are a request's answer arrive after
// it is built.
type Table struct {
	table   table.Model
	columns []string
	rows    [][]string
}

// NewTable builds a Table showing `height` rows at a time, with no headings
// and no rows, focused so that it moves its cursor when SNGL's focus routes
// keys to it.
func NewTable(height int) Table {
	return Table{table: table.New(table.WithHeight(height), table.WithFocused(true))}
}

// set shows columns and rows in place of those before. Showing the same again
// does nothing, so it is cheap to call on every frame.
func (t *Table) set(columns []string, rows [][]string) {
	if slices.Equal(t.columns, columns) && slices.EqualFunc(t.rows, rows, slices.Equal) {
		return
	}
	t.columns = slices.Clone(columns)
	t.rows = make([][]string, len(rows))
	for i, r := range rows {
		t.rows[i] = slices.Clone(r)
	}
	cols := Columns(columns)
	for _, r := range rows {
		for i, cell := range r {
			if i < len(cols) && len(cell)+2 > cols[i].Width {
				cols[i].Width = len(cell) + 2
			}
		}
	}
	// The rows go first: bubbles renders each row against the columns it
	// holds, and a row wider than the new columns would index past them.
	t.table.SetRows(nil)
	t.table.SetColumns(cols)
	t.table.SetRows(Rows(rows))
	if t.table.Width() < tableWidth(cols) {
		t.table.SetWidth(tableWidth(cols))
	}
}

// Update shows columns and rows, then moves the cursor as bubbles' table does.
func (t Table) Update(msg tea.Msg, columns []string, rows [][]string) (Table, tea.Cmd) {
	t.set(columns, rows)
	var cmd tea.Cmd
	t.table, cmd = t.table.Update(msg)
	return t, cmd
}

// View draws columns and rows.
func (t Table) View(columns []string, rows [][]string) string {
	t.set(columns, rows)
	return t.table.View()
}

// Cursor is the 0-based row the cursor is on, for a SNGL `selected int` bind.
func (t Table) Cursor() int { return t.table.Cursor() }

// SetWidth sizes the table to the terminal's width.
func (t *Table) SetWidth(w int) { t.table.SetWidth(w) }
