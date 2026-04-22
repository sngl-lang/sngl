package checker

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// desugarBindings rewrites `:prop=target` props on a VisualNode into the
// equivalent (prop=target + synthesized event handler) pair. The synthesized
// handler writes the new value back into `target` so simple one-line bindings
// are truly two-way without the user wiring an @input/@change handler.
//
// Behaviour:
//   - `:value=target`   → +handler for "input" (fallback "change") that runs `target = e.value`
//   - `:checked=target` → +handler for "change" that runs `target!!`
//   - Other `:prop`     → the `:` prefix is simply dropped.
//
// If the user already declared a handler for the chosen event, we skip
// synthesis (explicit wins). The target must be a plain identifier or field
// access referring to reactive state.
func (c *checker) desugarBindings(comp *ir.Component, props []ir.Arg, handlers []ir.EventHandler) ([]ir.Arg, []ir.EventHandler) {
	existing := make(map[string]bool, len(handlers))
	for _, h := range handlers {
		existing[h.Name] = true
	}
	for i := range props {
		p := &props[i]
		if !strings.HasPrefix(p.Name, ":") {
			continue
		}
		propName := p.Name[1:]
		p.Name = propName

		if comp == nil || p.Value == nil {
			continue
		}
		target := p.Value
		if !isAssignableTarget(target) {
			continue
		}

		evt := pickBindEvent(comp, propName)
		if evt == "" || existing[evt] {
			continue
		}

		body := buildBindBody(target, propName)
		if body == nil {
			continue
		}

		eventType := componentEventType(comp, evt)
		if eventType == nil {
			eventType = ir.TypDyn
		}
		eParam := &ir.Param{Name: "e", Type: eventType}
		fn := &ir.Func{
			Params: []*ir.Param{eParam},
			Block:  []ir.Stmt{body},
		}
		handlers = append(handlers, ir.EventHandler{
			Name: evt,
			Func: fn,
			// AST left nil — this handler has no source counterpart. Codegen
			// backends must treat an empty AST as "check Func.Block instead".
		})
		existing[evt] = true
	}
	return props, handlers
}

func pickBindEvent(comp *ir.Component, propName string) string {
	switch propName {
	case "checked":
		if componentEventType(comp, "change") != nil {
			return "change"
		}
	default:
		if componentEventType(comp, "input") != nil {
			return "input"
		}
		if componentEventType(comp, "change") != nil {
			return "change"
		}
	}
	return ""
}

func buildBindBody(target ir.Expr, propName string) ir.Stmt {
	if propName == "checked" {
		// Checkbox/toggle: flip the bound bool.
		return &ir.Toggle{Target: target}
	}
	// Default: assign target = e.value.
	eIdent := &ir.Ident{Name: "e", Type: ir.TypDyn}
	val := &ir.Select{Operand: eIdent, Field: "value", Type: ir.TypString}
	return &ir.Assign{Target: target, Op: ast.AssignSet, Value: val}
}

// isAssignableTarget is a conservative check: only plain identifiers or field
// accesses on identifiers can serve as binding targets.
func isAssignableTarget(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.Ident:
		return x != nil
	case *ir.Select:
		return x != nil && isAssignableTarget(x.Operand)
	case *ir.Index:
		return x != nil && isAssignableTarget(x.Operand)
	}
	return false
}
