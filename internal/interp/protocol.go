package interp

import (
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/ir"
)

// hosted is a node a simulated host made through its component's declared
// protocol: what the create returned, and each prop as the host last wrote it.
type hosted struct {
	made  any
	props map[string]any
}

// hostProtocols makes, fills and connects every node of v whose component
// declares its protocol, through the #[gen.protocol] functions lowering
// splices into the target's node ops, so the natives they call are handed
// what the target hands them and the tracker follows each `c.str`. A node
// already made has only the props that changed written; one gone from the
// tree is forgotten, and made again if it returns.
func (env *Env) hostProtocols(v *View) error {
	h := env.owned()
	if h.hosted == nil {
		h.hosted = map[Key]*hosted{}
	}
	seen := map[Key]bool{}
	var walk func([]*Node) error
	walk = func(nodes []*Node) error {
		for _, n := range nodes {
			if p := protocolOf(n); p != nil {
				seen[n.Key] = true
				if err := env.host(h, n, p); err != nil {
					return err
				}
			}
			if err := walk(n.Children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(v.Roots); err != nil {
		return err
	}
	for k := range h.hosted {
		if !seen[k] {
			delete(h.hosted, k)
		}
	}
	return nil
}

func protocolOf(n *Node) *ir.NodeProtocol {
	if c := n.Component; c != nil && c.Protocol != nil && c.Protocol.Create != nil {
		return c.Protocol
	}
	return nil
}

func (env *Env) host(h *handles, n *Node, p *ir.NodeProtocol) error {
	hn, ok := h.hosted[n.Key]
	if !ok {
		made, err := env.CallUserFuncValues(p.Create, nil)
		if err != nil {
			return err
		}
		hn = &hosted{made: made, props: map[string]any{}}
		h.hosted[n.Key] = hn
	}
	for _, name := range n.PropOrder {
		set := p.Setter(name)
		if set == nil {
			continue
		}
		v := n.Props[name]
		if old, had := hn.props[name]; had && renderValue(old) == renderValue(v) {
			continue
		}
		hn.props[name] = v
		if _, err := env.CallUserFuncValues(set, []any{hn.made, v}); err != nil {
			return err
		}
	}
	if ok {
		return nil
	}
	for _, hd := range n.Handlers {
		fn, args := p.Handler(hd.Name), []any{hn.made}
		if fn == nil {
			if fn = h.platformConnect(); fn == nil {
				continue
			}
			args = append(args, connectsAs(n.Component, hd.Name))
		}
		if _, err := env.CallUserFuncValues(fn, append(args, nil)); err != nil {
			return err
		}
	}
	return nil
}

// connectsAs is event under the name its host raises it by.
func connectsAs(comp *ir.Component, event string) string {
	for _, e := range comp.Events {
		if e.Name == event && e.Connects != "" {
			return e.Connects
		}
	}
	return event
}

// platformConnect is the #[gen.protocol(connect)] of the simulated platform's
// build-target node, which every handler its components declare no connect
// for goes through.
func (h *handles) platformConnect() *ir.Func {
	if !h.connectFound {
		h.connectFound = true
		if p := checker.LibPackage("platform/" + h.simulated); p != nil {
			for _, c := range p.Components {
				if _, name, ok := ir.TargetNode(c); ok && name == h.simulated {
					h.connect = c.Protocol.Op(ir.ProtocolConnect)
				}
			}
		}
	}
	return h.connect
}
