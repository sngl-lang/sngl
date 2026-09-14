// Package fynert is the schedule behind fyne's `time.timer`.
//
// It exists because Fyne offers no start/stop pair of its own: a ticker is an
// ordinary goroutine, and Fyne owns the goroutine its loop runs on, so a tick
// has to be handed over before it touches a widget. That hand-over is the whole
// of this package.
package fynert

import (
	"sync"
	"time"

	fyne "fyne.io/fyne/v2"
)

type schedule struct {
	ticker *time.Ticker
	done   chan struct{}
}

var (
	mu      sync.Mutex
	next    int
	running = map[int]*schedule{}
)

// Every runs fn on Fyne's own thread every ms milliseconds until the returned
// id is passed to CancelEvery.
//
// The id is an int rather than the ticker itself because the caller is a SNGL
// `var handle = 0`: this pair is named by a `#[go.native]` declaration in
// fyne.sngl, and `int` is the only integer type that declaration can spell.
func Every(ms int, fn func()) int {
	if ms <= 0 {
		return 0
	}
	s := &schedule{ticker: time.NewTicker(time.Duration(ms) * time.Millisecond), done: make(chan struct{})}
	mu.Lock()
	next++
	id := next
	running[id] = s
	mu.Unlock()
	go func() {
		for {
			select {
			case <-s.ticker.C:
				fyne.Do(fn)
			case <-s.done:
				return
			}
		}
	}()
	return id
}

// CancelEvery stops a schedule Every armed. Zero is accepted and does nothing,
// so a caller need not track whether it ever armed one.
//
// The done channel is what ends the goroutine: Ticker.Stop does not close C, so
// a receive on it alone would block for the life of the process.
func CancelEvery(id int) {
	mu.Lock()
	s := running[id]
	delete(running, id)
	mu.Unlock()
	if s == nil {
		return
	}
	s.ticker.Stop()
	close(s.done)
}
