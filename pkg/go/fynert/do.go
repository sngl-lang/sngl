package fynert

import fyne "fyne.io/fyne/v2"

// fyneDo is the hand-over, named once so the test can see the tick without a
// driver: a widget touched from anywhere but Fyne's own goroutine is a race.
var fyneDo = fyne.Do
