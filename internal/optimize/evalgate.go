package optimize

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
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

// gateEval asks policy, once per package, whether reqs may run, and returns
// the ones it may and a refusal for the rest by request key.
func gateEval(policy *trust.Policy, dir string, reqs []*nativeRequest) ([]*nativeRequest, map[string]error, error) {
	byPkg := map[string][]*nativeRequest{}
	var order []string
	for _, r := range reqs {
		if _, seen := byPkg[r.importPath]; !seen {
			order = append(order, r.importPath)
		}
		byPkg[r.importPath] = append(byPkg[r.importPath], r)
	}
	var run []*nativeRequest
	refused := map[string]error{}
	for _, p := range order {
		rs := byPkg[p]
		scheme := rs[0].scheme
		name := scheme + ":" + p
		subject := trust.Subject{Name: name}
		switch scheme {
		case "go":
			subject.Resolve = func() (trust.Origin, error) { return GoOrigin(dir, p) }
		case "js":
			subject.Resolve = func() (trust.Origin, error) { return JSOrigin(dir, p) }
		}
		err := policy.Check(trust.Request{Kind: trust.Eval, Subject: subject})
		var refusal *trust.Refusal
		switch {
		case errors.As(err, &refusal):
			for _, r := range rs {
				refused[r.key] = &evalRefused{pkg: name}
			}
		case err != nil:
			return nil, nil, err
		default:
			run = append(run, rs...)
		}
	}
	return run, refused, nil
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

// callStart is where a call is written: the start of what it calls, which is
// where a reader looks, rather than its parenthesis.
func callStart(c *ast.CallExpr) ast.Pos {
	e := c.Func
	for {
		sel, ok := e.(*ast.SelectExpr)
		if !ok {
			break
		}
		e = sel.Operand
	}
	if e != nil {
		if p := e.ExprPos(); p != nil && p.IsValid() {
			return *p
		}
	}
	return c.Pos
}

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

// GoOrigin is where the Go package path resolves from dir: the main module's
// own, or a directory a replace or vendoring put it in, by its directory and
// module path -- a project's bytes, whatever it calls them -- and a
// dependency go.sum pins by module and version.
func GoOrigin(dir, pkg string) (trust.Origin, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-find", "-json=ImportPath,Dir,Module", "--", pkg)
	cmd.Dir = dir
	cmd.Env = ChildEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return trust.Origin{}, fmt.Errorf("resolving go:%s: %w: %s", pkg, err, strings.TrimSpace(stderr.String()))
	}
	var p struct {
		Dir    string
		Module *struct {
			Path, Version, Dir string
			Main               bool
			Replace            *struct{ Path, Version, Dir string }
		}
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return trust.Origin{}, fmt.Errorf("resolving go:%s: %w", pkg, err)
	}
	m := p.Module
	switch {
	case m == nil:
		return trust.DirOrigin(p.Dir), nil
	case m.Main, m.Dir == "", m.Replace != nil && m.Replace.Version == "":
		// The main module, a vendored copy, a replace to a directory: bytes
		// the project holds, whatever module path it gives them.
		o := trust.DirOrigin(p.Dir)
		o.Module = m.Path
		return o, nil
	case m.Replace != nil:
		return trust.Origin{Spec: "go:" + m.Replace.Path + "@" + m.Replace.Version}, nil
	}
	return trust.Origin{Spec: "go:" + m.Path + "@" + m.Version}, nil
}

// JSOrigin is the directory a js: module resolves to: node_modules is the
// project's own, as anything else on disk is, so no version it claims names
// it.
func JSOrigin(dir, spec string) (trust.Origin, error) {
	if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
		rel, err := jsRunSpecifier(dir, spec)
		if err != nil {
			return trust.Origin{}, err
		}
		// jsRunSpecifier answers from one level below dir.
		return trust.DirOrigin(filepath.Dir(filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(rel, "../"))))), nil
	}
	name := spec
	if strings.HasPrefix(name, "@") {
		parts := strings.SplitN(name, "/", 3)
		name = path.Join(parts[:min(2, len(parts))]...)
	} else {
		name, _, _ = strings.Cut(name, "/")
	}
	return trust.DirOrigin(filepath.Join(dir, "node_modules", filepath.FromSlash(name))), nil
}
