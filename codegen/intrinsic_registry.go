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
	platformEmitters  = map[string]map[string]IntrinsicEmitter{} // platform -> intrinsic ID -> emitter
	platformPackages  = map[string]map[string]bool{}             // platform -> library package it implements
	langDeclared      = map[string]map[string]bool{}             // lang -> intrinsic ID it answers outside the emitter registry
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

// RegisterPlatformIntrinsic registers an emitter for an id a *platform* owns
// rather than a language: one whose native form is a call into the surface the
// platform renders onto, so only builds targeting that platform can emit it at
// all.
//
// The 2D drawing primitives are the case that needs it. gtk4 emits CanvasSave
// in Go and android emits it in Kotlin, but neither language can emit it on its
// own — a bubbletea build in Go has no canvas — so registering them per language
// would claim a capability that is false for every other platform sharing that
// language. html's canvas emitters were registered against "js" for want of
// somewhere better, and said the same untrue thing more quietly.
//
// A transport is the other case, and the reason this exists now: whether a
// program can reach gRPC depends on there being a binding for the target, and
// that answer can come from either side. See RequireIntrinsicFallback.
func RegisterPlatformIntrinsic(platform, id string, e IntrinsicEmitter) {
	intrinsicMu.Lock()
	defer intrinsicMu.Unlock()
	byID := platformEmitters[platform]
	if byID == nil {
		byID = map[string]IntrinsicEmitter{}
		platformEmitters[platform] = byID
	}
	if _, dup := byID[id]; dup {
		panic("codegen: duplicate platform intrinsic emitter " + platform + "/" + id)
	}
	byID[id] = e
}

// LookupPlatformIntrinsic returns the emitter registered for (platform, id), or
// nil. An empty id always returns nil.
func LookupPlatformIntrinsic(platform, id string) IntrinsicEmitter {
	if id == "" {
		return nil
	}
	intrinsicMu.RLock()
	defer intrinsicMu.RUnlock()
	return platformEmitters[platform][id]
}

