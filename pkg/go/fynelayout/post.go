package fynelayout

import "fyne.io/fyne/v2"

// Post runs f on the goroutine that owns the widgets. A test build points it
// at the queue a test's wait drains, since Fyne's test driver runs fyne.Do on
// the goroutine that calls it: a scroll looking again for its end measured
// labels a test was rebuilding, and the Pokédex's Moves tab segfaulted.
var Post = fyne.Do
