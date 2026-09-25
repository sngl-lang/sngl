package gencache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/pkg/go/consteval"
)

// Input is one thing a generated file was derived from: a member of
// `sngl:x/gen/cache`'s input family and the props it is written with. The
// constructors below record the value an input has now; Store.stale compares
// a recorded one against the value it has then.
type Input struct {
	Kind  string
	Props []Prop
}

// Prop is one named argument of an input: a string, or a list of them.
type Prop struct {
	Name   string
	Value  string
	List   []string
	IsList bool
}

func str(name, v string) Prop           { return Prop{Name: name, Value: v} }
func list(name string, v []string) Prop { return Prop{Name: name, List: v, IsList: true} }

// Get returns the named prop's string value.
func (in Input) Get(name string) string {
	for _, p := range in.Props {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}

func (in Input) getList(name string) []string {
	for _, p := range in.Props {
		if p.Name == name {
			return p.List
		}
	}
	return nil
}

func (in Input) render(b *bytes.Buffer, alias string) {
	fmt.Fprintf(b, "%s.%s(", alias, in.Kind)
	for i, p := range in.Props {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name)
		b.WriteByte('=')
		if p.IsList {
			b.WriteByte('[')
			for j, v := range p.List {
				if j > 0 {
					b.WriteString(", ")
				}
				b.Write(consteval.AppendQuote(nil, v))
			}
			b.WriteByte(']')
			continue
		}
		b.Write(consteval.AppendQuote(nil, p.Value))
	}
	b.WriteByte(')')
}

// String is the input as the directive spells it, which is also what an
// input is memoized under.
func (in Input) String() string {
	var b bytes.Buffer
	in.render(&b, vocabAlias)
	return b.String()
}

// File records path's content. A path that is not there is an error rather
// than an absent input: a producer recording a file it is about to read
// expects one, and should say Absent where it does not.
func File(path string) (Input, error) {
	sum, err := fileSum(path)
	if err != nil {
		return Input{}, err
	}
	return Input{Kind: "file", Props: []Prop{str("path", path), str("sha256", sum)}}, nil
}

// Absent records that path does not exist.
func Absent(path string) Input {
	return Input{Kind: "absent", Props: []Prop{str("path", path)}}
}

// Dir records path's listing: which names it holds, not what is in them.
func Dir(path string) (Input, error) {
	sum, err := dirSum(path)
	if err != nil {
		return Input{}, err
	}
	return Input{Kind: "dir", Props: []Prop{str("path", path), str("sha256", sum)}}, nil
}

// GoDir records path's listing as the go command reads it: a name beginning
// with `_` or `.` is left out, since no build or directory embed pattern
// (short of `all:`) ever sees one. That is what lets a test put a scratch
// module package beside the code it builds, as the platform tests do, without
// invalidating everything recorded against the package it sits in.
func GoDir(path string) (Input, error) {
	sum, err := goDirSum(path)
	if err != nil {
		return Input{}, err
	}
	return Input{Kind: "godir", Props: []Prop{str("path", path), str("sha256", sum)}}, nil
}

// Env records an environment variable's value, or that it is unset.
func Env(name string) Input {
	if v, ok := os.LookupEnv(name); ok {
		return Input{Kind: "env", Props: []Prop{str("name", name), str("value", v)}}
	}
	return Input{Kind: "unsetenv", Props: []Prop{str("name", name)}}
}

// GoEnv records what `go env` reports in dir for each name.
func (s *Store) GoEnv(dir string, names ...string) ([]Input, error) {
	vals, err := s.goEnv(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Input, 0, len(names))
	for _, n := range names {
		v, ok := vals[n]
		if !ok {
			return nil, fmt.Errorf("gencache: go env %s is not among the variables the store asks for", n)
		}
		out = append(out, Input{Kind: "goenv", Props: []Prop{str("dir", dir), str("name", n), str("value", v)}})
	}
	return out, nil
}

// Entry records another request's output, which the store answers now -- from
// what it holds, or by producing it.
func (s *Store) Entry(req Request) (Input, []byte, error) {
	data, err := s.Get(req)
	if err != nil {
		return Input{}, nil, err
	}
	return Input{Kind: "entry", Props: []Prop{
		str("producer", req.Producer), list("params", req.Params), str("sha256", Digest(data)),
	}}, data, nil
}

// Stale reports why a stored file no longer answers its request, or "" when
// every input it records still holds. An error is a file the store cannot
// read the inputs of, which is stale too.
func (s *Store) Stale(data []byte) (string, error) {
	inputs, err := ReadInputs(data)
	if err != nil {
		return "", err
	}
	for _, in := range inputs {
		if why := s.check(in); why != "" {
			return why, nil
		}
	}
	return "", nil
}

