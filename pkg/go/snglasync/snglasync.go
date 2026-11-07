// Package snglasync is the runtime behind sngl:async's Lifetime for a program
// compiled to Go, and for the interpreter.
//
// A lifetime is a number. 0 is `forever`, which never ends; Begin hands out
// the others in order and holds each until End, so one no longer held, and
// not 0, has ended. A request is made under Context(id), which End cancels.
package snglasync

import (
	"context"
	"sync"
)

var (
	mu   sync.Mutex
	next int
	live = map[int]context.CancelFunc{}
	ctxs = map[int]context.Context{}
)

// Begin is a new lifetime, not yet ended.
func Begin() int {
	mu.Lock()
	defer mu.Unlock()
	next++
	ctx, cancel := context.WithCancel(context.Background())
	live[next], ctxs[next] = cancel, ctx
	return next
}

// End ends id: every request made under its context stops. Ending one already
// ended, or 0, does nothing.
func End(id int) {
	mu.Lock()
	cancel, ok := live[id]
	delete(live, id)
	delete(ctxs, id)
	mu.Unlock()
	if ok {
		cancel()
	}
}

// Ended reports whether id has ended.
func Ended(id int) bool {
	if id == 0 {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	_, ok := live[id]
	return !ok
}

// Context is what a request made under id is made in: one that never ends for
// 0, an ended one for a lifetime that has ended.
func Context(id int) context.Context {
	if id == 0 {
		return context.Background()
	}
	mu.Lock()
	defer mu.Unlock()
	if ctx, ok := ctxs[id]; ok {
		return ctx
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
