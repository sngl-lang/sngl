//go:build !js

package optimize

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/internal/buildhost"
	"duckfam.us/sngl/internal/gencache"
	"duckfam.us/sngl/internal/gencache/godeps"
	"duckfam.us/sngl/internal/trust"
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/pkg/go/consteval"
)

// evalTimeout bounds one round: generating, building and running the batch.
// It must be generous — on a cold build cache the link dwarfs the calls.
const evalTimeout = 120 * time.Second

// ownerLifetime bounds how long one round can hold its generated package and
// its cached binary. A round builds and runs under evalTimeout each, retries
// both once when the binary was reclaimed under it, and then parses the
// results — so evalTimeout alone is nowhere near the bound.
const ownerLifetime = 5 * evalTimeout

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
	scheme     string   // "go", "js" — decides which runner evaluates the batch
	importPath string   // import path or module specifier of the declaring package
	nativeType string   // qualified native ref, e.g. "docs.Pages"
	funcName   string   // the identifier the generated program calls
	args       []string // arguments as source in the scheme's language
	imports    []string // import paths the argument sources reference (go only)
	ctxArg     bool
	errReturn  bool
	// ret is the declared return type: nil when the function returns nothing
	// but an error, and otherwise the type the result is checked against.
	ret *ir.Type
}