// check re-reads one input and reports how it changed, or "" when it has not.
// A verdict is memoized for the life of the store: two entries recording one
// file ask the filesystem once.
func (s *Store) check(in Input) string {
	memo := in.String()
	s.mu.Lock()
	ok, seen := s.checks[memo]
	s.mu.Unlock()
	if seen {
		if ok {
			return ""
		}
		return memo + " changed"
	}
	why := s.recheck(in)
	s.mu.Lock()
	s.checks[memo] = why == ""
	s.mu.Unlock()
	return why
}

func (s *Store) recheck(in Input) string {
	switch in.Kind {
	case "file":
		sum, err := fileSum(in.Get("path"))
		if err != nil || sum != in.Get("sha256") {
			return in.Get("path") + " changed"
		}
	case "absent":
		if _, err := os.Lstat(in.Get("path")); !errors.Is(err, fs.ErrNotExist) {
			return in.Get("path") + " exists"
		}
	case "dir":
		sum, err := dirSum(in.Get("path"))
		if err != nil || sum != in.Get("sha256") {
			return in.Get("path") + " listing changed"
		}
	case "godir":
		sum, err := goDirSum(in.Get("path"))
		if err != nil || sum != in.Get("sha256") {
			return in.Get("path") + " listing changed"
		}
	case "env":
		if v, ok := os.LookupEnv(in.Get("name")); !ok || v != in.Get("value") {
			return "$" + in.Get("name") + " changed"
		}
	case "unsetenv":
		if _, ok := os.LookupEnv(in.Get("name")); ok {
			return "$" + in.Get("name") + " is set"
		}
	case "goenv":
		vals, err := s.goEnv(in.Get("dir"))
		if err != nil || vals[in.Get("name")] != in.Get("value") {
			return "go env " + in.Get("name") + " changed"
		}
	case "entry":
		req := Request{Producer: in.Get("producer"), Params: in.getList("params")}
		data, err := s.Get(req)
		if err != nil || Digest(data) != in.Get("sha256") {
			return req.String() + " changed"
		}
	default:
		// A kind this compiler does not know is one it cannot check, so the
		// file it guards is produced again rather than trusted.
		return "unknown input " + in.Kind
	}
	return ""
}

// ReadInputs returns the inputs a generated file's directive records. It reads
// the parsed source and nothing else: whether the file is current decides
// whether it is checked at all, so this cannot wait for the checker.
//
// Only the head of the file is parsed when the directive can be found there,
// which is where Render puts it: a stored file may be large -- the GTK
// registry is megabytes -- and deciding it is current should not cost a parse
// of the part that decision is about. A body may also hold a value in the
// encoded form only parser.ParseNativeValue reads, which a whole-file parse
// would reject.
func ReadInputs(data []byte) ([]Input, error) {
	if head := directiveHead(data); head != nil {
		if ins, err := readInputs(head); err == nil {
			return ins, nil
		}
	}
	return readInputs(data)
}

// directiveHead is data up to the end of the first inputs directive, or nil
// when it cannot be found by its rendered shape.
func directiveHead(data []byte) []byte {
	i := bytes.Index(data, []byte(".inputs {\n"))
	if i < 0 {
		return nil
	}
	j := bytes.Index(data[i:], []byte("\n}\n"))
	if j < 0 {
		return nil
	}
	return data[:i+j+3]
}

func readInputs(data []byte) ([]Input, error) {
	doc, err := parser.Parse("gencache", data)
	if err != nil {
		return nil, err
	}
	alias := ""
	for _, st := range doc.Stmts {
		if imp, ok := st.(*ast.Import); ok && imp.Path == VocabPath {
			alias = imp.Alias
		}
	}
	if alias == "" || alias == "." {
		return nil, errors.New("gencache: file does not import " + VocabPath + " under a name")
	}
	for _, st := range doc.Stmts {
		vn, ok := st.(*ast.VisualNode)
		if !ok || vn.TargetName() != alias+".inputs" {
			continue
		}
		var out []Input
		for _, s := range vn.Block.Stmts {
			in, err := readInput(s, alias)
			if err != nil {
				return nil, err
			}
			out = append(out, in)
		}
		return out, nil
	}
	return nil, errors.New("gencache: file has no " + alias + ".inputs directive")
}

