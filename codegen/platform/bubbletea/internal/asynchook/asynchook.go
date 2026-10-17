// Package asynchook is what an emitted program's run test hands work it
// started off the event loop, so the driver can say when the work goes on
// and hear what it read, both with a happens-before edge the race detector
// sees.
package asynchook

var (
	release  = make(chan struct{})
	reported = make(chan int, 1)
)

// Wait blocks until Release.
func Wait() { <-release }

// Release lets every Wait go on.
func Release() { close(release) }

// Report hands v to Reported.
func Report(v int) { reported <- v }

// Reported is what Report was handed.
func Reported() <-chan int { return reported }
