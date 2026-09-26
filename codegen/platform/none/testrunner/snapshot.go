package testrunner

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/interp"
)

// renderSnapshot renders the component's current tree as deterministic SNGL
// source: the concrete children it renders in the state it is in, one
// statement per line, with `value="Count: {count}"` already interpolated.
//
// It prints a mounted interp.View. It used to walk the IR itself, re-evaluating
// every if, for and prop -- a second walk beside the one behind element refs,
// which the two had to keep in agreement by hand. Printing what the tree
// already resolved is what makes them one thing, and it is why the slot content
// this used to drop now appears.
func renderSnapshot(cv *componentValue) (string, error) {
	view, err := interp.Mount(cv.compEnv())
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	var b strings.Builder
	w := &snapWriter{b: &b}
	w.nodes(view.Roots, 0)
	return b.String(), nil
}

type snapWriter struct {
	b *strings.Builder
}

func indent(depth int) string { return strings.Repeat("    ", depth) }

func (w *snapWriter) nodes(nodes []*interp.Node, depth int) {
	for _, n := range nodes {
		w.node(n, depth)
	}
}

// node prints one mounted node. A component of the program's own renders its
// expansion and never itself -- a snapshot is the rendered tree, so `badge`
// does not appear and the text its body renders does. A body-less one
// (`component holder { var n = 0 }`) renders nothing at all, which falls out of
// the same rule: its expansion is empty.
func (w *snapWriter) node(n *interp.Node, depth int) {
	if n.IsUserComponent() {
		w.nodes(n.Children, depth)
		return
	}
	open := len(n.Children) > 0
	w.writeOpen(n.Name, propsOf(n), depth, open)
	if !open {
		return
	}
	w.nodes(n.Children, depth+1)
	fmt.Fprintf(w.b, "%s}\n", indent(depth))
}

// prop is a rendered name/value pair (Name == "" for a bare marker).
type prop struct {
	Name  string
	Value string // already SNGL-formatted
}

// propsOf renders a node's props in written order, then its handlers.
//
// Written order is the checker's: a positional argument is resolved to its name
// on the way in, so `text({}, "x")` arrives as `text(style=Style{}, value="x")`
// and there is no unnamed prop to place. A handler prints as a bare `@name`
// marker -- a snapshot records that one is attached, never what it would do.
func propsOf(n *interp.Node) []prop {
	out := make([]prop, 0, len(n.PropOrder)+len(n.Handlers))
	for _, name := range n.PropOrder {
		out = append(out, prop{Name: name, Value: formatSNGLValue(n.Props[name])})
	}
	for _, h := range n.Handlers {
		out = append(out, prop{Name: "", Value: "@" + h.Name})
	}
	return out
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
	case *interp.Struct:
		// Before the fmt.Stringer case, which Struct satisfies -- reaching it
		// quoted the whole literal, so a Style printed as `style="{}"` and the
		// snapshot was not the SNGL it claimed to be. Field order is the
		// checker's and already deterministic, so unlike the map case below
		// there is nothing to sort.
		parts := make([]string, 0, len(val.Fields))
		for _, f := range val.Fields {
			parts = append(parts, f.Name+" = "+formatSNGLValue(f.Value))
		}
		return "{" + strings.Join(parts, ", ") + "}"
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
			if k == "_type" || k == "__ownerEnv" || k == "__ownerComponent" || k == "__ownerContext" {
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