// wantTypes keys the declared return types the way the results file keys its
// records, which is the form parseNativeResults checks values against. Every
// runner builds it the same way from its own batch.
func wantTypes(reqs []*nativeRequest) map[string]*ir.Type {
	want := make(map[string]*ir.Type, len(reqs))
	for _, r := range reqs {
		want[r.key] = r.ret
	}
	return want
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

// requestPureNativeFunc answers a pure scheme-import call from the cache, or
// records it as pending so the round loop can batch it with every other
// pending call.
//
// The scheme decides which runner evaluates the batch and which language the
// arguments are rendered in. An import with no recorded scheme is read as go:
// a request that shouldn't have been made costs one build error naming the
// function, while wrongly rejecting one costs every const that needed it (and
// the checker's own IR fixtures record no Path at all).
func requestPureNativeFunc(ctx *evalCtx, scheme, importPath string, f *ir.Func, args []any) (ir.Expr, nativeCallState, error) {
	if scheme == "" {
		scheme = "go"
	}
	if importPath == "" {
		return nil, nativeFailed, fmt.Errorf("import has no path")
	}
	var (
		funcName string
		argSrc   []string
		imports  []string
		err      error
	)
	switch scheme {
	case "go":
		var ok bool
		_, funcName, ok = strings.Cut(f.Foreign.Name, ".")
		if !ok || funcName == "" || !isExported(funcName) {
			return nil, nativeFailed, fmt.Errorf("%q is not an exported Go function", f.Foreign.Name)
		}
		argSrc, imports, err = renderGoArgs(f, args)
	case "js":
		// A JS export is named by its binding alone: there is no package
		// qualifier to strip, and the importer only ever recorded a
		// declaration that carried an `export`.
		funcName = f.Foreign.Name
		if funcName == "" {
			return nil, nativeFailed, fmt.Errorf("native function has no name")
		}
		argSrc, err = renderJSArgs(f, args)
	default:
		return nil, nativeFailed, fmt.Errorf("%s:// functions cannot be evaluated at build time", scheme)
	}
	if err != nil {
		return nil, nativeFailed, err
	}
	key := requestKey(scheme, importPath, f.Foreign.Name, argSrc)
	if res, loaded := ctx.evalCache().load(key); loaded {
		if res.err != nil {
			return nil, nativeFailed, res.err
		}
		// A clone per call site: later phases mutate IR in place, so one node
		// spliced into several places in the tree would be edited through
		// whichever site was lowered first.
		return ir.CloneExpr(res.expr), nativeReady, nil
	}
	req := &nativeRequest{
		key:        key,
		scheme:     scheme,
		importPath: importPath,
		nativeType: f.Foreign.Name,
		funcName:   funcName,
		args:       argSrc,
		imports:    imports,
		ctxArg:     f.HasContextArg,
		errReturn:  f.HasErrorReturn,
		ret:        f.Return,
	}
	if ctx.native != nil {
		ctx.native.add(req)
		return nil, nativePending, nil
	}
	if err := ctx.nativeErr[scheme]; err != nil {
		// This scheme's batch failed for a reason that is not about this call
		// (a build error, a timeout). Report it without building again per
		// call site, and without caching it: another target's Optimize gets to
		// retry.
		return nil, nativeFailed, err
	}

	// No batch is open: the round loop was skipped because
	// hasUnresolvedNativeCall found nothing to evaluate. That walk is allowed
	// to be approximate precisely because of this branch — a call it missed is
	// evaluated on its own here, costing one extra build rather than the value.
	if errs := runNativeRequests(ctx.evalCache(), ctx.policy(), ctx.dir, ir.IndexNativeDecls(ctx.pkg), []*nativeRequest{req}); errs[scheme] != nil {
		return nil, nativeFailed, errs[scheme]
	}
	if r, ok := ctx.evalCache().load(key); ok && r.err == nil {
		return r.expr, nativeReady, nil
	} else if ok {
		return nil, nativeFailed, r.err
	}
	return nil, nativeFailed, fmt.Errorf("compile-time evaluation of %s produced no value", f.Foreign.Name)
}

// requestKey identifies a call. The rendered arguments are source in the
// callee's language, so distinct argument lists cannot render to the same key
// — unlike a %v of the value slice, where []any{"a b"} and []any{"a","b"} both
// print "[a b]". The scheme is part of the key because the rendering is: the
// same value spells differently in Go and in JS.
func requestKey(scheme, importPath, nativeType string, args []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00", scheme, importPath, nativeType, len(args))
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
		return nil, nil, fmt.Errorf("%s takes %d arguments, got %d", f.Foreign.Name, len(f.Params), len(args))
	}
	if len(args) == 0 {
		return nil, nil, nil
	}
	gc := golang.NewIRContext(codegen.NewExprCtx(nil))
	out := make([]string, len(args))
	for i, a := range args {
		e := irFromValue(a, f.Params[i].Type)
		if e == nil {
			return nil, nil, fmt.Errorf("argument %d of %s (%T) has no IR form", i, f.Foreign.Name, a)
		}
		// The Go translator spells a struct literal with the name its declaring
		// package uses — `Item{}` — while the batch program reaches that
		// package under an alias, where only `p0.Item{}` resolves. Refusing
		// costs this one call; rendering it would be a program that does not
		// build, and every other value in the batch with it.
		if sd := unnameableStruct(e); sd != nil {
			return nil, nil, fmt.Errorf("parameter %s of %s has struct type %s, which the compile-time evaluator cannot spell", f.Params[i].Name, f.Foreign.Name, sd.Name)
		}
		src := gc.EvalExpr(e)
		if strings.TrimSpace(src) == "" {
			return nil, nil, fmt.Errorf("argument %d of %s has no Go form", i, f.Foreign.Name)
		}
		out[i] = src
	}
	return out, gc.Imports(), nil
}

// unnameableStruct returns the first struct literal in e whose type the batch
// program has no name for. A color is not one: it renders as the shared
// snglcolor.Color, whose import the translator registers.
func unnameableStruct(e ir.Expr) *ir.StructDef {
	switch n := e.(type) {
	case *ir.StructLit:
		def := n.Def
		if def == nil && n.Type != nil {
			def, _ = n.Type.Decl.(*ir.StructDef)
		}
		if def != nil && !ir.IsColorStruct(n.Type) && def.Name != "color" {
			return def
		}
		for _, f := range n.Fields {
			if sd := unnameableStruct(f.Value); sd != nil {
				return sd
			}
		}
	case *ir.ListLit:
		for _, el := range n.Elems {
			if sd := unnameableStruct(el); sd != nil {
				return sd
			}
		}
	}
	return nil
}

