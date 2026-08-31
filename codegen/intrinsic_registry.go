package codegen

import (
	"fmt"
	"sort"
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
// "list.push") in the given target language (e.g. "javascript"). Backends call
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

// IntrinsicIDs returns the ids lang has registered an emitter for, sorted. For
// a caller asking what a backend implements — which is this registry's own
// question, and not one the intrinsic declarations answer.
func IntrinsicIDs(lang string) []string {
	intrinsicMu.RLock()
	defer intrinsicMu.RUnlock()
	ids := make([]string, 0, len(intrinsicEmitters[lang]))
	for id := range intrinsicEmitters[lang] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
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
	args := make([]ir.Expr, 0, len(c.Args)+1)
	// Prepend the receiver only when it's a value operand filling the
	// intrinsic's first param — i.e. a method-style call like canvas
	// `ctx.fillRect(x,y,w,h)` (params [ctx,x,y,w,h], explicit args [x,y,w,h]).
	// A namespace-qualified call like `stdlib.StrLength(s)` (the inlined body
	// of `string.length`) carries the namespace as Receiver but passes every
	// operand as an explicit arg, so the explicit args already fill all params;
	// prepending would shift the `stdlib` namespace ident into a[0] and emit
	// e.g. `len(stdlib)`. Gate on the param count so both shapes land their
	// operands at the right indices.
	def := ir.LookupIntrinsic(c.Func.Intrinsic)
	prependReceiver := c.Receiver != nil && (def == nil || len(c.Args) < len(def.Params))
	if prependReceiver {
		args = append(args, c.Receiver)
	}
	for _, a := range c.Args {
		args = append(args, a.Value)
	}
	if out, imports := e(args, translate); out != "" {
		return out, imports, true
	}
	return "", nil, false
}

// RequireIntrinsicFallback stops a build that has nothing to emit. A
// declaration marked #[intrinsic] with no body is a signature: the result
// comes from the target's implementation of the id, so emitting a plain call
// to it would name a function that does not exist. The `usable` flag is the
// declaration saying its own body computes the same answer, which is what
// makes emitting the SNGL body safe for the ones that carry it.
//
// Call this at the point a backend is about to emit a generic call, not when
// the id-keyed registry declines: several ids are still served by a backend's
// own name-keyed dispatch, and those calls never reach the generic path.
func RequireIntrinsicFallback(lang string, fn *ir.Func) {
	if fn == nil || fn.Intrinsic == "" {
		return
	}
	// A body that survived type checking is one the declaration claimed
	// computes the right answer; a placeholder was dropped there, so there is
	// nothing here to emit instead. The body is the whole question — asking
	// `usable` as well would let a `usable` intrinsic with an empty body
	// through, which is the case with nothing to emit.
	if len(fn.Block) > 0 {
		return
	}
	panic(fmt.Sprintf(
		"codegen: %s has no implementation of intrinsic %q (called as %s), and its SNGL body is a placeholder, not an implementation; "+
			"register an emitter for it, or mark the declaration `usable` if its body is a correct answer",
		lang, fn.Intrinsic, callName(fn)))
}

func callName(fn *ir.Func) string {
	if fn.Receiver != "" {
		return fn.Receiver + "." + fn.Name
	}
	return fn.Name
}
