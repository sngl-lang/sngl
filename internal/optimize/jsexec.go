//go:build !js

package optimize

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	jsscheme "git.duckfam.us/jonathan/sngl/codegen/scheme/js"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/pkg/js/consteval"
)

// nodeBin is the interpreter a js:// batch runs under. Named as a bare command
// so PATH decides, which is also what makes "node is not installed" a
// diagnosable condition rather than a mysterious failure.
const nodeBin = "node"

// renderJSArgs turns folded argument values into JavaScript source using the JS
// language translator, so the generated call site spells a value exactly the
// way generated code would.
func renderJSArgs(f *ir.Func, args []any) ([]string, error) {
	if len(args) != len(f.Params) {
		return nil, fmt.Errorf("%s takes %d arguments, got %d", f.NativeName, len(f.Params), len(args))
	}
	if len(args) == 0 {
		return nil, nil
	}
	jc := javascript.NewIRContext(codegen.NewExprCtx(nil))
	out := make([]string, len(args))
	for i, a := range args {
		e := irFromValue(a, f.Params[i].Type)
		if e == nil {
			return nil, fmt.Errorf("argument %d of %s (%T) has no IR form", i, f.NativeName, a)
		}
		src := jc.EvalExpr(e)
		if strings.TrimSpace(src) == "" {
			return nil, fmt.Errorf("argument %d of %s has no JavaScript form", i, f.NativeName)
		}
		out[i] = src
	}
	return out, nil
}

// execJSConstEval generates and runs the js:// batch, returning the values
// keyed by request key and the keys whose value did not check.
//
// There is no build step, so none of the Go path's binary-cache machinery has
// anything to cache: node reads the program and runs it. That also means the
// generated directory needs no stable name — its only job is to hold the
// program until it has run.
func execJSConstEval(dir string, reqs []*nativeRequest) (map[string]ir.Expr, map[string]error, error) {
	if dir == "" {
		return nil, nil, fmt.Errorf("no project directory")
	}
	node, err := exec.LookPath(nodeBin)
	if err != nil {
		return nil, nil, fmt.Errorf("evaluating js:// functions at build time needs %s on PATH: %w", nodeBin, err)
	}

	src, err := jsConstEvalSource(dir, reqs)
	if err != nil {
		return nil, nil, err
	}

	// The program lives inside the project so that node resolves bare
	// specifiers against the project's node_modules; the dot prefix keeps it
	// out of the tool globs that scan the tree.
	srcDir, err := os.MkdirTemp(dir, ".sngl-consteval-js-*")
	if err != nil {
		return nil, nil, fmt.Errorf("creating evaluator dir: %w", err)
	}
	defer os.RemoveAll(srcDir)
	// The runtime travels as a file rather than inlined into the program: a
	// module that wants to register an encoder for a type it does not own has
	// to reach the same module instance the program does, and node gives one
	// instance per resolved path.
	if err := os.WriteFile(filepath.Join(srcDir, consteval.RuntimeFile), consteval.Runtime, 0o644); err != nil {
		return nil, nil, fmt.Errorf("writing evaluator runtime: %w", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.mjs"), []byte(src), 0o644); err != nil {
		return nil, nil, fmt.Errorf("writing evaluator source: %w", err)
	}

	runDir, err := os.MkdirTemp("", "sngl-consteval-js-*")
	if err != nil {
		return nil, nil, fmt.Errorf("creating output dir: %w", err)
	}
	defer os.RemoveAll(runDir)
	resultPath := filepath.Join(runDir, "results.sngl")

	slog.Info("exec", "cmd", nodeBin+" (const evaluator)", "dir", dir, "calls", len(reqs))
	if err := runJSConstEval(node, dir, filepath.Join(srcDir, "main.mjs"), resultPath); err != nil {
		return nil, nil, err
	}

	results, err := os.ReadFile(resultPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading const evaluator results: %w", err)
	}
	want := make(map[string]*ir.Type, len(reqs))
	for _, r := range reqs {
		want[r.key] = r.ret
	}
	return parseConstResults(resultPath, results, want)
}

func runJSConstEval(node, dir, mainPath, resultPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), evalTimeout)
	defer cancel()
	run := exec.CommandContext(ctx, node, mainPath)
	run.Dir = dir
	run.Env = append(os.Environ(), consteval.OutEnv+"="+resultPath)
	// Anything an evaluated function prints goes to stderr: results travel in
	// the file, so stdout carries nothing we need.
	run.Stdout = os.Stderr
	run.Stderr = os.Stderr
	start := time.Now()
	if err := run.Run(); err != nil {
		return fmt.Errorf("running const evaluator: %w", err)
	}
	slog.Debug("consteval run", "lang", "js", "duration", time.Since(start))
	return nil
}

