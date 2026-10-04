// Package buildhost is how SNGL run at build time reads the machine the build
// runs on: sngl:x/gen's lines, exists, files, env and exec. Two things run
// there -- a const func the optimizer folds, and a gen.scheme's @generate
// handler the checker runs to resolve an import -- and both are producers:
// what they compute is stored between builds behind what they read and the
// code that read it. Each call is gated (internal/trust) by the package that
// made it, and each read is recorded.
package buildhost

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/gencache"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/trust"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Error is a host call that failed: a refusal, or a read that could not
// be made. Unlike a fold that simply could not finish, it stops the build,
// since no target can make the call at run time instead.
type Error struct {
	pos ast.Pos
	err error
}

func (e *Error) Error() string {
	if e.pos.IsValid() {
		return e.pos.String() + ": " + e.err.Error()
	}
	return e.err.Error()
}

func (e *Error) Unwrap() error { return e.err }

// Pos is where the failing call is written, when it is known.
func (e *Error) Pos() ast.Pos { return e.pos }

// Err is the failure without its position.
func (e *Error) Err() error { return e.err }

// Owner is a package as the host sees it: who is asking, and what its code is.
type Owner struct {
	pkg     *ir.Package
	library bool
	// name is the package as a flag spells it: its import path from the root.
	name string
	// dir is where it is on disk, canonical; "" for a fetched package.
	dir    string
	uri    string
	digest string // the closure's, once asked
}

// subject is the owner as the trust gate sees it.
func (o *Owner) Subject() trust.Subject {
	if o == nil {
		return trust.Subject{}
	}
	s := trust.Subject{Name: o.name, Library: o.library}
	if o.dir != "" {
		s.Origin = trust.DirOrigin(o.dir)
	} else if o.uri != "" {
		s.Origin = trust.Origin{Spec: o.uri, Digest: o.Closure()}
	}
	return s
}

// closure is the digest of the owner's code and the code of every package it
// imports that is not compiled into the compiler: what a stored answer from
// it depends on that no recorded input could say.
func (o *Owner) Closure() string {
	if o.digest == "" {
		o.digest = ClosureDigest(o.pkg, map[*ir.Package]string{})
	}
	return o.digest
}

func ClosureDigest(pkg *ir.Package, memo map[*ir.Package]string) string {
	if d, ok := memo[pkg]; ok {
		return d
	}
	memo[pkg] = "" // a cycle contributes nothing twice
	h := sha256.New()
	if pkg.Origin != nil {
		var files []string
		for _, doc := range pkg.Origin.Docs {
			files = append(files, parser.Format(doc))
		}
		sort.Strings(files)
		for _, f := range files {
			fmt.Fprintf(h, "%d:%s\x00", len(f), f)
		}
	}
	var deps []string
	for _, imp := range pkg.Imports {
		switch {
		case imp.Native != nil:
			deps = append(deps, "native:"+imp.Path)
		case imp.Pkg != nil && imp.Pkg.Origin != nil:
			deps = append(deps, ClosureDigest(imp.Pkg, memo))
		}
	}
	sort.Strings(deps)
	for _, d := range deps {
		fmt.Fprintf(h, "%s\x00", d)
	}
	d := hex.EncodeToString(h.Sum(nil))
	memo[pkg] = d
	return d
}

// NewOwner is a package the caller has already placed: a plugin a check is
// running, which it knows the origin of better than an ir.PackageOrigin says.
func NewOwner(pkg *ir.Package, name, dir, uri string, library bool) *Owner {
	o := &Owner{pkg: pkg, name: name, uri: uri, library: library}
	if dir != "" {
		o.dir = trust.Canonical(dir)
	}
	return o
}

// Name is the package as a flag spells it.
func (o *Owner) Name() string { return o.name }

// Dir is where the package is on disk, canonical; "" for a fetched one.
func (o *Owner) Dir() string { return o.dir }

// Host answers sngl:x/gen for one Optimize run, or one scheme handler.
type Host struct {
	trust  *trust.Policy
	root   string // the import root, canonical
	owners map[*ir.Func]*Owner
	rootOw *Owner
	// rec is the producer recording now; nil outside one.
	rec *Recorder
	// out is what a scheme handler writes; nil outside one.
	out *Output
}

