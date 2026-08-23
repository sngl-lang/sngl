//go:build !js

package optimize

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/pkg/go/consteval"
)

// evalTimeout bounds one round: generating, building and running the batch.
// It must be generous — on a cold build cache the link dwarfs the calls.
const evalTimeout = 120 * time.Second

// constResult is a finished compile-time evaluation: a value, or the reason
// there will never be one.
type constResult struct {
	val any
	err error
}

// constCache memoizes results across rounds and across Optimize calls (the
// pipeline optimizes twice per target, and one docs build shares a process).
// The key is derived from the function and its rendered arguments, so two
// calls collide only when they really are the same call.
var constCache sync.Map // string → constResult

// nativeCallState is what a fold pass learned about one pure native call.
type nativeCallState int

const (
	nativeReady   nativeCallState = iota // the value is in hand
	nativePending                        // requested; a later round will have it
	nativeFailed                         // no value will ever arrive
)

// nativeRequest is one call the next round must make.
type nativeRequest struct {
	key        string
	importPath string   // Go import path of the package holding the function
	nativeType string   // qualified native ref, e.g. "docs.Pages"
	funcName   string   // exported Go identifier
	args       []string // arguments as Go source
	imports    []string // import paths the argument sources reference
	ctxArg     bool
	errReturn  bool
	hasResult  bool // returns something other than an error
}

// nativeEval collects the requests one fold pass discovered.
type nativeEval struct {
	byKey map[string]*nativeRequest
	order []*nativeRequest
}

func (n *nativeEval) add(req *nativeRequest) {
	if n.byKey == nil {
		n.byKey = map[string]*nativeRequest{}
	}
	if _, dup := n.byKey[req.key]; dup {
		return
	}
	n.byKey[req.key] = req
	n.order = append(n.order, req)
}

// requestPureGoFunc answers a pure go:// call from the cache, or records it as
// pending so the round loop can batch it with every other pending call.
//
// Only the go scheme can be linked into the generated program. An import with
// no recorded scheme is accepted: a request that shouldn't have been made
// costs one build error naming the function, while wrongly rejecting one costs
// every const that needed it (and the checker's own IR fixtures record no
// Path at all).
func requestPureGoFunc(ctx *evalCtx, scheme, importPath string, f *ir.Func, args []any) (any, nativeCallState, error) {
	if scheme != "go" && scheme != "" {
		return nil, nativeFailed, fmt.Errorf("%s:// functions cannot be evaluated at build time", scheme)
	}
	if importPath == "" {
		return nil, nativeFailed, fmt.Errorf("import has no Go import path")
	}
	_, funcName, ok := strings.Cut(f.NativeName, ".")
	if !ok || funcName == "" || !isExported(funcName) {
		return nil, nativeFailed, fmt.Errorf("%q is not an exported Go function", f.NativeName)
	}
	argSrc, imports, err := renderGoArgs(f, args)
	if err != nil {
		return nil, nativeFailed, err
	}
	key := requestKey(importPath, f.NativeName, argSrc)
	if c, loaded := constCache.Load(key); loaded {
		res := c.(constResult)
		if res.err != nil {
			return nil, nativeFailed, res.err
		}
		return res.val, nativeReady, nil
	}
	req := &nativeRequest{
		key:        key,
		importPath: importPath,
		nativeType: f.NativeName,
		funcName:   funcName,
		args:       argSrc,
		imports:    imports,
		ctxArg:     f.HasContextArg,
		errReturn:  f.HasErrorReturn,
		hasResult:  f.Return != nil,
	}
	if ctx.native != nil {
		ctx.native.add(req)
		return nil, nativePending, nil
	}

	// No batch is open: the round loop was skipped because
	// hasUnresolvedNativeCall found nothing to evaluate. That walk is allowed
	// to be approximate precisely because of this branch — a call it missed is
	// evaluated on its own here, costing one extra build rather than the value.
	runNativeRequests(ctx.dir, []*nativeRequest{req})
	res, _ := constCache.Load(key)
	if r, ok := res.(constResult); ok && r.err == nil {
		return r.val, nativeReady, nil
	} else if ok {
		return nil, nativeFailed, r.err
	}
	return nil, nativeFailed, fmt.Errorf("compile-time evaluation of %s produced no value", f.NativeName)
}

