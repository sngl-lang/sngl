//go:build !js

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
	"strings"
	"time"

	"duckfam.us/sngl/internal/buildhost"
	"duckfam.us/sngl/internal/trust"
)

// What runs a gated package and resolves where it came from: a WASM build
// runs nothing (goexec_js.go), so none of it is there.

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

// GoOrigin is where the Go package path resolves from dir: the main module's
// own, or a directory a replace or vendoring put it in, by its directory and
// module path -- a project's bytes, whatever it calls them -- and a
// dependency go.sum pins by module and version.
func GoOrigin(dir, pkg string) (trust.Origin, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-find", "-json=ImportPath,Dir,Module", "--", pkg)
	cmd.Dir = dir
	cmd.Env = buildhost.ChildEnv()
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
