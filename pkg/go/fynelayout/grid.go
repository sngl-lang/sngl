package fynelayout

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/layout"
)

// SetGridColumns lays c's children out n to a row, as a container
// container.NewGridWithColumns(n) built does. The count is that constructor's
// argument and fyne's Container has no setter for it, so a grid whose count is
// read from state -- a table's columns -- kept the count it was built with.
func SetGridColumns(c *fyne.Container, n int) {
	c.Layout = layout.NewGridLayoutWithColumns(n)
	c.Refresh()
}