// requestKey identifies a call. The rendered arguments are Go source, so
// distinct argument lists cannot render to the same key — unlike a %v of the
// value slice, where []any{"a b"} and []any{"a","b"} both print "[a b]".
func requestKey(importPath, nativeType string, args []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%d\x00", importPath, nativeType, len(args))
	for _, a := range args {
		fmt.Fprintf(h, "%s\x00", a)
	}
	return fmt.Sprintf("c%x", h.Sum(nil)[:12])
}

func isExported(name string) bool {
	c := name[0]
	return c >= 'A' && c <= 'Z'
}

// renderGoArgs turns folded argument values into Go source using the Go
// language translator, so the generated call site spells a value exactly the
// way generated code would. Returns the argument sources and the import paths
// they reference.
func renderGoArgs(f *ir.Func, args []any) ([]string, []string, error) {
	if len(args) != len(f.Params) {
		return nil, nil, fmt.Errorf("%s takes %d arguments, got %d", f.NativeName, len(f.Params), len(args))
	}
	if len(args) == 0 {
		return nil, nil, nil
	}
	gc := golang.NewIRContext(codegen.NewExprCtx(nil))
	out := make([]string, len(args))
	for i, a := range args {
		e := irFromValue(a, f.Params[i].Type)
		if e == nil {
			return nil, nil, fmt.Errorf("argument %d of %s (%T) has no IR form", i, f.NativeName, a)
		}
		src := gc.EvalExpr(e)
		if strings.TrimSpace(src) == "" {
			return nil, nil, fmt.Errorf("argument %d of %s has no Go form", i, f.NativeName)
		}
		out[i] = src
	}
	return out, gc.Imports(), nil
}

// runNativeRequests evaluates every pending request in one generated program:
// one build, one run, one results document. Each request ends up in
// constCache — with a value, or with the reason it has none, so a later round
// neither re-requests it nor spins.
func runNativeRequests(dir string, reqs []*nativeRequest) {
	values, err := execConstEval(dir, reqs)
	for _, req := range reqs {
		if err != nil {
			constCache.Store(req.key, constResult{err: fmt.Errorf("evaluating %s: %w", req.nativeType, err)})
			continue
		}
		v, ok := values[req.key]
		if !ok {
			// The generated program ran but produced nothing for this key:
			// consteval.Fail was called, or the call never returned.
			constCache.Store(req.key, constResult{err: fmt.Errorf("compile-time evaluation of %s produced no value", req.nativeType)})
			continue
		}
		constCache.Store(req.key, constResult{val: v})
		slog.Debug("const eval", "func", req.nativeType)
	}
}