// runNativeRequests evaluates every pending request, one generated program per
// scheme. Each request ends up in cache — with a value, or with the reason it
// has none, so a later round neither re-requests it nor spins.
//
// Only a program outcome is cached. A returned error is a failure of a batch
// as a whole (the build, the run, a corrupt results document), which says
// nothing about any one call and must not become that call's answer. It is
// returned under its scheme: one scheme failing that way keeps neither
// another scheme's values out of the cache nor its calls from folding.
func runNativeRequests(cache *EvalCache, policy *trust.Policy, dir string, types ir.NativeDecls, reqs []*nativeRequest) map[string]error {
	byScheme := map[string][]*nativeRequest{}
	var order []string
	for _, r := range reqs {
		if _, seen := byScheme[r.scheme]; !seen {
			order = append(order, r.scheme)
		}
		byScheme[r.scheme] = append(byScheme[r.scheme], r)
	}
	errs := map[string]error{}
	for _, scheme := range order {
		if err := runSchemeRequests(cache, policy, dir, types, scheme, byScheme[scheme]); err != nil {
			errs[scheme] = err
		}
	}
	return errs
}

// runSchemeRequests runs one scheme's batch and caches its outcome per call.
func runSchemeRequests(cache *EvalCache, policy *trust.Policy, dir string, types ir.NativeDecls, scheme string, reqs []*nativeRequest) error {
	var (
		values map[string]ir.Expr
		bad    map[string]error
		err    error
	)
	switch scheme {
	case "js":
		// Nothing js: computes is stored, so every call is a run, and every
		// run is asked about.
		run, refused, gerr := gateEval(policy, dir, reqs)
		if gerr != nil {
			return gerr
		}
		values, bad = map[string]ir.Expr{}, map[string]error{}
		if len(run) > 0 {
			values, bad, err = execJSConstEval(dir, types, run)
		}
		maps.Copy(bad, refused)
	default:
		values, bad, err = execConstEval(cache.genStore(), policy, dir, types, reqs)
	}
	if err != nil {
		return err
	}
	for _, req := range reqs {
		switch v, ok := values[req.key]; {
		case bad[req.key] != nil:
			cache.store(req.key, constResult{err: fmt.Errorf("evaluating %s: %w", req.nativeType, bad[req.key])})
		case !ok:
			// The generated program ran but produced nothing for this key:
			// the runtime's Fail was called, or the call never returned.
			cache.store(req.key, constResult{err: fmt.Errorf("compile-time evaluation of %s produced no value", req.nativeType)})
		default:
			cache.store(req.key, constResult{expr: v})
			slog.Debug("const eval", "func", req.nativeType)
		}
	}
	return nil
}