// PlatformIntrinsicIDs returns the ids platform has registered an emitter for,
// sorted.
func PlatformIntrinsicIDs(platform string) []string {
	intrinsicMu.RLock()
	defer intrinsicMu.RUnlock()
	ids := make([]string, 0, len(platformEmitters[platform]))
	for id := range platformEmitters[platform] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// DeclarePlatformImplements records that a platform emits every intrinsic
// declared in a library package, naming the package rather than the ids.
//
// It exists because some emitters cannot be an IntrinsicEmitter and should not
// be made into one. The 2D drawing primitives are translated inside each
// platform, and for good reason: gtk4 returns []ir.Stmt and carries a pending
// style across calls, android writes lines into a stateful Compose context.
// Both are statement-producing and stateful, where an IntrinsicEmitter renders
// one expression. Reshaping them to fit would cost more than it told anyone.
//
// So the emitter stays where it is and this records the fact it cannot state:
// which side of a build can emit the id. That is not the duplication the
// signature tables were — a declaration says what an id *is*, and this says who
// can emit it, which no declaration can answer.
//
// Naming the package rather than the ids is what keeps it from drifting: a
// primitive added to sngl:internal/draw is covered by every platform that
// already implements drawing, with nothing to update here.
func DeclarePlatformImplements(platform, pkg string) {
	intrinsicMu.Lock()
	defer intrinsicMu.Unlock()
	byPkg := platformPackages[platform]
	if byPkg == nil {
		byPkg = map[string]bool{}
		platformPackages[platform] = byPkg
	}
	byPkg[pkg] = true
}

// DeclareLangImplements records that a language emits these ids from somewhere
// other than a registered emitter.
//
// The language-axis counterpart of DeclarePlatformImplements, and it exists for
// the same reason stated there: an IntrinsicEmitter renders one *expression*,
// and some things are not one. `go.select` is a statement whose arms are
// statement lists, so it is answered from CallStmtLines; `go.makechan` needs the
// call's return type, which an emitter handed only its arguments cannot see; and
// `go.recv` is never emitted alone at all -- the select takes it apart.
//
// It names the ids rather than the package, which is the opposite choice to the
// platform side and deliberate: `sngl:language/go` holds ordinary
// emitter-answered intrinsics too, so covering the whole package would stop
// this check ever asking about them again.
func DeclareLangImplements(lang string, ids ...string) {
	intrinsicMu.Lock()
	defer intrinsicMu.Unlock()
	byID := langDeclared[lang]
	if byID == nil {
		byID = map[string]bool{}
		langDeclared[lang] = byID
	}
	for _, id := range ids {
		byID[id] = true
	}
}

// PlatformImplementsPackage reports whether platform declared it implements the
// intrinsics of pkg.
func PlatformImplementsPackage(platform, pkg string) bool {
	intrinsicMu.RLock()
	defer intrinsicMu.RUnlock()
	return platformPackages[platform][pkg]
}

// AnyTargetImplements reports whether some registered language or platform can
// emit the intrinsic id declares. It is the question the completeness check
// asks: an intrinsic nothing implements and with no body of its own is a build
// emitting a call to a function that does not exist, and it does not matter
// which side answers.
func AnyTargetImplements(def *ir.IntrinsicDef) bool {
	if def == nil || def.Name == "" {
		return false
	}
	intrinsicMu.RLock()
	defer intrinsicMu.RUnlock()
	for _, byID := range intrinsicEmitters {
		if byID[def.Name] != nil {
			return true
		}
	}
	for _, byID := range platformEmitters {
		if byID[def.Name] != nil {
			return true
		}
	}
	for _, byPkg := range platformPackages {
		if byPkg[def.Pkg] {
			return true
		}
	}
	for _, byID := range langDeclared {
		if byID[def.Name] {
			return true
		}
	}
	return false
}

// IntrinsicIDs returns the ids lang has registered an emitter for, sorted:
// what a backend implements, which the intrinsic declarations cannot answer.
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

// EmitIntrinsicCall renders an intrinsic call for a build targeting lang on
// platform, if c resolves to an intrinsic with a registered emitter, returning
// (code, imports, true).
//
// The language is asked first and the platform second, so a platform may
// implement an id its language does not — the 2D primitives are exactly that,
// and a canvas emitter keyed per language claimed every build in that language
// could draw.
// Otherwise it returns ("", nil, false) and the caller falls back to its
// generic emission. This is the single dispatch point every backend's call
// translator should consult before any method-name handling, so intrinsics are
// dispatched by ID — never by method name — and uniformly whether or not the
// call was inlined. The caller must apply the returned imports through its own
// import mechanism.
func EmitIntrinsicCall(lang, platform string, c *ir.Call, translate func(ir.Expr) string) (string, []string, bool) {
	if c == nil || c.Func == nil || c.Func.Intrinsic == "" {
		return "", nil, false
	}
	e := LookupIntrinsic(lang, c.Func.Intrinsic)
	if e == nil {
		e = LookupPlatformIntrinsic(platform, c.Func.Intrinsic)
	}
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
	// A body is the declaration's claim that it computes the right answer, and
	// it is the whole claim — there is no flag beside it to consult. A
	// declaration with none is a signature, so there is nothing here to emit
	// instead of the missing emitter.
	if len(fn.Block) > 0 {
		return
	}
	panic(fmt.Sprintf(
		"codegen: %s has no implementation of intrinsic %q (called as %s), and the declaration has no body to emit instead; "+
			"register an emitter for it, or give the declaration a body if it computes a correct answer",
		lang, fn.Intrinsic, callName(fn)))
}

func callName(fn *ir.Func) string {
	if fn.Receiver != "" {
		return fn.Receiver + "." + fn.Name
	}
	return fn.Name
}