// New indexes which package declares each function reachable from root,
// which is who a host call written in it is asked of. dir is the import root,
// and rootOwner is root as the host should see it, or nil to derive it from
// root's origin.
func New(policy *trust.Policy, dir string, root *ir.Package, rootOwner *Owner) *Host {
	h := &Host{trust: policy, owners: map[*ir.Func]*Owner{}}
	if dir != "" {
		h.root = trust.Canonical(dir)
	}
	seen := map[*ir.Package]bool{}
	var walk func(pkg *ir.Package, ow *Owner)
	walk = func(pkg *ir.Package, ow *Owner) {
		if pkg == nil || seen[pkg] {
			return
		}
		seen[pkg] = true
		for _, f := range pkg.Funcs {
			h.owners[f] = ow
		}
		for _, c := range pkg.Components {
			for _, f := range c.Funcs {
				h.owners[f] = ow
			}
		}
		for _, imp := range pkg.Imports {
			if imp.Pkg == nil || imp.Native != nil {
				continue
			}
			switch {
			case strings.HasPrefix(imp.Path, "sngl:"):
				walk(imp.Pkg, &Owner{pkg: imp.Pkg, library: true, name: imp.Path})
			case imp.Pkg.Origin == nil:
				// Nothing says where it came from, so nothing may be granted
				// to it: its name is the import's, and it has no origin a
				// recorded grant could match.
				walk(imp.Pkg, &Owner{pkg: imp.Pkg, name: imp.Path})
			default:
				walk(imp.Pkg, h.OwnerOf(imp.Pkg))
			}
		}
	}
	h.rootOw = rootOwner
	if h.rootOw == nil {
		h.rootOw = h.OwnerOf(root)
	}
	walk(root, h.rootOw)
	return h
}

// Recording makes rec what host calls record into until the returned func
// puts the previous recorder back.
func (h *Host) Recording(rec *Recorder) func() {
	outer := h.rec
	h.rec = rec
	return func() { h.rec = outer }
}

// Recorder is the producer recording now, or nil.
func (h *Host) Recorder() *Recorder { return h.rec }

// Writing makes out what a handler's out.write calls write.
func (h *Host) Writing(out *Output) { h.out = out }

// OwnerOf is pkg as the host sees it, by its origin.
func (h *Host) OwnerOf(pkg *ir.Package) *Owner {
	o := &Owner{pkg: pkg}
	switch {
	case pkg.Origin == nil:
		o.name = "."
	case pkg.Origin.URI != "":
		o.name, o.uri = pkg.Origin.URI, pkg.Origin.URI
	default:
		o.name = pkg.Origin.Dir
		if !strings.HasPrefix(o.name, ".") {
			o.name = "./" + o.name
		}
		if h.root != "" {
			o.dir = trust.Canonical(filepath.Join(h.root, filepath.FromSlash(pkg.Origin.Dir)))
		}
	}
	return o
}

// OwnerFor is the package that declares fn, which a call written in it is asked of.
func (h *Host) OwnerFor(fn *ir.Func) *Owner {
	if o, ok := h.owners[fn]; ok {
		return o
	}
	return h.rootOw
}

// Call answers one host call.
func (h *Host) Call(id string, caller *ir.Func, call *ir.Call, args []any) (any, error) {
	ow := h.OwnerFor(caller)
	pos := h.position(ow, call)
	v, err := h.call(id, ow, call, args)
	if err != nil {
		if _, ok := errors.AsType[*Error](err); ok {
			return nil, err
		}
		return nil, &Error{pos: pos, err: err}
	}
	return v, nil
}

// position is where call is written, with the file named from the import
// root as a flag names the package.
func (h *Host) position(ow *Owner, call *ir.Call) ast.Pos {
	if call == nil || call.AST == nil {
		return ast.Pos{}
	}
	pos := CallStart(call.AST)
	if ow != nil && ow.pkg != nil && ow.pkg.Origin != nil && ow.pkg.Origin.Dir != "" && ow.pkg.Origin.Dir != "." && pos.File != "" {
		pos.File = path.Join(ow.pkg.Origin.Dir, path.Base(filepath.ToSlash(pos.File)))
	}
	return pos
}

