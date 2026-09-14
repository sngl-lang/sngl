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
)

// Schedule is one armed ticker. fyne.sngl names it as an opaque SNGL type, so
// the caller holds the schedule itself rather than an index into a registry
// here -- which is what this package used to be, and the reason it existed.
type Schedule struct {
	ticker *time.Ticker
	once   sync.Once
	done   chan struct{}
}

// Every runs fn on Fyne's own thread every ms milliseconds until the returned
// schedule is cancelled. A period of zero or less arms nothing and answers nil,
// which Cancel accepts.
func Every(ms int, fn func()) *Schedule {
	if ms <= 0 {
		return nil
	}
	s := &Schedule{
		ticker: time.NewTicker(time.Duration(ms) * time.Millisecond),
		done:   make(chan struct{}),
	}
	go func() {
		for {
			select {
			case <-s.ticker.C:
				fyneDo(fn)
			case <-s.done:
				return
			}
		}
	}()
	return s
}

// Cancel stops the schedule. A nil receiver and a second call are both no-ops,
// so an unmount need not track whether a mount ever armed one.
//
// The done channel is what ends the goroutine: Ticker.Stop does not close C, so
// a receive on it alone would block for the life of the process.
func (s *Schedule) Cancel() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.ticker.Stop()
		close(s.done)
	})
}