// execConstEval answers a batch of go: calls, returning the values keyed by
// request key, and the keys whose value did not check.
//
// Each call is looked up in gen first: a call evaluated by an earlier build,
// whose Go source and toolchain have not changed since, is answered with the
// value stored then, and only the rest are built into a program and run. A
// value is stored as SNGL -- `const value = …`, the form it arrived in -- behind
// one input, the go.deps closure of the packages the program imported, so an
// edit anywhere in them is an edit that invalidates it (see storeConstEval).
//
// Stored values are checked against the declared return type on the way back
// in, exactly as fresh ones are: they pass through the same results reader, so
// a declaration that changed on the SNGL side is held to what it says now.
func execConstEval(gen *gencache.Store, policy *trust.Policy, dir string, types ir.NativeDecls, reqs []*nativeRequest) (map[string]ir.Expr, map[string]error, error) {
	if dir == "" {
		return nil, nil, fmt.Errorf("no project directory")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}

	raw := map[string]string{}
	var misses []*nativeRequest
	for _, r := range reqs {
		if data, ok := gen.Lookup(storedRequest(absDir, r)); ok {
			if v, ok := storedValue(data); ok {
				raw[r.key] = v
				continue
			}
		}
		misses = append(misses, r)
	}
	slog.Debug("consteval store", "calls", len(reqs), "stored", len(reqs)-len(misses))

	// Only a miss runs anything, so only a miss is asked about: a stored
	// value was computed by a run that was allowed.
	misses, refused, err := gateEval(policy, absDir, misses)
	if err != nil {
		return nil, nil, err
	}

	// Recorded before the program is built, so a file edited while it builds
	// is recorded as it was and found stale next time.
	deps := map[string]gencache.Input{}
	var read []gencache.Input
	if len(misses) > 0 {
		deps = constEvalDeps(gen, absDir, misses)
		results, err := runConstEvalBatch(dir, misses)
		if err != nil {
			return nil, nil, err
		}
		var reported []string
		for line := range strings.SplitSeq(string(results), "\n") {
			if in, ok := strings.CutPrefix(line, genInputRecord); ok {
				reported = append(reported, in)
				continue
			}
			if key, val, ok := strings.Cut(recordLine(line), resultSep); ok {
				raw[key] = val
			}
		}
		// What the batch read of the compiler's own generated source, which
		// every value in it records: one run, so nothing says which call read
		// what, and recording it on all of them errs toward a repeat.
		if extra, err := gencache.ParseInputs(reported); err != nil {
			slog.Debug("consteval generated inputs", "err", err)
			deps = map[string]gencache.Input{} // store nothing rather than store it unrecorded
		} else {
			read = extra
		}
	}

	var doc strings.Builder
	for _, r := range reqs {
		if v, ok := raw[r.key]; ok {
			doc.WriteString(r.key + resultSep + v + "\n")
		}
	}
	parseStart := time.Now()
	values, bad, err := parseNativeResults("const evaluator results", []byte(doc.String()), wantTypes(reqs), types)
	slog.Debug("consteval parse", "bytes", doc.Len(), "duration", time.Since(parseStart))
	if err != nil {
		return nil, nil, err
	}
	for _, r := range misses {
		dep, ok := deps[r.key]
		if _, checked := values[r.key]; ok && checked && bad[r.key] == nil {
			gen.Put(storedRequest(absDir, r), gencache.Output{
				Inputs: append([]gencache.Input{dep}, read...),
				Body:   []byte(storedValuePrefix + raw[r.key] + "\n"),
			})
		}
	}
	for k, e := range refused {
		if bad == nil {
			bad = map[string]error{}
		}
		bad[k] = e
	}
	return values, bad, nil
}

// storedRequest is the store's name for one call: the project, and the
// request key -- which already says which function, in which package, with
// which arguments. The function's name rides along so a stored file says
// what it holds.
func storedRequest(absDir string, r *nativeRequest) gencache.Request {
	return gencache.Request{Producer: constEvalProducer, Params: []string{absDir, r.nativeType, r.key}}
}

// constEvalProducer names stored go: values. Nothing registers it: the
// evaluator answers its misses in one batch, through Lookup and Put, rather
// than one request at a time through Get.
const constEvalProducer = "consteval.go"

// genInputRecord prefixes a line of the results file naming a generated file
// the batch read. It is a comment to the results reader, so a compiler that
// does not look for it reads the file as it always did.
const genInputRecord = "//gencache "

// storedValuePrefix is how a stored value is written: as the const it is.
const storedValuePrefix = "const value = "

// storedValue reads the value back out of a stored file.
func storedValue(data []byte) (string, bool) {
	line, _, _ := strings.Cut(string(gencache.Body(data)), "\n")
	return strings.CutPrefix(line, storedValuePrefix)
}