// execConstEval generates, builds and runs the batch program, returning the
// values keyed by request key.
func execConstEval(dir string, reqs []*nativeRequest) (map[string]any, error) {
	if dir == "" {
		return nil, fmt.Errorf("no project directory")
	}

	// The source must sit inside the project so the module resolves; the
	// binary must not, or `go build ./...` in the project would pick it up.
	src := constEvalSource(reqs)
	srcDir, err := constEvalSrcDir(dir, src)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(srcDir)
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(src), 0o644); err != nil {
		return nil, fmt.Errorf("writing evaluator source: %w", err)
	}

	binPath, err := constEvalBinPath(filepath.Base(srcDir))
	if err != nil {
		return nil, err
	}
	runDir, err := os.MkdirTemp("", "sngl-consteval-*")
	if err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}
	defer os.RemoveAll(runDir)
	resultPath := filepath.Join(runDir, "results.sngl")

	buildCtx, cancel := context.WithTimeout(context.Background(), evalTimeout)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binPath, "./"+filepath.Base(srcDir))
	build.Dir = dir
	slog.Info("exec", "cmd", "go build (const evaluator)", "dir", dir, "pkg", filepath.Base(srcDir), "calls", len(reqs))
	buildStart := time.Now()
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("building const evaluator: %w: %s", err, out)
	}
	slog.Debug("consteval build", "duration", time.Since(buildStart))
	// Mark the entry as used: pruning goes by mtime, and an unchanged program
	// is never relinked, so its binary's own timestamp would go stale.
	now := time.Now()
	os.Chtimes(filepath.Dir(binPath), now, now)

	runCtx, cancelRun := context.WithTimeout(context.Background(), evalTimeout)
	defer cancelRun()
	run := exec.CommandContext(runCtx, binPath)
	run.Dir = dir
	run.Env = append(os.Environ(), consteval.OutEnv+"="+resultPath)
	// Anything an evaluated function prints goes to stderr: results travel in
	// the file, so stdout carries nothing we need.
	run.Stdout = os.Stderr
	run.Stderr = os.Stderr
	runStart := time.Now()
	if err := run.Run(); err != nil {
		return nil, fmt.Errorf("running const evaluator: %w", err)
	}
	slog.Debug("consteval run", "duration", time.Since(runStart))

	results, err := os.ReadFile(resultPath)
	if err != nil {
		return nil, fmt.Errorf("reading const evaluator results: %w", err)
	}
	parseStart := time.Now()
	values, err := parseConstResults(resultPath, results)
	slog.Debug("consteval parse", "bytes", len(results), "duration", time.Since(parseStart))
	return values, err
}

// constEvalSrcDir creates the directory to generate the program into, named
// from a hash of the source.
//
// The name has to be stable: Go's build cache keys a package on its import
// path, so a fresh random directory per invocation misses the cached link
// every time — 1.1s against 0.1s in this repo. Hashing the source means the
// same program lands on the same path (cache hit) while a different call set
// lands on a different one, and Go still invalidates the entry by itself when
// a dependency like docs/ changes. A cache of our own keyed on this hash would
// not: it cannot see that docs.Highlight was edited.
//
// The dot prefix keeps the package out of `./...`.
func constEvalSrcDir(dir, src string) (string, error) {
	sum := sha256.Sum256([]byte(src))
	name := fmt.Sprintf(".sngl-consteval-%x", sum[:8])
	path := filepath.Join(dir, name)

	// Mkdir is the lock: whoever creates the directory owns it until it is
	// removed. A hard crash can leave one behind, but no live owner can
	// outlast evalTimeout, so an older one is reclaimed rather than wedging
	// every later compile into the slow path.
	err := os.Mkdir(path, 0o755)
	if errors.Is(err, fs.ErrExist) {
		if fi, statErr := os.Stat(path); statErr == nil && time.Since(fi.ModTime()) > evalTimeout {
			os.RemoveAll(path)
			err = os.Mkdir(path, 0o755)
		}
	}
	if err == nil {
		return path, nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("creating evaluator dir: %w", err)
	}

	// Another compile is building this exact program right now. A unique
	// directory is correct and only costs the cached link, which beats any
	// wait that could deadlock.
	unique, err := os.MkdirTemp(dir, name+"-*")
	if err != nil {
		return "", fmt.Errorf("creating evaluator dir: %w", err)
	}
	return unique, nil
}

// constEvalBinPath returns the path to build the evaluator binary at, named
// after the generated package.
//
// The path has to be stable for the same program: `go build -o` relinks
// whenever the output is missing (1.0s here against 0.1s when it is already
// there and current), and that dominated a round. Reusing the path is not a
// cache of our own — go decides whether the file is current by comparing its
// build ID against the recomputed action ID, so editing docs/ relinks it and
// the binary reflects the change. It lives outside the project so that a
// 27 MB binary never lands in the tree.
func constEvalBinPath(pkgName string) (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		root = os.TempDir()
	}
	root = filepath.Join(root, "sngl", "consteval")
	pruneConstEvalBins(root)
	binDir := filepath.Join(root, strings.TrimPrefix(pkgName, "."))
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("creating evaluator cache dir: %w", err)
	}
	return filepath.Join(binDir, "eval"), nil
}

