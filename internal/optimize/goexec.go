//go:build !js

package optimize

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"git.duckfam.us/jonathan/sngl/ir"
)

var pureCache sync.Map // funcName+args → result

// evalTimeout bounds a single compile-time evaluation. It must be generous:
// the first request pays for building the evaluator, and on a cold cache
// (fresh CI runner) that build dwarfs the call itself.
const evalTimeout = 60 * time.Second

// execPureGoFunc evaluates a pure Go function at compile time.
//
// Every pure function reachable from the package's go:// imports is compiled
// once into a single evaluator binary that stays resident and answers calls
// over a pipe. Building per call — the `go run` this replaced — cost a full
// link each time: 33 calls, 33 seconds, for one docs-site build.
func execPureGoFunc(ctx *evalCtx, importPath, nativeType string, args []any) (any, error) {
	cacheKey := fmt.Sprintf("%s:%v", nativeType, args)
	if cached, ok := pureCache.Load(cacheKey); ok {
		return cached, nil
	}

	ev, err := evaluatorFor(ctx)
	if err != nil {
		return nil, err
	}
	_, funcName, _ := strings.Cut(nativeType, ".")
	result, err := ev.call(importPath+"."+funcName, nativeType, args)
	if err != nil {
		return nil, err
	}
	result = normalizeJSON(result)
	pureCache.Store(cacheKey, result)
	return result, nil
}

// evaluator is a resident child process holding every pure Go function the
// package's go:// imports expose, addressed by "<import path>.<func>".
type evaluator struct {
	mu      sync.Mutex
	dir     string
	binPath string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	results *bufio.Reader // child's fd 3, not its stdout
	dead    error         // set once the child is no longer usable
}

var (
	evaluatorsMu sync.Mutex
	evaluators   = map[string]*evaluator{} // dir+function-set → evaluator
)

// evaluatorFor returns the evaluator covering ctx's go:// imports, building it
// on first use. Keying on the function set (not just the directory) means a
// package importing more go:// functions than an earlier one gets its own
// evaluator instead of missing entries in a stale one.
func evaluatorFor(ctx *evalCtx) (*evaluator, error) {
	if ctx.dir == "" {
		return nil, fmt.Errorf("no project directory")
	}
	entries := pureGoFuncs(ctx)
	if len(entries) == 0 {
		return nil, fmt.Errorf("no pure go:// functions to evaluate")
	}

	key := ctx.dir + "\x00" + entriesKey(entries)
	evaluatorsMu.Lock()
	defer evaluatorsMu.Unlock()
	if ev, ok := evaluators[key]; ok {
		if ev.dead != nil {
			return nil, ev.dead
		}
		return ev, nil
	}

	ev, err := startEvaluator(ctx.dir, entries)
	if err != nil {
		// Remember the failure: a build that failed once fails identically for
		// every later call, so later calls report it without rebuilding.
		evaluators[key] = &evaluator{dir: ctx.dir, dead: err}
		return nil, err
	}
	evaluators[key] = ev
	return ev, nil
}

// evalEntry is one function the evaluator exposes.
type evalEntry struct {
	importPath string // Go import path, e.g. "git.duckfam.us/jonathan/sngl/docs"
	funcName   string // exported Go identifier, e.g. "Highlight"
}

func (e evalEntry) key() string { return e.importPath + "." + e.funcName }

// pureGoFuncs collects every pure function of every go:// import. A known
// non-go scheme is excluded — a js:// function is not Go source and cannot be
// linked in — but an import with no recorded scheme is included: the evaluator
// links all of these together, so wrongly excluding one costs every const in
// the package, while wrongly including one costs a build error naming it.
func pureGoFuncs(ctx *evalCtx) []evalEntry {
	var entries []evalEntry
	seen := map[string]bool{}
	for alias, ns := range ctx.getNativeImports() {
		if scheme := ctx.nativeSchemes[alias]; scheme != "go" && scheme != "" {
			continue
		}
		if ns.ImportPath == "" {
			continue
		}
		for _, f := range ns.Funcs {
			if f.Purity != ir.PurityPure || f.NativePkg == "file" || f.Unusable != "" {
				continue
			}
			_, name, ok := strings.Cut(f.NativeName, ".")
			if !ok || name == "" || !isExported(name) {
				continue
			}
			e := evalEntry{importPath: ns.ImportPath, funcName: name}
			if seen[e.key()] {
				continue
			}
			seen[e.key()] = true
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key() < entries[j].key() })
	return entries
}

func isExported(name string) bool {
	c := name[0]
	return c >= 'A' && c <= 'Z'
}