func (h *Host) call(id string, ow *Owner, call *ir.Call, args []any) (any, error) {
	rec := h.rec
	if rec == nil {
		return nil, fmt.Errorf("%s answers only inside a const func the build evaluates, or a gen.scheme or gen.emit @generate handler", id)
	}
	str := func(i int) string {
		if i < len(args) {
			s, _ := args[i].(string)
			return s
		}
		return ""
	}
	strs := func(i int) []string {
		var out []string
		if i < len(args) {
			xs, _ := args[i].([]any)
			for _, x := range xs {
				s, _ := x.(string)
				out = append(out, s)
			}
		}
		return out
	}
	switch id {
	case "gen.lines":
		p, err := h.readable(ow, str(0), trust.File)
		if err != nil {
			return nil, err
		}
		return rec.lines(p)
	case "gen.exists":
		p, err := h.readable(ow, str(0), trust.File)
		if err != nil {
			return nil, err
		}
		return rec.exists(p)
	case "gen.files":
		dir, err := h.readable(ow, str(0), trust.Dir)
		if err != nil {
			return nil, err
		}
		return rec.listing(dir, str(1))
	case "gen.env":
		name := str(0)
		if err := h.trust.Check(trust.Request{Kind: trust.Env, Subject: ow.Subject(), Value: name}); err != nil {
			return nil, err
		}
		rec.Add(gencache.Env(name))
		if v, ok := os.LookupEnv(name); ok {
			return v, nil
		}
		return nil, nil
	case "gen.exec":
		return h.exec(rec, ow, call, strs(0), strs(1), strs(2), str(3), str(4))
	case "gen.Out.write":
		if h.out == nil {
			return nil, errors.New("out.write answers only in a gen.scheme's or a gen.emit's @generate handler")
		}
		// args[0] is the receiver.
		return nil, h.out.write(str(1), str(2))
	case "gen.Process.code":
		p, _ := args[0].(*interp.Struct)
		st, _ := p.Get("stdout")
		proc := rec.procs[streamOf(st)]
		if proc == nil {
			return nil, errors.New("gen.Process.code: not a process this build started")
		}
		return proc.wait()
	}
	return nil, fmt.Errorf("%s: no build host answers it", id)
}

func streamOf(v any) *interp.Stream {
	s, _ := v.(*interp.Stream)
	return s
}

// readable resolves p against the import root and asks whether ow may read
// it: inside the root, or inside ow's own package, needs nothing.
func (h *Host) readable(ow *Owner, p string, kind trust.Kind) (string, error) {
	if h.root == "" {
		return "", errors.New("there is no import root to read from")
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(h.root, filepath.FromSlash(p))
	}
	abs = trust.Canonical(abs)
	if trust.Within(abs, h.root) || ow.dir != "" && trust.Within(abs, ow.dir) {
		return abs, nil
	}
	return abs, h.trust.Check(trust.Request{Kind: kind, Subject: ow.Subject(), Value: abs, Root: h.root})
}

// Key is what names ow's code in a stored answer: where it came from, and the
// import root it ran under. The root is part of it because a relative read
// resolves against it and a read inside it is unasked, so two projects
// importing one plugin or one shared package may each get a different answer
// from the same call.
func (h *Host) Key(ow *Owner) []string {
	where := ow.Name()
	if ow.Dir() != "" {
		where = "dir:" + ow.Dir()
	}
	return []string{where, "root:" + h.root}
}

// Declared gates an input ow's output names in a literal cache.inputs: the
// store completes and later re-checks it by reading what it names, so it is a
// read ow makes, held to what ow may read. A file or directory is a read like
// gen.lines' or gen.files', a variable is gen.env's, and a go env value or
// another producer's output runs a process in a directory ow chose, which only
// a library may ask.
func (h *Host) Declared(ow *Owner, in gencache.Input) error {
	switch in.Kind {
	case "file", "godir", "absent":
		_, err := h.readable(ow, in.Get("path"), trust.File)
		return err
	case "dir":
		_, err := h.readable(ow, in.Get("path"), trust.Dir)
		return err
	case "env", "unsetenv":
		return h.trust.Check(trust.Request{Kind: trust.Env, Subject: ow.Subject(), Value: in.Get("name")})
	case "goenv", "entry":
		if ow.library {
			return nil
		}
		return fmt.Errorf("cache.%s is the library's to declare: it runs a process, and a plugin's own output may not ask for one", in.Kind)
	}
	return nil
}