// binMaxAge bounds how long an unused evaluator binary is kept. Each one links
// the whole compiler, so they are large and there is one per distinct call set.
const binMaxAge = 7 * 24 * time.Hour

func pruneConstEvalBins(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < binMaxAge {
			continue
		}
		os.RemoveAll(filepath.Join(root, e.Name()))
	}
}

// constEvalSource generates the batch program. Each call sits in its own
// function with a recover, so a panic costs one value rather than the round.
func constEvalSource(reqs []*nativeRequest) string {
	aliases := map[string]string{}
	var paths []string
	for _, r := range reqs {
		if _, ok := aliases[r.importPath]; ok {
			continue
		}
		aliases[r.importPath] = fmt.Sprintf("p%d", len(aliases))
		paths = append(paths, r.importPath)
	}
	extra := map[string]bool{}
	for _, r := range reqs {
		for _, p := range r.imports {
			if _, ok := aliases[p]; !ok {
				extra[p] = true
			}
		}
	}
	var extraPaths []string
	for p := range extra {
		extraPaths = append(extraPaths, p)
	}
	sort.Strings(extraPaths)

	var b strings.Builder
	b.WriteString(`package main

import (
	"context"
	"fmt"
	"os"

	"git.duckfam.us/jonathan/sngl/pkg/go/consteval"

	// A pure func may reach into the codegen registries (docs.Targets()
	// enumerates them); blank-import both so it sees what the compiler sees.
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
`)
	for _, p := range paths {
		fmt.Fprintf(&b, "\n\t%s %q", aliases[p], p)
	}
	for _, p := range extraPaths {
		fmt.Fprintf(&b, "\n\t%q", p)
	}
	// context is imported unconditionally because whether any call in the
	// batch takes one is not known until the call sites are written.
	b.WriteString("\n)\n\nvar _ = context.Background\n\nfunc main() {\n")
	for i := range reqs {
		fmt.Fprintf(&b, "\teval%d()\n", i)
	}
	b.WriteString(`	if err := consteval.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
`)
	for i, r := range reqs {
		var args []string
		if r.ctxArg {
			args = append(args, "context.Background()")
		}
		args = append(args, r.args...)
		call := fmt.Sprintf("%s.%s(%s)", aliases[r.importPath], r.funcName, strings.Join(args, ", "))

		fmt.Fprintf(&b, "\nfunc eval%d() {\n\tconst key = %q\n", i, r.key)
		b.WriteString("\tdefer func() {\n\t\tif r := recover(); r != nil {\n\t\t\tconsteval.Fail(key, fmt.Errorf(\"panic: %v\", r))\n\t\t}\n\t}()\n")
		switch {
		case !r.hasResult && !r.errReturn:
			// A pure func with no result still runs — that is what the folder
			// asked for — and folds to null.
			fmt.Fprintf(&b, "\t%s\n\tconsteval.Emit(key, nil)\n}\n", call)
		case r.errReturn && r.hasResult:
			fmt.Fprintf(&b, "\tv, err := %s\n\tif err != nil {\n\t\tconsteval.Fail(key, err)\n\t\treturn\n\t}\n\tconsteval.Emit(key, v)\n}\n", call)
		case r.errReturn:
			fmt.Fprintf(&b, "\tif err := %s; err != nil {\n\t\tconsteval.Fail(key, err)\n\t\treturn\n\t}\n\tconsteval.Emit(key, nil)\n}\n", call)
		default:
			fmt.Fprintf(&b, "\tconsteval.Emit(key, %s)\n}\n", call)
		}
	}
	return b.String()
}