func entriesKey(entries []evalEntry) string {
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintln(h, e.key())
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// startEvaluator generates, builds, and launches the evaluator binary.
func startEvaluator(dir string, entries []evalEntry) (*evaluator, error) {
	// The source must sit inside the project so the module resolves; the
	// binary must not, or `go build ./...` in the project would pick it up.
	srcDir, err := os.MkdirTemp(dir, ".sngl-goeval-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(srcDir)
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(evaluatorSource(entries)), 0o644); err != nil {
		return nil, fmt.Errorf("writing evaluator source: %w", err)
	}

	binDir, err := os.MkdirTemp("", "sngl-goeval-*")
	if err != nil {
		return nil, fmt.Errorf("creating binary dir: %w", err)
	}
	binPath := filepath.Join(binDir, "eval")

	buildCtx, cancel := context.WithTimeout(context.Background(), evalTimeout)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binPath, "./"+filepath.Base(srcDir))
	build.Dir = dir
	slog.Info("exec", "cmd", "go build (const evaluator)", "dir", dir, "funcs", len(entries))
	if out, err := build.CombinedOutput(); err != nil {
		os.RemoveAll(binDir)
		return nil, fmt.Errorf("building const evaluator: %w: %s", err, out)
	}

	// Results come back on fd 3, so anything the evaluated code prints to
	// stdout cannot corrupt the protocol.
	resultR, resultW, err := os.Pipe()
	if err != nil {
		os.RemoveAll(binDir)
		return nil, err
	}
	defer resultW.Close()

	cmd := exec.Command(binPath)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{resultW}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		os.RemoveAll(binDir)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		os.RemoveAll(binDir)
		return nil, fmt.Errorf("starting const evaluator: %w", err)
	}

	return &evaluator{
		dir:     dir,
		binPath: binPath,
		cmd:     cmd,
		stdin:   stdin,
		results: bufio.NewReader(resultR),
	}, nil
}

// evalRequest and evalResponse are the wire format, one JSON object per line
// in each direction.
type evalRequest struct {
	Func string `json:"func"`
	Args []any  `json:"args"`
}

type evalResponse struct {
	Value any    `json:"value"`
	Error string `json:"error,omitempty"`
}

// call evaluates one function in the resident child. Requests are serialized:
// the protocol is a single pipe pair, and evaluation is fast enough that
// pipelining would buy less than it costs in complexity.
func (ev *evaluator) call(key, nativeType string, args []any) (any, error) {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if ev.dead != nil {
		return nil, ev.dead
	}
	if args == nil {
		args = []any{}
	}

	req, err := json.Marshal(evalRequest{Func: key, Args: args})
	if err != nil {
		return nil, err
	}
	if _, err := ev.stdin.Write(append(req, '\n')); err != nil {
		return nil, ev.kill(fmt.Errorf("writing request for %s: %w", nativeType, err))
	}

	// A hung evaluation would otherwise block the compile forever: the child
	// holds every subsequent call, so the timeout has to take the whole
	// process down rather than abandon one request.
	type readResult struct {
		line []byte
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		line, err := ev.results.ReadBytes('\n')
		done <- readResult{line, err}
	}()

	select {
	case <-time.After(evalTimeout):
		return nil, ev.kill(fmt.Errorf("compile-time evaluation of %s timed out after %s", nativeType, evalTimeout))
	case r := <-done:
		if r.err != nil {
			return nil, ev.kill(fmt.Errorf("compile-time evaluation of %s failed: evaluator exited: %w", nativeType, r.err))
		}
		var resp evalResponse
		if err := json.Unmarshal(r.line, &resp); err != nil {
			return nil, ev.kill(fmt.Errorf("parsing result of %s: %w", nativeType, err))
		}
		if resp.Error != "" {
			// A per-call failure (panic, returned error) leaves the child
			// healthy — only this value is lost.
			return nil, fmt.Errorf("compile-time evaluation of %s failed: %s", nativeType, resp.Error)
		}
		slog.Debug("const eval", "func", nativeType)
		return resp.Value, nil
	}
}

// kill tears down a child that can no longer be trusted and records why, so
// later calls fall back instead of hanging on a dead pipe. It returns cause
// for the caller to propagate.
func (ev *evaluator) kill(cause error) error {
	ev.dead = cause
	if ev.cmd != nil && ev.cmd.Process != nil {
		ev.cmd.Process.Kill()
		ev.cmd.Wait()
	}
	ev.cleanupBinary()
	return cause
}

func (ev *evaluator) cleanupBinary() {
	if ev.binPath != "" {
		os.RemoveAll(filepath.Dir(ev.binPath))
		ev.binPath = ""
	}
}

// close shuts the child down and removes its binary.
func (ev *evaluator) close() {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if ev.dead == nil && ev.stdin != nil {
		ev.stdin.Close() // EOF on stdin is the child's exit signal
		if ev.cmd != nil {
			ev.cmd.Wait()
		}
		ev.dead = fmt.Errorf("evaluator closed")
	}
	ev.cleanupBinary()
}

// CloseEvaluators shuts down every resident compile-time evaluator and
// removes its binary. A command that compiles should call it before exiting;
// leaving it uncalled leaks one binary per distinct import set into the
// temp directory.
func CloseEvaluators() {
	evaluatorsMu.Lock()
	defer evaluatorsMu.Unlock()
	for key, ev := range evaluators {
		ev.close()
		delete(evaluators, key)
	}
}

