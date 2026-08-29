package testrunner

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
)

// renderSnapshot walks the component's visual body, evaluating reactive
// constructs (if/for, prop bindings) against the component's CURRENT state,
// and produces deterministic SNGL source for the resulting concrete tree.
//
// The output is a fragment: the rendered children of the component, one
// statement per line. It reflects live state — e.g. an interpolated
// `value="Count: {count}"` becomes `value="Count: 3"` for count == 3.
//
// This is a focused renderer: it covers what test fixtures exercise — native
// elements with positional/named props and inline event handlers, text/attr
// interpolation, reactive if/else, reactive for (expanded per the current
// list), platform filters, slots, and nested user components (expanded
// inline). When it meets a construct it cannot render it returns an error
// naming the construct rather than emitting wrong output.
func renderSnapshot(cv *componentValue) (string, error) {
	env := cv.compEnv()
	var b strings.Builder
	w := &snapWriter{env: env, b: &b}
	if err := w.stmts(env, cv.body, 0); err != nil {
		return "", err
	}
	return b.String(), nil
}

type snapWriter struct {
	env *interp.Env
	b   *strings.Builder
}

func indent(depth int) string { return strings.Repeat("    ", depth) }

// stmts emits each visual statement in stmts, evaluating against env.
func (w *snapWriter) stmts(env *interp.Env, stmts []ir.Stmt, depth int) error {
	for _, s := range stmts {
		if err := w.stmt(env, s, depth); err != nil {
			return err
		}
	}
	return nil
}

func (w *snapWriter) stmt(env *interp.Env, s ir.Stmt, depth int) error {
	switch n := s.(type) {
	case *ir.NodeInst:
		return w.nodeInst(env, n, depth)

	case *ir.CallStmt:
		return w.callStmt(env, n, depth)

	case *ir.If:
		cond, err := env.Eval(n.Cond)
		if err != nil {
			return fmt.Errorf("snapshot: evaluating if-condition: %w", err)
		}
		if b, _ := cond.(bool); b {
			return w.stmts(env, n.Body, depth)
		}
		return w.stmts(env, n.Else, depth)

	case *ir.For:
		return w.forStmt(env, n, depth)

	case *ir.SlotInst:
		return w.stmts(env, n.Children, depth)

	case *ir.ContextProvider:
		return w.stmts(env, n.Children, depth)

	case *ir.ErrorBoundary:
		return w.stmts(env, n.Children, depth)

	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle:
		// Imperative statements produce no rendered output.
		return nil

	default:
		return fmt.Errorf("snapshot: cannot render statement of type %T", s)
	}
}

// nodeInst renders an element or user-component instantiation.
func (w *snapWriter) nodeInst(env *interp.Env, n *ir.NodeInst, depth int) error {
	// User-defined component: expand inline against its child env so the
	// snapshot reflects the concrete rendered tree, not an opaque ref.
	if n.Component != nil && isUserComponent(n.Component) {
		childEnv := env.ComponentEnv(n.Component, n)
		return w.stmts(childEnv, n.Component.Body, depth)
	}

	props, err := w.props(env, n.Props, n.Handlers, n.ID)
	if err != nil {
		return err
	}

	hasChildren := len(n.Children) > 0
	w.writeOpen(n.Name, props, depth, hasChildren)
	if !hasChildren {
		return nil
	}
	if err := w.stmts(env, n.Children, depth+1); err != nil {
		return err
	}
	fmt.Fprintf(w.b, "%s}\n", indent(depth))
	return nil
}

// callStmt renders a children-less element call (e.g. `text(value=…)`), or
// expands a user component invoked as a call.
func (w *snapWriter) callStmt(env *interp.Env, n *ir.CallStmt, depth int) error {
	name := interp.CallStmtElemName(n)
	if name == "" {
		// Not an element call (e.g. a bare void call); nothing to render.
		return nil
	}
	if env.Pkg != nil {
		if comp := interp.FindComponent(env.Pkg, name); comp != nil && isUserComponent(comp) {
			childEnv := env.ComponentEnvFromCallStmt(comp, n)
			return w.stmts(childEnv, comp.Body, depth)
		}
	}
	rendered := env.RenderCallStmtNode(n)
	if rendered == nil {
		return nil
	}
	props, err := w.propsFromMap(rendered)
	if err != nil {
		return err
	}
	w.writeOpen(name, props, depth, false)
	return nil
}

