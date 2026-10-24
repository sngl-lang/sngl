package lower

import (
	"fmt"

	"duckfam.us/sngl/ir"
)

// passCMarshal converts each argument of a C call through the sngl:x/c
// conversion its parameter's type names: `showText(ctx, s)` becomes
// `showText(ctx, c.cstring(s))`. A parameter of any other type -- an opaque
// handle, a typedef'd enum -- is handed over as it is. See design/ownership.md.
var passCMarshal = pass{
	name:    "CMarshal",
	enabled: func(Features) bool { return true },
	apply:   func(pkg *ir.Package, _ Features, _ Options) error { return CMarshal(pkg) },
}

// CMarshal is passCMarshal for the interpreter simulating a target.
//
// Every body the program reaches is rewritten, not only the package's own: on
// the interpreter a library declaration is called where it is declared. A
// frozen declaration is one the program does not reach (ir.ClonePackageFor).
func CMarshal(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	m := &cMarshal{done: map[*ir.Call]bool{}, walked: map[*ir.Func]bool{}, indexed: map[*ir.Package]bool{}}
	m.index(pkg)
	if err := m.walk(pkg); err != nil {
		return err
	}
	seen := map[*ir.Package]bool{pkg: true}
	var visit func(p *ir.Package) error
	visit = func(p *ir.Package) error {
		for _, imp := range p.Imports {
			if imp == nil || imp.Pkg == nil || seen[imp.Pkg] {
				continue
			}
			seen[imp.Pkg] = true
			for _, o := range ir.Owners(imp.Pkg) {
				if o.Comp != nil && !o.Comp.Frozen {
					if err := m.walk(o.Comp); err != nil {
						return err
					}
				}
				for _, fn := range o.Funcs {
					if err := m.walkFunc(fn); err != nil {
						return err
					}
				}
			}
			if err := visit(imp.Pkg); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(pkg); err != nil {
		return err
	}
	for len(m.queue) > 0 {
		fn := m.queue[0]
		m.queue = m.queue[1:]
		if err := m.walk(fn); err != nil {
			return err
		}
	}
	return nil
}

type cMarshal struct {
	// done is each C call already converted: a body spliced without a deep
	// copy shares its nodes with the declaration it came from.
	done map[*ir.Call]bool
	// queue is the bodied functions a call leads to, which on the
	// interpreter no package list names.
	walked  map[*ir.Func]bool
	queue   []*ir.Func
	conv    map[ir.BuiltinKind]*ir.Func
	indexed map[*ir.Package]bool
}

func (m *cMarshal) index(p *ir.Package) {
	if p == nil || m.indexed[p] {
		return
	}
	m.indexed[p] = true
	for _, fn := range p.Funcs {
		if fn.Builtin.IsCMarshal() {
			if m.conv == nil {
				m.conv = map[ir.BuiltinKind]*ir.Func{}
			}
			if m.conv[fn.Builtin] == nil {
				m.conv[fn.Builtin] = fn
			}
		}
	}
	for _, imp := range p.Imports {
		if imp != nil {
			m.index(imp.Pkg)
		}
	}
}

func (m *cMarshal) walkFunc(fn *ir.Func) error {
	if fn == nil || fn.Frozen || fn.Foreign.Name != "" || m.walked[fn] {
		return nil
	}
	m.walked[fn] = true
	return m.walk(fn)
}

func (m *cMarshal) walk(root any) error {
	return ir.Walk(root, func(n ir.Node) error {
		call, ok := n.(*ir.Call)
		if !ok || call.Func == nil {
			return nil
		}
		if fn := call.Func; !fn.Frozen && fn.Foreign.Name == "" && !m.walked[fn] {
			m.walked[fn] = true
			m.queue = append(m.queue, fn)
		}
		return m.call(call)
	})
}

// marshalSpliced converts the C calls of an expression a pass splices after
// passCMarshal ran.
func marshalSpliced(e ir.Expr) error {
	m := &cMarshal{done: map[*ir.Call]bool{}, walked: map[*ir.Func]bool{}, indexed: map[*ir.Package]bool{}}
	return ir.Walk(e, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && c.Func != nil {
			return m.call(c)
		}
		return nil
	})
}

// call converts each argument of c, when c calls a C function other than a
// conversion. Aligned from the end: a native in the receiver-first shape is
// called with the receiver as argument zero, which no parameter declares.
func (m *cMarshal) call(c *ir.Call) error {
	fn := c.Func
	if fn.Foreign.Path != "C" || fn.Builtin.IsCMarshal() || m.done[c] {
		return nil
	}
	m.done[c] = true
	off := len(c.Args) - len(fn.Params)
	if off < 0 {
		return nil
	}
	for i, p := range fn.Params {
		kind := marshalKind(p.Type)
		if kind == ir.BuiltinNone {
			continue
		}
		m.index(fn.Pkg)
		conv := m.conv[kind]
		if conv == nil {
			return fmt.Errorf("C function %q takes a %s, and no package it or the program imports declares #[builtin(%q)], which sngl:x/c does", fn.Foreign.Name, p.Type, string(kind))
		}
		arg := &c.Args[off+i]
		// Converted already: a body CMarshal rewrote in place and a pass then
		// spliced is walked again with the splice.
		if t := arg.Value.ExprType(); t != nil && marshalKind(t) != kind {
			continue
		}
		arg.Value = convertArg(conv, arg.Value)
	}
	return nil
}

func marshalKind(t *ir.Type) ir.BuiltinKind {
	if t == nil {
		return ir.BuiltinNone
	}
	switch t.Kind {
	case ir.TypeString:
		return ir.BuiltinCString
	case ir.TypeInt:
		return ir.BuiltinCInt
	case ir.TypeFloat:
		return ir.BuiltinCDouble
	case ir.TypeBool:
		return ir.BuiltinCBool
	}
	return ir.BuiltinNone
}

// convertArg is arg made through conv: a call of a native conversion, or a
// bodied one's `=>` expression with arg in place of its parameter, since a
// library function is emitted only when the program reaches it.
func convertArg(conv *ir.Func, arg ir.Expr) ir.Expr {
	if body := exprBody(conv); conv.Foreign.Name == "" && body != nil && len(conv.Params) == 1 {
		param := conv.Params[0]
		out := ir.CloneExprSharingDecls(body)
		if id, ok := out.(*ir.Ident); ok && id.Sym == param {
			return arg
		}
		_ = ir.Rewrite(out, func(n ir.Node) (ir.Node, error) {
			if id, ok := n.(*ir.Ident); ok && id.Sym == param {
				return arg, nil
			}
			return n, nil
		})
		return out
	}
	return &ir.Call{Type: conv.Return, Func: conv, Args: []ir.CallArg{{Value: arg}}}
}

func exprBody(fn *ir.Func) ir.Expr {
	if len(fn.Block) != 1 {
		return nil
	}
	if r, ok := fn.Block[0].(*ir.Return); ok {
		return r.Value
	}
	return nil
}