// constEvalDeps records, per call, the go.deps closure of the packages the
// evaluator program would import for it. Calls into one package share one
// closure, which is looked up or listed once. A closure that cannot be
// listed leaves its calls unrecorded: they are still evaluated, and not
// stored.
func constEvalDeps(gen *gencache.Store, absDir string, reqs []*nativeRequest) map[string]gencache.Input {
	out := map[string]gencache.Input{}
	for _, r := range reqs {
		roots := append(append(slices.Clone(evaluatorImports), r.importPath), r.imports...)
		req, err := godeps.Request(absDir, roots)
		if err != nil {
			continue
		}
		in, _, err := gen.Entry(req)
		if err != nil {
			slog.Debug("consteval deps", "func", r.nativeType, "err", err)
			continue
		}
		out[r.key] = in
	}
	return out
}

// evaluatorImports is what every generated program imports whatever it
// calls: the runtime it reports through, and the codegen registries a pure
// function may enumerate (docs.Targets() does).
var evaluatorImports = []string{
	"duckfam.us/sngl/pkg/go/consteval",
	"duckfam.us/sngl/codegen/lang",
	"duckfam.us/sngl/codegen/platform",
}

// runConstEvalBatch generates, builds and runs the program for reqs and
// returns the results document it wrote.
func runConstEvalBatch(dir string, reqs []*nativeRequest) ([]byte, error) {

	// The source must sit inside the project so the module resolves; the
	// binary must not, or `go build ./...` in the project would pick it up.
	src := constEvalSource(reqs)
	srcDir, canonical, err := constEvalSrcDir(dir, src)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(srcDir)
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(src), 0o644); err != nil {
		return nil, fmt.Errorf("writing evaluator source: %w", err)
	}

	runDir, err := os.MkdirTemp("", "sngl-consteval-*")
	if err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}
	defer os.RemoveAll(runDir)
	resultPath := filepath.Join(runDir, "results.sngl")

	// A fallback directory is uniquely named, so its binary could never be
	// reused: keeping it would be dead weight in the cache until it aged out.
	// It goes next to the results instead and dies with them.
	binPath := filepath.Join(runDir, "eval")
	if canonical {
		if binPath, err = constEvalBinPath(filepath.Base(srcDir)); err != nil {
			return nil, err
		}
	}

	slog.Info("exec", "cmd", "go build (const evaluator)", "dir", dir, "pkg", filepath.Base(srcDir), "calls", len(reqs))
	// Two attempts, because the binary is shared: another compile's cache
	// eviction can delete it between this build and this exec, and the loser of
	// that race has to rebuild rather than report a failure that would abort a
	// build over a reclaimed cache entry. (binGrace makes the window very
	// unlikely; this makes it harmless.)
	for attempt := range 2 {
		if err := buildConstEval(dir, srcDir, binPath); err != nil {
			return nil, err
		}
		err := runConstEval(dir, binPath, resultPath)
		if err == nil {
			break
		}
		if attempt == 0 && errors.Is(err, fs.ErrNotExist) {
			slog.Debug("consteval binary vanished before exec; rebuilding", "path", binPath)
			continue
		}
		return nil, err
	}

	results, err := os.ReadFile(resultPath)
	if err != nil {
		return nil, fmt.Errorf("reading const evaluator results: %w", err)
	}
	return results, nil
}

func buildConstEval(dir, srcDir, binPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), evalTimeout)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binPath, "./"+filepath.Base(srcDir))
	build.Dir = dir
	start := time.Now()
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building const evaluator: %w: %s", err, out)
	}
	slog.Debug("consteval build", "duration", time.Since(start))
	return nil
}