func (w *snapWriter) forStmt(env *interp.Env, n *ir.For, depth int) error {
	iterVal, err := env.Eval(n.Iter)
	if err != nil {
		return fmt.Errorf("snapshot: evaluating for-iterable: %w", err)
	}
	list, ok := iterVal.([]any)
	if !ok {
		return fmt.Errorf("snapshot: for-loop over non-list value %T", iterVal)
	}
	if len(list) == 0 {
		return w.stmts(env, n.Else, depth)
	}
	for i, item := range list {
		child := env.Snapshot()
		child.Set(n.KeySym, item)
		child.Set(n.ValueSym, i)
		if err := w.stmts(child, n.Body, depth); err != nil {
			return err
		}
	}
	return nil
}

// prop is a rendered name/value pair (Name == "" for positional).
type prop struct {
	Name  string
	Value string // already SNGL-formatted
}

// props evaluates IR props and inline handlers into formatted SNGL pairs.
func (w *snapWriter) props(env *interp.Env, args []ir.Arg, handlers []ir.EventHandler, id string) ([]prop, error) {
	var out []prop
	for _, a := range args {
		v, err := env.Eval(a.Value)
		if err != nil {
			return nil, fmt.Errorf("snapshot: evaluating prop %q: %w", a.Name, err)
		}
		out = append(out, prop{Name: a.Name, Value: formatSNGLValue(v)})
	}
	for _, h := range handlers {
		// Handler bodies aren't part of the rendered state; emit a stable
		// marker so the snapshot records the handler's presence without
		// depending on lowered closure internals.
		out = append(out, prop{Name: "", Value: "@" + h.Name})
	}
	_ = id
	return out, nil
}

// propsFromMap formats the element-map produced by RenderCallStmtNode into
// SNGL pairs, dropping interpreter bookkeeping keys.
func (w *snapWriter) propsFromMap(m map[string]any) ([]prop, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		switch {
		case k == "_type", k == "__ownerEnv", k == "__ownerComponent":
			continue
		case strings.HasPrefix(k, "@"):
			continue // handlers: appended below in declaration-stable order
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []prop
	for _, k := range keys {
		out = append(out, prop{Name: k, Value: formatSNGLValue(m[k])})
	}
	// Event handlers, sorted for determinism.
	var hk []string
	for k := range m {
		if strings.HasPrefix(k, "@") {
			hk = append(hk, k)
		}
	}
	sort.Strings(hk)
	for _, k := range hk {
		out = append(out, prop{Name: "", Value: k})
	}
	return out, nil
}

// writeOpen emits `name(prop=val, …)` followed by ` {` when open, else newline.
func (w *snapWriter) writeOpen(name string, props []prop, depth int, open bool) {
	fmt.Fprintf(w.b, "%s%s(", indent(depth), name)
	parts := make([]string, len(props))
	for i, p := range props {
		if p.Name == "" {
			parts[i] = p.Value
		} else {
			parts[i] = p.Name + "=" + p.Value
		}
	}
	w.b.WriteString(strings.Join(parts, ", "))
	w.b.WriteString(")")
	if open {
		w.b.WriteString(" {\n")
	} else {
		w.b.WriteString("\n")
	}
}

// formatSNGLValue renders a runtime interpreter value as a SNGL literal.
// Mirrors interp.formatValue's spirit but emits SNGL-source syntax.
func formatSNGLValue(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		return strconv.FormatFloat(val, 'g', -1, 64)
	case fmt.Stringer:
		return strconv.Quote(val.String())
	case []any:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = formatSNGLValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			if k == "_type" || k == "__ownerEnv" || k == "__ownerComponent" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+" = "+formatSNGLValue(val[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", v)
	}
}
