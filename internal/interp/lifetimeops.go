package interp

import (
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/pkg/go/snglasync"
)

// runLifetimeIntrinsic answers sngl:async's lifetime and queue ids, which
// snglasync holds; handled is false for any other id.
func runLifetimeIntrinsic(fn *ir.Func, args []any) (any, bool, error) {
	if fn == nil {
		return nil, false, nil
	}
	id := func() int {
		if len(args) == 1 {
			n, _ := args[0].(int)
			return n
		}
		return 0
	}
	switch fn.Intrinsic {
	case "async.begin":
		return snglasync.Begin(), true, nil
	case "async.end":
		snglasync.End(id())
		return nil, true, nil
	case "async.ended":
		return snglasync.Ended(id()), true, nil
	case "async.queueOpen":
		return snglasync.OpenQueue(), true, nil
	case "async.queueSend":
		n, _ := args[0].(int)
		if !snglasync.Send(n, args[1]) {
			return nil, true, snglasync.ErrClosed
		}
		return nil, true, nil
	case "async.queueClose":
		snglasync.CloseQueue(id())
		return nil, true, nil
	}
	return nil, false, nil
}

// heldID is the number a Lifetime or a queue holds, 0 -- forever, or a queue
// closed and empty -- for anything else.
func heldID(v any) int {
	s, ok := v.(*Struct)
	if !ok {
		return 0
	}
	id, _ := s.Get("_id")
	n, _ := id.(int)
	return n
}