// ParseInputs reads inputs written one per line in the directive's own
// spelling -- `cache.file(path="…", sha256="…")` -- as a process reports the
// ones it used (see Store.Used).
func ParseInputs(lines []string) ([]Input, error) {
	if len(lines) == 0 {
		return nil, nil
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "import %s %q\n\n%s.inputs {\n", vocabAlias, VocabPath, vocabAlias)
	for _, l := range lines {
		b.WriteString("    " + l + "\n")
	}
	b.WriteString("}\n")
	return readInputs(b.Bytes())
}

func readInput(s ast.Stmt, alias string) (Input, error) {
	var name string
	var args ast.ArgList
	switch n := s.(type) {
	case *ast.VisualNode:
		name, args = n.TargetName(), n.Args
	case *ast.CallStmt:
		sel, ok := n.Call.Func.(*ast.SelectExpr)
		if id, isID := selOperand(sel); ok && isID {
			name = id + "." + sel.Field
		}
		args = n.Call.Args
	}
	kind, ok := strings.CutPrefix(name, alias+".")
	if !ok {
		return Input{}, fmt.Errorf("gencache: %T in %s.inputs is not an input", s, alias)
	}
	in := Input{Kind: kind}
	for _, a := range args.Args {
		var arg ast.Arg
		switch v := a.(type) {
		case ast.Arg:
			arg = v
		case *ast.Arg:
			arg = *v
		default:
			return Input{}, fmt.Errorf("gencache: %s.%s has an argument that is not a value", alias, kind)
		}
		p, err := readProp(arg)
		if err != nil {
			return Input{}, fmt.Errorf("gencache: %s.%s: %w", alias, kind, err)
		}
		in.Props = append(in.Props, p)
	}
	return in, nil
}

func selOperand(sel *ast.SelectExpr) (string, bool) {
	if sel == nil {
		return "", false
	}
	id, ok := sel.Operand.(*ast.IdentExpr)
	if !ok {
		return "", false
	}
	return id.Name, true
}

func readProp(a ast.Arg) (Prop, error) {
	if a.Name == "" {
		return Prop{}, errors.New("an input's arguments are named")
	}
	switch v := a.Value.(type) {
	case *ast.LiteralExpr:
		s, ok := v.StringValue()
		if !ok {
			return Prop{}, fmt.Errorf("%s is not a string", a.Name)
		}
		return str(a.Name, s), nil
	case *ast.ListExpr:
		var xs []string
		for _, e := range v.Elements {
			lit, ok := e.(*ast.LiteralExpr)
			if !ok {
				return Prop{}, fmt.Errorf("%s is not a list of strings", a.Name)
			}
			s, ok := lit.StringValue()
			if !ok {
				return Prop{}, fmt.Errorf("%s is not a list of strings", a.Name)
			}
			xs = append(xs, s)
		}
		return list(a.Name, xs), nil
	}
	return Prop{}, fmt.Errorf("%s is not a literal", a.Name)
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// dirSum hashes a directory's sorted entry names, a subdirectory marked with
// a trailing slash so that a file replaced by a directory of the same name is
// a change.
func dirSum(path string) (string, error) {
	return listingSum(path, func(string) bool { return true })
}

// goDirSum is dirSum over the names the go command does not ignore.
func goDirSum(path string) (string, error) {
	return listingSum(path, func(name string) bool {
		return !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, ".")
	})
}

func listingSum(path string, keep func(name string) bool) (string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if !keep(e.Name()) {
			continue
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%s\x00", n)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// GoEnvNames is every variable the store asks `go env` for: which toolchain,
// for which target, with which flags and cgo configuration, resolving modules
// from where. One query per directory answers every goenv input recorded
// there, so the list is fixed rather than per caller.
var GoEnvNames = []string{
	"GOVERSION", "GOROOT", "GOOS", "GOARCH", "GOFLAGS", "GOEXPERIMENT",
	"GOAMD64", "GOARM", "GOARM64", "GO386", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64", "GOWASM",
	"CGO_ENABLED", "CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS",
	"GOMOD", "GOWORK", "GOMODCACHE", "GOFIPS140",
}

type goEnvResult struct {
	vals map[string]string
	err  error
}

func (s *Store) goEnv(dir string) (map[string]string, error) {
	s.mu.Lock()
	r, ok := s.goenv[dir]
	s.mu.Unlock()
	if ok {
		return r.vals, r.err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", append([]string{"env", "-json"}, GoEnvNames...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		err = json.Unmarshal(out, &r.vals)
	}
	if err != nil {
		r.err = fmt.Errorf("go env in %s: %w", dir, err)
	}
	s.mu.Lock()
	s.goenv[dir] = r
	s.mu.Unlock()
	return r.vals, r.err
}