func runConstEval(dir, binPath, resultPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), evalTimeout)
	defer cancel()
	run := exec.CommandContext(ctx, binPath)
	run.Dir = dir
	run.Env = append(buildhost.ChildEnv(), consteval.OutEnv+"="+resultPath)
	// Anything an evaluated function prints goes to stderr: results travel in
	// the file, so stdout carries nothing we need.
	run.Stdout = os.Stderr
	run.Stderr = os.Stderr
	start := time.Now()
	if err := run.Run(); err != nil {
		// Wrapped, not reformatted: the caller checks for fs.ErrNotExist to
		// tell "the binary is gone" from "the program failed".
		return fmt.Errorf("running const evaluator: %w", err)
	}
	slog.Debug("consteval run", "duration", time.Since(start))
	return nil
}

// constEvalSrcDir creates the directory to generate the program into, named
// from a hash of the source. The second result reports whether the directory
// got that canonical name — false when another compile already held it and
// this one had to fall back to a unique name.
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
func constEvalSrcDir(dir, src string) (path string, canonical bool, err error) {
	sum := sha256.Sum256([]byte(src))
	name := fmt.Sprintf(".sngl-consteval-%x", sum[:8])
	path = filepath.Join(dir, name)

	// Mkdir is the lock: whoever creates the directory owns it until it is
	// removed. A hard crash can leave one behind, so a directory no live owner
	// could still hold is reclaimed rather than wedging every later compile
	// into the slow path.
	err = os.Mkdir(path, 0o755)
	if errors.Is(err, fs.ErrExist) {
		if fi, statErr := os.Stat(path); statErr == nil && time.Since(fi.ModTime()) > ownerLifetime {
			os.RemoveAll(path)
			err = os.Mkdir(path, 0o755)
		}
	}
	if err == nil {
		return path, true, nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return "", false, fmt.Errorf("creating evaluator dir: %w", err)
	}

	// Another compile is building this exact program right now. A unique
	// directory is correct and only costs the cached link, which beats any
	// wait that could deadlock.
	unique, err := os.MkdirTemp(dir, name+"-*")
	if err != nil {
		return "", false, fmt.Errorf("creating evaluator dir: %w", err)
	}
	return unique, false, nil
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
	root := os.Getenv(binCacheEnv)
	if root == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		root = filepath.Join(base, "sngl", "consteval")
	}
	pruneConstEvalBins(root, binCacheBytes)
	binDir := filepath.Join(root, strings.TrimPrefix(pkgName, "."))
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("creating evaluator cache dir: %w", err)
	}
	// Touch on every use, hit included. Eviction is by mtime, and an unchanged
	// program is never relinked, so its binary's own timestamp would age out
	// from under the compile that keeps reusing it. Touching here rather than
	// after the build also means the entry is already young while the build
	// and the exec that follows it are in flight, which is what keeps another
	// process's eviction off it.
	now := time.Now()
	os.Chtimes(binDir, now, now)
	return filepath.Join(binDir, "eval"), nil
}

// binCacheEnv overrides where evaluator binaries are cached. It exists
// because the obvious way to relocate the cache — pointing XDG_CACHE_HOME at a
// temp directory — also moves GOCACHE, so the child `go build` rebuilds the
// entire Go build cache there: 600 MB and 35s for what should be a few links.
// Tests set this; CI can point it at a workspace-local directory.
const binCacheEnv = "SNGL_CONSTEVAL_CACHE"

const (
	// binCacheBytes bounds the evaluator binary cache.
	//
	// A byte budget, not an entry count: an entry here is 26 MB or 73 MB
	// depending on which codegen registries the batch links, so a count would
	// bound the cache to anywhere in a 3x range. What grows the cache is not
	// time but argument changes — editing one prose line in a docs page gives
	// docs.Highlight a new argument, which is a new program, which is a new
	// entry — so a day of iterative work would otherwise leave several GB
	// behind. A gigabyte holds a dozen of the largest entries, enough that an
	// iterative session keeps reusing its recent programs.
	binCacheBytes = 1 << 30

	// binMaxAge is the floor under the budget: an entry nothing has used in a
	// week goes whether the cache is over budget or not.
	binMaxAge = 7 * 24 * time.Hour

	// binGrace protects an entry another compile may be about to execute. A run
	// touches its directory before building, and ownerLifetime is how long it
	// may still be building and running after that, so anything younger may be
	// live. Under budget pressure the cache may sit over budget for this long
	// rather than delete a binary out from under a running compile.
	binGrace = ownerLifetime
)

