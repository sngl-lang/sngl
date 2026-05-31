package codegen

import (
	"sync"

	"git.duckfam.us/jonathan/sngl/ir"
)

// IntrinsicEmitter renders the native call-site invocation for an intrinsic in
// one target language. args are the call's arguments in positional order — for
// a type-method intrinsic args[0] is the receiver — exactly as they appear in
// the IR (whether the call reached codegen as a type-method call or was inlined
// to a direct intrinsic call). translate renders a sub-expression to native
// source in the caller's context. Emitters inspect arg IR/types directly, so
// generic intrinsics specialize on the concrete element types here rather than
// forcing every backend to special-case method names.
//
// The returned imports are language-defined dependency tags the emitted code
// needs (Go import paths like "strings"/"math"; empty for backends whose
// native form is a builtin or fully qualified). The call site applies them
// through its own import mechanism (e.g. GoIRContext.RequireImport).
//
// Returning code == "" declines emission; the caller then falls back to its
// generic path. This lets a language register only the intrinsics it
// implements.
type IntrinsicEmitter func(args []ir.Expr, translate func(ir.Expr) string) (code string, imports []string)

var (
	intrinsicMu       sync.RWMutex
	intrinsicEmitters = map[string]map[string]IntrinsicEmitter{} // lang -> intrinsic ID -> emitter
)

// RegisterIntrinsic registers an emitter for the given intrinsic ID (e.g.
// "ListPush") in the given target language (e.g. "javascript"). Backends call
// this from init(). Registering the same (lang, id) twice panics — intrinsic
// emission must have a single owner.
func RegisterIntrinsic(lang, id string, e IntrinsicEmitter) {
	intrinsicMu.Lock()
	defer intrinsicMu.Unlock()
	byID := intrinsicEmitters[lang]
	if byID == nil {
		byID = map[string]IntrinsicEmitter{}
		intrinsicEmitters[lang] = byID
	}
	if _, dup := byID[id]; dup {
		panic("codegen: duplicate intrinsic emitter " + lang + "/" + id)
	}
	byID[id] = e
}

// LookupIntrinsic returns the emitter registered for (lang, id), or nil. ID is
// typically a *ir.Func's Intrinsic field; an empty id always returns nil.
func LookupIntrinsic(lang, id string) IntrinsicEmitter {
	if id == "" {
		return nil
	}
	intrinsicMu.RLock()
	defer intrinsicMu.RUnlock()
	return intrinsicEmitters[lang][id]
}

// EmitIntrinsicCall renders an intrinsic call for lang if c resolves to an
// intrinsic with a registered emitter, returning (code, imports, true).
// Otherwise it returns ("", nil, false) and the caller falls back to its
// generic emission. This is the single dispatch point every backend's call
// translator should consult before any method-name handling, so intrinsics are
// dispatched by ID — never by method name — and uniformly whether or not the
// call was inlined. The caller must apply the returned imports through its own
// import mechanism.
func EmitIntrinsicCall(lang string, c *ir.Call, translate func(ir.Expr) string) (string, []string, bool) {
	if c == nil || c.Func == nil || c.Func.Intrinsic == "" {
		return "", nil, false
	}
	e := LookupIntrinsic(lang, c.Func.Intrinsic)
	if e == nil {
		return "", nil, false
	}
	args := make([]ir.Expr, len(c.Args))
	for i, a := range c.Args {
		args[i] = a.Value
	}
	if out, imports := e(args, translate); out != "" {
		return out, imports, true
	}
	return "", nil, false
}