// evaluatorSource generates the evaluator program. Calls are dispatched
// through reflection so the generated code never has to name a parameter or
// result type: it only names the functions, which is all the IR gives us.
func evaluatorSource(entries []evalEntry) string {
	// One alias per import path, stable across runs so the build cache hits.
	aliases := map[string]string{}
	var paths []string
	for _, e := range entries {
		if _, ok := aliases[e.importPath]; ok {
			continue
		}
		aliases[e.importPath] = fmt.Sprintf("p%d", len(aliases))
		paths = append(paths, e.importPath)
	}

	var b strings.Builder
	b.WriteString(`package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
`)
	// Blank-import the registries so a pure func reaching into
	// codegen.Platforms() sees the same registrations the compiler has.
	for _, p := range paths {
		fmt.Fprintf(&b, "\n\t%s %q", aliases[p], p)
	}
	b.WriteString("\n)\n\nvar funcs = map[string]any{\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "\t%q: %s.%s,\n", e.key(), aliases[e.importPath], e.funcName)
	}
	b.WriteString("}\n")
	b.WriteString(evaluatorRuntime)
	return b.String()
}

// evaluatorRuntime is the fixed half of the generated program: the request
// loop and the reflective call. It is a plain string so the generated file
// stays readable when a build of it fails.
const evaluatorRuntime = `
type request struct {
	Func string            ` + "`json:\"func\"`" + `
	Args []json.RawMessage ` + "`json:\"args\"`" + `
}

type response struct {
	Value any    ` + "`json:\"value\"`" + `
	Error string ` + "`json:\"error,omitempty\"`" + `
}

var (
	ctxType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errType = reflect.TypeOf((*error)(nil)).Elem()
)

func main() {
	// Results go to fd 3; stdout is redirected to stderr so a stray print
	// inside an evaluated function cannot corrupt the protocol.
	out := bufio.NewWriter(os.NewFile(3, "results"))
	os.Stdout = os.Stderr

	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(out)
	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			return // EOF: the compiler is done with us
		}
		v, err := call(req)
		resp := response{Value: v}
		if err != nil {
			resp = response{Error: err.Error()}
		}
		if err := enc.Encode(resp); err != nil {
			return
		}
		if err := out.Flush(); err != nil {
			return
		}
	}
}

// call invokes one function. A panic is contained here rather than taking the
// process down: the compiler asked for one value, and only that value is lost.
func call(req request) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, fmt.Errorf("panic: %v", r)
		}
	}()

	fn, ok := funcs[req.Func]
	if !ok {
		return nil, fmt.Errorf("unknown function %q", req.Func)
	}
	rv := reflect.ValueOf(fn)
	rt := rv.Type()

	var in []reflect.Value
	next := 0
	// The importer strips a leading context.Context from the SNGL signature,
	// so the caller never supplies one.
	if rt.NumIn() > 0 && rt.In(0) == ctxType {
		in = append(in, reflect.ValueOf(context.Background()))
		next = 1
	}
	for _, raw := range req.Args {
		pt, err := paramType(rt, next)
		if err != nil {
			return nil, err
		}
		pv := reflect.New(pt)
		if err := json.Unmarshal(raw, pv.Interface()); err != nil {
			return nil, fmt.Errorf("argument %d: %w", next, err)
		}
		in = append(in, pv.Elem())
		next++
	}
	if want := rt.NumIn(); !rt.IsVariadic() && len(in) != want {
		return nil, fmt.Errorf("got %d arguments, want %d", len(in), want)
	}

	outs := rv.Call(in)

	// A trailing error is unwrapped, matching the importer's adapter.
	if n := rt.NumOut(); n > 0 && rt.Out(n-1) == errType {
		if e := outs[n-1].Interface(); e != nil {
			return nil, e.(error)
		}
		outs = outs[:n-1]
	}
	if len(outs) == 0 {
		return nil, nil
	}
	return outs[0].Interface(), nil
}

// paramType is the declared type of parameter i, or the variadic element type
// once i runs past the fixed parameters.
func paramType(rt reflect.Type, i int) (reflect.Type, error) {
	if i < rt.NumIn()-1 || (!rt.IsVariadic() && i < rt.NumIn()) {
		return rt.In(i), nil
	}
	if rt.IsVariadic() && i >= rt.NumIn()-1 {
		return rt.In(rt.NumIn() - 1).Elem(), nil
	}
	if i == rt.NumIn()-1 {
		return rt.In(i), nil
	}
	return nil, fmt.Errorf("too many arguments: function takes %d", rt.NumIn())
}
`

// lowerFirst lowercases the first letter of a string, matching SNGL field naming convention.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	if strings.ToUpper(s) == s {
		return strings.ToLower(s)
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// normalizeJSON converts JSON float64 numbers to int where they are whole numbers,
// and recursively normalizes nested structures.
func normalizeJSON(v any) any {
	switch val := v.(type) {
	case float64:
		if val == float64(int(val)) {
			return int(val)
		}
		return val
	case []any:
		for i, el := range val {
			val[i] = normalizeJSON(el)
		}
		return val
	case map[string]any:
		normalized := make(map[string]any, len(val))
		for k, el := range val {
			normalized[lowerFirst(k)] = normalizeJSON(el)
		}
		return normalized
	default:
		return v
	}
}
