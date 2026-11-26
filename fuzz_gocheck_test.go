package sngl_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
)

// checkGoFiles type-checks every Go package among files, the generated output
// of one build keyed by path. A package the output imports from this module or
// a dependency is read from its export data, which `go list -export` builds
// once per process; one the output itself holds is checked from its source;
// anything else -- a project's own go: module, a cgo package this host cannot
// build -- is an empty stand-in, whose selectors are let through.
func checkGoFiles(files map[string][]byte, goos string) error {
	fset := token.NewFileSet()
	type pkgKey struct{ dir, name string }
	pkgs := map[pkgKey][]*ast.File{}
	modules := map[string]string{} // directory of a go.mod -> its module path
	for name, data := range files {
		if path.Base(name) == "go.mod" {
			if p := modfile.ModulePath(data); p != "" {
				modules[path.Dir(name)] = p
			}
		}
	}
	for name, data := range files {
		if path.Ext(name) != ".go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, data, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		if !buildsFor(f, goos) {
			continue
		}
		k := pkgKey{path.Dir(name), f.Name.Name}
		pkgs[k] = append(pkgs[k], f)
	}
	if len(pkgs) == 0 {
		return nil
	}

	imp := &goImporter{fset: fset, local: map[string][]*ast.File{}, checked: map[string]*types.Package{}, fake: map[string]bool{}}
	for k, fs := range pkgs {
		if strings.HasSuffix(k.name, "_test") {
			continue
		}
		for dir, mod := range modules {
			if rel, ok := within(k.dir, dir); ok {
				imp.local[path.Join(mod, rel)] = fs
			}
		}
	}
	imp.gc = importer.ForCompiler(fset, "gc", exportFile)

	var errs []string
	keys := make([]pkgKey, 0, len(pkgs))
	for k := range pkgs {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b pkgKey) int { return strings.Compare(a.dir+a.name, b.dir+b.name) })
	for _, k := range keys {
		conf := imp.config(&errs)
		_, _ = conf.Check(k.name, fset, pkgs[k], nil)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}
	return nil
}

// within reports dir's path relative to root, when it is under it.
func within(dir, root string) (string, bool) {
	if dir == root {
		return ".", true
	}
	if strings.HasPrefix(dir, root+"/") {
		return dir[len(root)+1:], true
	}
	return "", false
}

// buildsFor is whether f's build constraint holds for goos on amd64 with cgo,
// the host the target's program is built for.
func buildsFor(f *ast.File, goos string) bool {
	for _, g := range f.Comments {
		if g.Pos() >= f.Package {
			break
		}
		for _, c := range g.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return true
			}
			return expr.Eval(func(tag string) bool {
				return tag == goos || (tag == "unix" && goos != "windows") || tag == "cgo" || tag == "amd64" || strings.HasPrefix(tag, "go1.")
			})
		}
	}
	return true
}

type goImporter struct {
	fset    *token.FileSet
	gc      types.Importer
	local   map[string][]*ast.File
	checked map[string]*types.Package
	// fake names the stand-ins, by the name a file refers to them by.
	fake map[string]bool
	errs *[]string
}

func (g *goImporter) config(errs *[]string) types.Config {
	g.errs = errs
	return types.Config{
		Importer:    g,
		FakeImportC: true,
		Error: func(err error) {
			msg := err.Error()
			if g.standIn(msg) {
				return
			}
			*errs = append(*errs, msg)
		},
	}
}

// standIn is whether msg is about a member of a stand-in package, which has
// none.
func (g *goImporter) standIn(msg string) bool {
	_, rest, ok := strings.Cut(msg, "undefined: ")
	if !ok {
		return false
	}
	pkg, _, ok := strings.Cut(rest, ".")
	return ok && g.fake[pkg]
}

func (g *goImporter) Import(p string) (*types.Package, error) {
	if pkg, ok := g.checked[p]; ok {
		return pkg, nil
	}
	if fs, ok := g.local[p]; ok {
		conf := g.config(g.errs)
		pkg, _ := conf.Check(p, g.fset, fs, nil)
		g.checked[p] = pkg
		return pkg, nil
	}
	if pkg, err := g.gc.Import(p); err == nil {
		g.checked[p] = pkg
		return pkg, nil
	}
	name := path.Base(p)
	if len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		name = path.Base(path.Dir(p))
	}
	name = strings.NewReplacer("-", "", ".", "").Replace(name)
	pkg := types.NewPackage(p, name)
	pkg.MarkComplete()
	g.fake[name] = true
	g.checked[p] = pkg
	return pkg, nil
}

var exports = struct {
	sync.Mutex
	file map[string]string // import path -> export data, "" where go list had none
}{file: map[string]string{}}

// exportFile is the gc importer's lookup: the export data `go list -export`
// builds for path and everything it imports, listed once and remembered.
func exportFile(p string) (io.ReadCloser, error) {
	exports.Lock()
	defer exports.Unlock()
	f, ok := exports.file[p]
	if !ok {
		out, _ := exec.Command("go", "list", "-e", "-export", "-deps", "-f", "{{.ImportPath}}\t{{.Export}}", p).Output()
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			ip, exp, _ := strings.Cut(sc.Text(), "\t")
			if _, seen := exports.file[ip]; !seen || exp != "" {
				exports.file[ip] = exp
			}
		}
		if _, ok := exports.file[p]; !ok {
			exports.file[p] = ""
		}
		f = exports.file[p]
	}
	if f == "" {
		return nil, fmt.Errorf("no export data for %s", p)
	}
	return os.Open(f)
}