// jsConstEvalSource generates the batch program. Each call sits in its own
// function with a catch, so a throw costs one value rather than the round.
func jsConstEvalSource(dir string, reqs []*nativeRequest) (string, error) {
	aliases := map[string]string{}
	var specs []string
	for _, r := range reqs {
		if _, ok := aliases[r.importPath]; ok {
			continue
		}
		aliases[r.importPath] = fmt.Sprintf("p%d", len(aliases))
		specs = append(specs, r.importPath)
	}

	var b strings.Builder
	b.WriteString("// Code generated by sngl. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "import * as consteval from %q;\n", "./"+consteval.RuntimeFile)
	for _, spec := range specs {
		mod, err := jsRunSpecifier(dir, spec)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "import * as %s from %q;\n", aliases[spec], mod)
	}
	b.WriteString("\n")

	for i, r := range reqs {
		call := fmt.Sprintf("%s.%s(%s)", aliases[r.importPath], r.funcName, strings.Join(r.args, ", "))
		fmt.Fprintf(&b, "async function eval%d() {\n  const key = %q;\n  try {\n", i, r.key)
		if r.ret == nil {
			// A pure func with no result still runs — that is what the folder
			// asked for — and folds to null.
			fmt.Fprintf(&b, "    await %s;\n    consteval.emit(key, null);\n", call)
		} else {
			// Awaited unconditionally: awaiting a non-promise is the value
			// itself, so an async export and a plain one need no distinction
			// here.
			fmt.Fprintf(&b, "    consteval.emit(key, await %s);\n", call)
		}
		b.WriteString("  } catch (err) {\n    consteval.fail(key, err);\n  }\n}\n\n")
	}

	for i := range reqs {
		fmt.Fprintf(&b, "await eval%d();\n", i)
	}
	b.WriteString("await consteval.flush();\n")
	return b.String(), nil
}

// jsRunSpecifier turns a js:// module spec into the specifier the generated
// program imports it by, relative to the directory the program is written into
// (one level below dir).
//
// A bare spec is handed to node unchanged so that node's own node_modules
// resolution applies. A relative spec goes through the same resolver the
// checker used, so `./lib` means the same module in both places — and then
// past it, because that resolver answers with types: a .d.ts describes a
// module but is not one, so the runtime file beside it is what actually runs.
func jsRunSpecifier(dir, spec string) (string, error) {
	if !strings.HasPrefix(spec, ".") && !strings.HasPrefix(spec, "/") {
		return spec, nil
	}
	fsys := os.DirFS(dir)
	root := jsscheme.VirtualRoot
	abs, err := jsscheme.ResolveSpec(fsys, root, spec, path.Join(root, "__sngl_entry__.ts"))
	if err != nil {
		return "", fmt.Errorf("resolving js://%s for compile-time evaluation: %w", spec, err)
	}
	rel, ok := jsscheme.StripVirtRoot(abs, root)
	if !ok {
		return "", fmt.Errorf("resolving js://%s: resolver returned out-of-root path %q", spec, abs)
	}
	if strings.HasSuffix(rel, ".d.ts") {
		runnable, ok := jsRuntimeSibling(fsys, rel)
		if !ok {
			return "", fmt.Errorf("js://%s resolves to %s, which declares a module but is not one; no runnable file sits beside it", spec, rel)
		}
		rel = runnable
	}
	return "../" + rel, nil
}

// jsRuntimeSibling finds the implementation a .d.ts describes, by the
// extensions node will actually execute.
func jsRuntimeSibling(fsys fs.FS, decl string) (string, bool) {
	base := strings.TrimSuffix(decl, ".d.ts")
	for _, ext := range []string{".js", ".mjs", ".cjs"} {
		if _, err := fs.Stat(fsys, base+ext); err == nil {
			return base + ext, true
		}
	}
	return "", false
}
