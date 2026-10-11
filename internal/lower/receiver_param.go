package lower

import "duckfam.us/sngl/ir"

// passReceiverParam makes the implicit `this` of a method on a generic
// built-in -- `func list<T>.pick() => this[n]` -- the method's first
// parameter. The checker keeps it off Params because such a body is written to
// be inlined, and every call already passes the receiver as Args[0]; one the
// optimizer left standing is emitted, and no target has another way to reach
// it. A call carrying its receiver beside it is given it as that argument.
//
// The program's own functions only: a library's is shared by every build, and
// none of those is emitted (passLibFuncs promotes no method).
var passReceiverParam = pass{
	name:    "ReceiverParam",
	enabled: func(Features) bool { return true },
	apply:   lowerReceiverParam,
}

func lowerReceiverParam(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	moved := map[*ir.Func]bool{}
	for _, owner := range ir.Owners(pkg) {
		for _, fn := range owner.Funcs {
			if fn != nil && fn.RecvParam != nil {
				fn.Params = append([]*ir.Param{fn.RecvParam}, fn.Params...)
				fn.RecvParam = nil
				moved[fn] = true
			}
		}
	}
	if len(moved) == 0 {
		return nil
	}
	return ir.Walk(pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && moved[c.Func] && c.Receiver != nil {
			c.Args = append([]ir.CallArg{{Value: c.Receiver}}, c.Args...)
			c.Receiver = nil
		}
		return nil
	})
}