// pruneConstEvalBins enforces binMaxAge and then the byte budget, evicting
// least recently used first. Errors are ignored throughout: this is a cache,
// and failing to reclaim it must never fail a build.
func pruneConstEvalBins(root string, budget int64) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}

	type cacheEntry struct {
		name  string
		used  time.Time
		bytes int64
	}
	var live []cacheEntry
	var total int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		age := time.Since(info.ModTime())
		if age > binMaxAge {
			os.RemoveAll(path)
			continue
		}
		if age < binGrace {
			// May be in use. Its bytes still count against the budget, so a
			// burst of live entries does not silently raise the ceiling.
			total += dirBytes(path)
			continue
		}
		size := dirBytes(path)
		total += size
		live = append(live, cacheEntry{name: e.Name(), used: info.ModTime(), bytes: size})
	}
	if total <= budget {
		return
	}

	slices.SortFunc(live, func(a, b cacheEntry) int { return a.used.Compare(b.used) })
	for _, e := range live {
		if total <= budget {
			return
		}
		if err := os.RemoveAll(filepath.Join(root, e.name)); err != nil {
			continue
		}
		total -= e.bytes
		slog.Debug("consteval cache evict", "entry", e.name, "bytes", e.bytes)
	}
}

func dirBytes(path string) int64 {
	var total int64
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}

// constEvalSource generates the batch program. Each call sits in its own
// function with a recover, so a panic costs one value rather than the round.
//
// It is gofmt'd because it sits inside the project, where `gofmt -l .` finds
// any a crashed compile leaves behind. Source that does not parse is returned
// as written, for the build to report.
func constEvalSource(reqs []*nativeRequest) string {
	src := constEvalSourceRaw(reqs)
	if formatted, err := format.Source([]byte(src)); err == nil {
		return string(formatted)
	}
	return src
}

func constEvalSourceRaw(reqs []*nativeRequest) string {
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

`)
	// The runtime is imported by name; the codegen registries are blank so a
	// pure func that enumerates them (docs.Targets()) sees what the compiler
	// sees.
	fmt.Fprintf(&b, "\t%q\n\t%q\n", evaluatorImports[0], "duckfam.us/sngl/codegen")
	for _, p := range evaluatorImports[1:] {
		fmt.Fprintf(&b, "\t_ %q\n", p)
	}
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
	// The generated files the calls read, as comment records: a reader that
	// does not look for them skips them.
	if f, err := os.OpenFile(os.Getenv(consteval.OutEnv), os.O_APPEND|os.O_WRONLY, 0); err == nil {
		for _, in := range codegen.GeneratedInputs() {
			fmt.Fprintf(f, "` + genInputRecord + `%s\n", in)
		}
		f.Close()
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
		case r.ret == nil && !r.errReturn:
			// A pure func with no result still runs — that is what the folder
			// asked for — and folds to null.
			fmt.Fprintf(&b, "\t%s\n\tconsteval.Emit(key, nil)\n}\n", call)
		case r.errReturn && r.ret != nil:
			fmt.Fprintf(&b, "\tv, err := %s\n\tif err != nil {\n\t\tconsteval.Fail(key, err)\n\t\treturn\n\t}\n\tconsteval.Emit(key, v)\n}\n", call)
		case r.errReturn:
			fmt.Fprintf(&b, "\tif err := %s; err != nil {\n\t\tconsteval.Fail(key, err)\n\t\treturn\n\t}\n\tconsteval.Emit(key, nil)\n}\n", call)
		default:
			fmt.Fprintf(&b, "\tconsteval.Emit(key, %s)\n}\n", call)
		}
	}
	return b.String()
}
