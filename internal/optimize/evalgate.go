package optimize

import (
	"errors"
	"fmt"
	"sort"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/buildhost"
	"git.duckfam.us/jonathan/sngl/internal/trust"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Evaluating a go: or js: function at build time builds the project's own
// package and runs it: native code with the user's privileges, which nothing
// between it and the OS asks about. So it is gated per evaluated package and
// totally (internal/trust), and a value already in the store needs no grant --
// it was computed by a run that was allowed, and replaying it runs nothing.
//
// A refused call does not fold. Whether that is fatal is the target's
// answer: one that can call the scheme at run time emits the call, and the
// build says so in a warning, once per package; one that cannot fails, with
// the same remedy.

// evalRefused is the reason a refused call has no value.
type evalRefused struct {
	pkg string // as a flag names it: "go:example.com/docs"
}

func (e *evalRefused) Error() string {
	return fmt.Sprintf("running %s was not allowed", e.pkg)
}

// refusedPkg is an evaluated package this target was refused, and the call
// sites left unfolded because of it.
type refusedPkg struct {
	name   string
	scheme string
	first  ast.Pos
	sites  map[*ast.CallExpr]bool
}

// policy is the run's trust policy; nil refuses.
func (ctx *evalCtx) policy() *trust.Policy {
	if ctx.cfg == nil {
		return nil
	}
	return ctx.cfg.Trust
}

// noteRefused records a call site left unfolded because its package was
// refused. It reports whether err was such a refusal.
func (ctx *evalCtx) noteRefused(call *ir.Call, scheme string, err error) bool {
	var ref *evalRefused
	if !errors.As(err, &ref) || ctx.cfg == nil {
		return false
	}
	cfg := ctx.cfg
	if cfg.refused == nil {
		cfg.refused = map[string]*refusedPkg{}
	}
	rp := cfg.refused[ref.pkg]
	if rp == nil {
		rp = &refusedPkg{name: ref.pkg, scheme: scheme, sites: map[*ast.CallExpr]bool{}}
		cfg.refused[ref.pkg] = rp
	}
	if call.AST != nil && !rp.sites[call.AST] {
		rp.sites[call.AST] = true
		if pos := callStart(call.AST); !rp.first.IsValid() || before(pos, rp.first) {
			rp.first = pos
		}
	}
	return true
}

func callStart(c *ast.CallExpr) ast.Pos { return buildhost.CallStart(c) }

func before(a, b ast.Pos) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column < b.Column
}

// reportRefused turns what this target was refused into warnings, or into the
// error that stops a target that cannot call the package at run time.
func reportRefused(cfg *Config) error {
	names := make([]string, 0, len(cfg.refused))
	for n := range cfg.refused {
		names = append(names, n)
	}
	sort.Strings(names)
	if cfg.warned == nil {
		cfg.warned = map[string]bool{}
	}
	for _, n := range names {
		rp := cfg.refused[n]
		count := len(rp.sites)
		if count == 0 {
			continue
		}
		calls, was, them := "calls", "were", "them"
		if count == 1 {
			calls, was, them = "call", "was", "it"
		}
		flag := fmt.Sprintf("--allow-eval=%q", n)
		remedy := fmt.Sprintf("running %s needs %s (or `sngl trust %s` to record it)", them, flag, flag)
		if !schemeRunnableAtRuntime(rp.scheme, cfg.Language) {
			return ir.Diagnostic{Pos: rp.first, Severity: ir.Error, Msg: fmt.Sprintf(
				"%d %s into %s %s not evaluated, and a %q build cannot call %s at run time: %s",
				count, calls, n, was, cfg.Language, them, remedy)}
		}
		if cfg.warned[n] {
			continue
		}
		cfg.warned[n] = true
		cfg.Warnings = append(cfg.Warnings, ir.Diagnostic{Pos: rp.first, Severity: ir.Warning, Msg: fmt.Sprintf(
			"%d %s into %s %s not evaluated: %s", count, calls, n, was, remedy)})
	}
	return nil
}
