package codegen

import "git.duckfam.us/jonathan/sngl/ir"

// LocalRenamer is a language translation context that can bind one name to
// render as another. Both Go's and JavaScript's contexts do, and the binding is
// the whole of what a var handler's parameter needs.
type LocalRenamer[T any] interface {
	WithRenamedLocal(name, as string) T
}

// BindVarHandlerValue binds a var handler's declared parameter — the value the
// var is being set to — to valueName, whatever the emitted setter calls its own
// parameter. Every backend already holds that value there, so the handler body
// only has to agree on the spelling; nothing is passed in.
func BindVarHandlerValue[T LocalRenamer[T]](rc T, h *ir.EventHandler, valueName string) T {
	if h == nil || h.Func == nil {
		return rc
	}
	for _, p := range h.Func.Params {
		if p == nil || p.Name == "" || p.Name == valueName {
			continue
		}
		rc = rc.WithRenamedLocal(p.Name, valueName)
	}
	return rc
}