func (h *Host) exec(rec *Recorder, ow *Owner, call *ir.Call, cmd, prefix, banFlags []string, dir, stdin string) (any, error) {
	if len(cmd) == 0 {
		return nil, errors.New("gen.exec: no command")
	}
	if len(prefix) == 0 {
		prefix = cmd[:1]
	}
	if len(prefix) > len(cmd) || !slices.Equal(prefix, cmd[:len(prefix)]) {
		return nil, fmt.Errorf("gen.exec: [%s] does not begin with its declared prefix [%s]", trust.ShellQuote(cmd), trust.ShellQuote(prefix))
	}
	for _, arg := range cmd[1:] {
		if ban, ok := bannedFlag(arg, banFlags); ok {
			return nil, fmt.Errorf("gen.exec: argument %q is a flag the call bans (%s)", arg, ban)
		}
	}
	if err := h.trust.Check(trust.Request{Kind: trust.Command, Subject: ow.Subject(), Cmd: cmd, Prefix: prefix, BanFlags: banFlags, Root: h.root}); err != nil {
		return nil, err
	}
	bin, err := exec.LookPath(cmd[0])
	if err != nil {
		return nil, fmt.Errorf("gen.exec: %w", err)
	}
	if abs, err := filepath.Abs(bin); err == nil {
		bin = abs
	}
	in, err := gencache.File(bin)
	if err != nil {
		return nil, fmt.Errorf("gen.exec: %w", err)
	}
	rec.Add(in)
	runDir := h.root
	if dir != "" {
		// The directory a command runs in is what its relative paths read,
		// so it is held to what the plugin may list.
		d, err := h.readable(ow, dir, trust.Dir)
		if err != nil {
			return nil, err
		}
		runDir = d
	}
	c := exec.Command(bin, cmd[1:]...)
	c.Dir = runDir
	c.Env = ChildEnv()
	// What it reads on stdin is the call's own argument, so it is part of
	// what the value is stored under and needs recording no more than cmd.
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("gen.exec: %w", err)
	}
	slog.Info("exec", "cmd", trust.ShellQuote(cmd), "dir", runDir, "for", ow.name)
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("gen.exec: %w", err)
	}
	p := &process{cmd: c, out: bufio.NewReader(out), stderr: &stderr, argv: cmd}
	st := &interp.Stream{
		Next: func() (any, bool, error) { return p.next() },
		Stop: func() error { return nil },
	}
	rec.procs[st] = p
	var def *ir.StructDef
	var typ *ir.Type
	if call != nil && call.Func != nil && call.Func.Return != nil {
		typ = call.Func.Return
		def, _ = typ.Decl.(*ir.StructDef)
	}
	v := interp.NewStruct(def, typ)
	v.Fields = []interp.Field{{Name: "stdout", Value: st}}
	return v, nil
}

// bannedFlag reports whether arg is one of bans in any spelling: one dash or
// two, alone or with `=value`.
func bannedFlag(arg string, bans []string) (string, bool) {
	if !strings.HasPrefix(arg, "-") {
		return "", false
	}
	name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
	for _, b := range bans {
		if strings.TrimLeft(b, "-") == name {
			return b, true
		}
	}
	return "", false
}

// ChildEnv is the environment every process the build starts gets: the
// build's, less the grants -- an allowed child must not be able to hand them
// to a nested build in some other project.
func ChildEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, trust.EnvVar+"=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// process is a command gen.exec started.
type process struct {
	cmd    *exec.Cmd
	out    *bufio.Reader
	stderr *bytes.Buffer
	argv   []string
	done   bool
	code   int
	err    error
}

func (p *process) next() (any, bool, error) {
	line, err := p.out.ReadString('\n')
	if line != "" {
		return strings.TrimRight(line, "\r\n"), true, nil
	}
	if err == io.EOF {
		return nil, false, nil
	}
	return nil, false, err
}

// wait reads what is left and waits for the process to exit.
func (p *process) wait() (any, error) {
	if !p.done {
		p.done = true
		io.Copy(io.Discard, p.out)
		p.err = p.cmd.Wait()
		var exit *exec.ExitError
		switch {
		case errors.As(p.err, &exit):
			p.code, p.err = exit.ExitCode(), nil
		case p.err != nil:
			p.err = fmt.Errorf("gen.exec %s: %w", trust.ShellQuote(p.argv), p.err)
		}
		if s := strings.TrimSpace(p.stderr.String()); s != "" {
			slog.Debug("gen.exec stderr", "cmd", trust.ShellQuote(p.argv), "stderr", s)
		}
	}
	return p.code, p.err
}

// Output is the files a handler wrote, in the order it wrote them. It records
// nothing: it is the output.
type Output struct {
	// Paths says the handler is a family's emitter, which writes into the
	// output directory: a name is any path inside it. A scheme's writes a
	// package, whose files are .sngl names with no directory.
	Paths bool
	Names []string
	Files map[string]string
}

