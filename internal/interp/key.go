package interp

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/pkg/go/snglhost"
)

// Key identifies a node by where it is written. Defined in snglhost because a
// host holds one too. See that declaration.
type Key = snglhost.Key

// VarKey identifies one binding across a reload. ir.Symbol carries SymName, so
// this needs nothing the IR does not already hold.
//
// Two declarations may share a name in different scopes, which is why the
// owner is part of the key rather than the name alone.
type VarKey struct {
	Owner string
	Name  string
}

func (v VarKey) String() string { return v.Owner + "." + v.Name }

// VarKeyOf projects a symbol onto its reload-stable key.
func VarKeyOf(owner string, sym ir.Symbol) VarKey {
	if sym == nil {
		return VarKey{Owner: owner}
	}
	return VarKey{Owner: owner, Name: sym.SymName()}
}

// ComponentKeys returns a key for every node written in the component's body,
// in source order.
//
// The walk is purely structural: no expression is evaluated, so an `if` yields
// keys for both branches and a `for` yields one key set for its body regardless
// of how many iterations the current state produces. Iteration is appended by
// the caller at mount time (see Key.Iter), because the number of iterations is
// state and the path is source.
func ComponentKeys(comp *ir.Component) []Key {
	if comp == nil {
		return nil
	}
	w := &keyWalk{comp: comp.Name}
	w.stmts(comp.Body, "")
	return w.out
}

type keyWalk struct {
	comp string
	out  []Key
}

func (w *keyWalk) emit(path string) {
	w.out = append(w.out, Key{Comp: w.comp, Path: path})
}

// seg builds one path segment. An `#id` is preferred over a positional segment
// because it is the author's own name for the node: it survives statements
// being inserted above it, and it is already how a test addresses a node
// (`c.<id>.click()`). Without one, the index is *among same-named siblings*
// rather than absolute, so adding a `button` above a `text` does not move the
// text's key.
func seg(kind, id string, nth int) string {
	if id != "" {
		return "#" + id
	}
	return fmt.Sprintf("%s@%d", kind, nth)
}

func (w *keyWalk) stmts(stmts []ir.Stmt, prefix string) {
	nth := map[string]int{}
	next := func(kind string) int {
		n := nth[kind]
		nth[kind] = n + 1
		return n
	}
	join := func(s string) string {
		if prefix == "" {
			return s
		}
		return prefix + "/" + s
	}

	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			p := join(seg(n.Name, n.ID, next(n.Name)))
			w.emit(p)
			w.stmts(n.Children, p)
			for _, name := range sortedSlotNames(n.Slots) {
				w.stmts(n.Slots[name].Body, p+"/slot:"+name)
			}

		case *ir.CallStmt:
			// A children-less element call (`text #id(...)`). The name and id
			// live on the AST back-reference, not on the IR.
			name, id := elemCallInfo(n)
			if name == "" {
				continue // a method call statement renders nothing
			}
			w.emit(join(seg(name, id, next(name))))

		case *ir.If:
			p := join(fmt.Sprintf("if@%d", next("if")))
			w.stmts(n.Body, p+":then")
			w.stmts(n.Else, p+":else")

		case *ir.For:
			p := join(fmt.Sprintf("for@%d", next("for")))
			w.stmts(n.Body, p+":body")
			w.stmts(n.Else, p+":else")

		case *ir.SlotInst:
			p := join(fmt.Sprintf("slot@%d", next("slot")))
			w.stmts(n.Children, p)
			for _, name := range sortedSlotNames(n.Slots) {
				w.stmts(n.Slots[name].Body, p+"/slot:"+name)
			}

		case *ir.ErrorBoundary:
			w.stmts(n.Children, join(fmt.Sprintf("boundary@%d", next("boundary"))))

		case *ir.ContextProvider:
			w.stmts(n.Children, join(fmt.Sprintf("context@%d", next("context"))))

		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
			*ir.Break, *ir.Continue:
			// Imperative statements render nothing, so they carry no key --
			// and inserting one does not shift the keys around it.

		default:
			panic(fmt.Sprintf("interp.keyWalk: unhandled ir.Stmt %T", n))
		}
	}
}

func sortedSlotNames(slots map[string]*ir.SlotContent) []string {
	if len(slots) == 0 {
		return nil
	}
	out := make([]string, 0, len(slots))
	for name := range slots {
		out = append(out, name)
	}
	// Deterministic order: a map iteration would make the key list depend on
	// hash seed, and these keys are compared across runs.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// KeyPathsEqual reports whether two key lists describe the same tree shape.
// Used by the reconciler and by tests that check a reload preserved identity.
func KeyPathsEqual(a, b []Key) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// KeyList renders keys one per line, for test failure messages.
func KeyList(keys []Key) string {
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k.String())
		b.WriteByte('\n')
	}
	return b.String()
}
