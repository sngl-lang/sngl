package snglasync

import (
	"context"
	"errors"
	"sync"
)

// A queue is a number, as a lifetime is. Open hands one out and it is held
// until its reader has read it closed and empty; a number not held, 0
// included, is a queue that is closed and empty.
type queue struct {
	items  []any
	closed bool
	ready  chan struct{}
}

var (
	qmu    sync.Mutex
	qnext  int
	queues = map[int]*queue{}
)

// OpenQueue is a new queue, empty and open.
func OpenQueue() int {
	qmu.Lock()
	defer qmu.Unlock()
	qnext++
	queues[qnext] = &queue{ready: make(chan struct{}, 1)}
	return qnext
}

// Send adds v to the end of id, answering false for a queue that is closed.
func Send(id int, v any) bool {
	qmu.Lock()
	defer qmu.Unlock()
	q := queues[id]
	if q == nil || q.closed {
		return false
	}
	q.items = append(q.items, v)
	q.wake()
	return true
}

// MustSend is Send where sending to a closed queue is a fault, as sending on
// a closed channel is.
func MustSend(id int, v any) {
	if !Send(id, v) {
		panic(ErrClosed)
	}
}

// ErrClosed is what sending to a closed queue fails with.
var ErrClosed = errors.New("send to a closed queue")

// CloseQueue says id holds no more than it has been sent. Closing one
// already closed does nothing.
func CloseQueue(id int) {
	qmu.Lock()
	defer qmu.Unlock()
	if q := queues[id]; q != nil {
		q.closed = true
		q.wake()
	}
}

// Receive waits for the next of id's items, answering false once it is closed
// and empty or ctx ends.
func Receive(ctx context.Context, id int) (any, bool) {
	for {
		qmu.Lock()
		q := queues[id]
		if q == nil {
			qmu.Unlock()
			return nil, false
		}
		if len(q.items) > 0 {
			v := q.items[0]
			q.items[0] = nil
			q.items = q.items[1:]
			qmu.Unlock()
			return v, true
		}
		if q.closed {
			delete(queues, id)
			qmu.Unlock()
			return nil, false
		}
		qmu.Unlock()
		select {
		case <-q.ready:
		case <-ctx.Done():
			return nil, false
		}
	}
}

func (q *queue) wake() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}