func (o *Output) write(name, src string) error {
	if o.Paths {
		clean := path.Clean(name)
		if name == "" || path.IsAbs(name) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("out.write: %q is not a path inside the output directory", name)
		}
		name = clean
	} else if name == "" || path.Base(name) != name || !strings.HasSuffix(name, ".sngl") {
		return fmt.Errorf("out.write: %q is not a file name: want a .sngl name with no directory", name)
	}
	if o.Files == nil {
		o.Files = map[string]string{}
	}
	if _, dup := o.Files[name]; dup {
		return fmt.Errorf("out.write: %s written twice", name)
	}
	o.Names = append(o.Names, name)
	o.Files[name] = src
	return nil
}

// NewRecorder is a recorder with nothing recorded.
func NewRecorder() *Recorder { return &Recorder{procs: map[*interp.Stream]*process{}} }

// Recorder collects what one producer read.
type Recorder struct {
	inputs []gencache.Input
	files  []*fileStream
	procs  map[*interp.Stream]*process
	// unrecordable is set when a read could not be recorded, so the value
	// is computed but not stored.
	unrecordable bool
}

func (r *Recorder) Add(ins ...gencache.Input) { r.inputs = append(r.inputs, ins...) }

func (r *Recorder) lines(p string) (any, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("gen.lines: %w", err)
	}
	fs := &fileStream{path: p, f: f, r: bufio.NewReader(f), h: sha256.New()}
	r.files = append(r.files, fs)
	return &interp.Stream{Next: fs.next, Stop: fs.drain}, nil
}

func (r *Recorder) exists(p string) (any, error) {
	info, err := os.Stat(p)
	switch {
	case err != nil:
		r.Add(gencache.Absent(p))
		return false, nil
	case info.IsDir():
		in, err := gencache.Dir(p)
		if err != nil {
			return nil, fmt.Errorf("gen.exists: %w", err)
		}
		r.Add(in)
	default:
		in, err := gencache.File(p)
		if err != nil {
			return nil, fmt.Errorf("gen.exists: %w", err)
		}
		r.Add(in)
	}
	return true, nil
}

func (r *Recorder) listing(dir, pattern string) (any, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, fmt.Errorf("gen.files: %q: %w", pattern, err)
	}
	in, err := gencache.Dir(dir)
	if err != nil {
		return nil, fmt.Errorf("gen.files: %w", err)
	}
	r.Add(in)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("gen.files: %w", err)
	}
	out := []any{}
	for _, e := range entries {
		if ok, _ := path.Match(pattern, e.Name()); ok {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// finish completes every read the producer left open -- a file stream it
// stopped taking, a process it never waited for -- and returns the inputs.
func (r *Recorder) Finish() ([]gencache.Input, error) {
	var errs []error
	for _, f := range r.files {
		if err := f.drain(); err != nil {
			errs = append(errs, err)
			continue
		}
		r.Add(gencache.Input{Kind: "file", Props: []gencache.Prop{{Name: "path", Value: f.path}, {Name: "sha256", Value: hex.EncodeToString(f.h.Sum(nil))}}})
	}
	for _, p := range r.procs {
		if _, err := p.wait(); err != nil {
			errs = append(errs, err)
		}
	}
	return r.inputs, errors.Join(errs...)
}

// fileStream hands a file out a line at a time and hashes every byte it
// reads, so what it records is what was read.
type fileStream struct {
	path string
	f    *os.File
	r    *bufio.Reader
	h    interface {
		io.Writer
		Sum([]byte) []byte
	}
	done bool
}

func (s *fileStream) next() (any, bool, error) {
	if s.done {
		return nil, false, nil
	}
	line, err := s.r.ReadString('\n')
	s.h.Write([]byte(line))
	if err == io.EOF {
		s.done = true
		s.f.Close()
		if line == "" {
			return nil, false, nil
		}
	} else if err != nil {
		return nil, false, err
	}
	return strings.TrimRight(line, "\r\n"), true, nil
}

// drain reads the rest without handing it out, so the file is recorded whole.
func (s *fileStream) drain() error {
	if s.done {
		return nil
	}
	s.done = true
	defer s.f.Close()
	_, err := io.Copy(s.h, s.r)
	return err
}

// CallStart is where a call is written: the start of what it calls, which is
// where a reader looks, rather than its parenthesis.
func CallStart(c *ast.CallExpr) ast.Pos {
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
