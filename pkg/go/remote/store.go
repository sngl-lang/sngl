package remote

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Store holds one box per (query, argument tuple). It is what makes two parts
// of a program asking the same question share a box and a single fetch.
//
// The generated program uses Default, a package-level store, because sharing is
// the point: a box keyed to a component could not be shared with anything. A
// test that wants isolation builds its own with New.
type Store struct {
	mu    sync.Mutex
	boxes map[string]any // key -> *Value[T], for whichever T that query answers
	// notify is called after any box in this store settles, which is how a
	// settled fetch reaches the screen. Read-triggered fetching means the
	// render that started the fetch has already finished by the time an answer
	// arrives, so something has to ask for another one.
	notify func()
}

// New returns an empty store.
func New() *Store { return &Store{boxes: map[string]any{}} }

// Default is the store a generated program uses. Package-level because a box
// keyed to one component could not be shared with another asking the same
// question, and sharing is what a store is for.
var Default = New()

// OnSettle registers the callback a store invokes after any of its boxes
// settles. The generated entry point installs one that re-runs the updaters.
//
// One callback, not one per box: what a platform does with the news is re-read
// whatever depends on it, and it has no cheaper answer for a particular box.
func (s *Store) OnSettle(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notify = f
}

// Query returns the box for a query id and argument tuple, starting the fetch
// if no attempt has been made for it.
//
// Read-triggered: the read is what starts the work. That is the whole reason
// the design does not hang a kicker off each reactive dependency — a key is
// routinely a loop variable or some other derived value, which has no setter
// for a kicker to watch, so there would be nothing to hang it on.
//
// Idempotent, which is what makes it safe in a prop expression evaluated during
// render: a second call with the same key returns the same box and starts
// nothing.
func Query[T any](s *Store, id string, args []any, fetch func() (T, error)) *Value[T] {
	if s == nil {
		s = Default
	}
	k := key(id, args)

	s.mu.Lock()
	if s.boxes == nil {
		s.boxes = map[string]any{}
	}
	box, ok := s.boxes[k].(*Value[T])
	if !ok {
		box = &Value[T]{}
		s.boxes[k] = box
	}
	s.mu.Unlock()

	// The box knows how to re-run itself, so Refresh needs nothing from the
	// caller and a retry button holds only the box.
	box.mu.Lock()
	if box.refresh == nil {
		box.refresh = func() { run(s, box, fetch) }
	}
	started := box.value != nil || box.failure != nil || box.inFlight
	box.mu.Unlock()

	if !started {
		run(s, box, fetch)
	}
	return box
}

// run performs one attempt, unless one is already in flight.
func run[T any](s *Store, box *Value[T], fetch func() (T, error)) {
	if !box.begin() {
		return
	}
	go func() {
		got, err := fetch()
		if err != nil {
			box.settle(nil, asFailure(err))
		} else {
			box.settle(&got, nil)
		}
		s.mu.Lock()
		notify := s.notify
		s.mu.Unlock()
		if notify != nil {
			notify()
		}
	}()
}

// asFailure classifies an error a fetch returned. An adapter that knows what
// went wrong says so by returning a Failure; anything else is a transport
// failure, which is the honest default for an error with no other information
// in it.
func asFailure(err error) *Failure {
	var f Failure
	switch e := err.(type) {
	case Failure:
		f = e
	case *Failure:
		f = *e
	default:
		f = Failure{Kind: Transport, Message: err.Error()}
	}
	return &f
}

// key encodes a query id and its arguments into the string a box is stored
// under. Two calls with equal arguments must produce one key, so the encoding
// walks values rather than hashing addresses — which is also why a query's
// arguments have to be key-encodable, enforced over the key the adapter built.
//
// Length-prefixed rather than delimited: ("a", "b") and ("a|b") are different
// keys, and a separator alone cannot tell them apart.
func key(id string, args []any) string {
	var b strings.Builder
	b.WriteString(id)
	for _, a := range args {
		b.WriteByte('\x1f')
		writeKey(&b, a)
	}
	return b.String()
}

func writeKey(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("~")
	case string:
		fmt.Fprintf(b, "%d:%s", len(x), x)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeKey(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		// Sorted, so two equal maps built in different orders are one key.
		ks := make([]string, 0, len(x))
		for k := range x {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		b.WriteByte('{')
		for i, k := range ks {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(b, "%d:%s=", len(k), k)
			writeKey(b, x[k])
		}
		b.WriteByte('}')
	default:
		// %v is enough for the scalars a key can hold, and a struct of them
		// prints field by field. A type a key cannot hold never reaches here:
		// the Query lowering rejects it where the key is built.
		fmt.Fprintf(b, "%v", x)
	}
}
