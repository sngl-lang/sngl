//go:build !js

// Package godeps is the "go.deps" producer: what a set of Go packages and
// everything they import were built from.
//
// It generates no declarations. Its output is the inputs alone -- every file
// the go command would compile or embed, every directory whose listing decides
// which files those are, and the toolchain configuration -- so that another
// entry built from those packages can depend on them with one `entry` input
// rather than restating hundreds of files. A consteval result is the first
// such entry; the SNGL a go: import generates will be the next.
//
// What is recorded is what the Go toolchain does not already pin by version:
// a package in GOROOT is fixed by GOVERSION and GOROOT, and one in the module
// cache by the go.mod and go.sum that selected it, which are recorded. Every
// other package -- the main module's, a replaced module on disk, a vendored
// copy -- is recorded file by file.
package godeps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"duckfam.us/sngl/internal/gencache"
)

// Producer is the name the producer is registered under.
const Producer = "go.deps"

func init() { gencache.Register(Producer, produce) }

// Request is the go.deps request for roots, as seen from the module in dir.
// dir is made absolute and the roots sorted, so two callers asking about the
// same packages ask one question.
func Request(dir string, roots []string) (gencache.Request, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return gencache.Request{}, err
	}
	roots = slices.Clone(roots)
	slices.Sort(roots)
	roots = slices.Compact(roots)
	return gencache.Request{Producer: Producer, Params: append([]string{abs}, roots...)}, nil
}

// recentEdit is how new a file may be and still be recorded. `go list` reads
// a package's imports before this producer hashes its files, so a file edited
// in between would be recorded with content the listing did not see; refusing
// to store anything that fresh turns that race into a repeat.
const recentEdit = 2 * time.Second

func produce(s *gencache.Store, params []string) (gencache.Output, error) {
	if len(params) < 2 {
		return gencache.Output{}, errors.New("go.deps: want a directory and at least one package")
	}
	dir, roots := params[0], params[1:]
	start := time.Now()

	inputs, err := s.GoEnv(dir, gencache.GoEnvNames...)
	if err != nil {
		return gencache.Output{}, err
	}
	env := map[string]string{}
	for _, in := range inputs {
		env[in.Get("name")] = in.Get("value")
	}

	pkgs, err := list(dir, roots)
	if err != nil {
		return gencache.Output{}, err
	}

	var files, dirs []string
	// allNames holds the directories whose every name counts: an `all:` embed
	// pattern embeds the `_` and `.` names the go command otherwise ignores, so
	// only there does one change the build.
	allNames := map[string]bool{}
	modFiles := map[string]bool{}
	if gomod := env["GOMOD"]; gomod != "" && gomod != os.DevNull {
		modFiles[gomod] = true
		modFiles[strings.TrimSuffix(gomod, ".mod")+".sum"] = true
		modFiles[filepath.Join(filepath.Dir(gomod), "vendor", "modules.txt")] = true
	}
	if work := env["GOWORK"]; work != "" && work != "off" {
		modFiles[work] = true
		modFiles[work+".sum"] = true
	}
	modCache := env["GOMODCACHE"]
	for _, p := range pkgs {
		if p.Standard || p.Dir == "" || (modCache != "" && inDir(p.Dir, modCache)) {
			continue
		}
		if p.Module != nil && p.Module.GoMod != "" && !inDir(p.Module.GoMod, modCache) {
			modFiles[p.Module.GoMod] = true
		}
		dirs = append(dirs, p.Dir)
		embedsAll := slices.ContainsFunc(p.EmbedPatterns, func(pat string) bool { return strings.HasPrefix(pat, "all:") })
		if embedsAll {
			allNames[p.Dir] = true
		}
		for _, group := range [][]string{
			p.GoFiles, p.CgoFiles, p.CFiles, p.CXXFiles, p.MFiles, p.HFiles, p.FFiles, p.SFiles,
			p.SwigFiles, p.SwigCXXFiles, p.SysoFiles, p.IgnoredGoFiles, p.IgnoredOtherFiles,
		} {
			for _, f := range group {
				files = append(files, filepath.Join(p.Dir, f))
			}
		}
		for _, f := range p.EmbedFiles {
			path := filepath.Join(p.Dir, f)
			files = append(files, path)
			// A pattern naming a directory embeds whatever is in it, so every
			// directory between the package and the file is a listing the
			// output depends on.
			for d := filepath.Dir(path); d != p.Dir && inDir(d, p.Dir); d = filepath.Dir(d) {
				dirs = append(dirs, d)
				if embedsAll {
					allNames[d] = true
				}
			}
		}
	}

	noStore := false
	for _, d := range sortedUnique(dirs) {
		record := gencache.GoDir
		if allNames[d] {
			record = gencache.Dir
		}
		in, err := record(d)
		if err != nil {
			return gencache.Output{}, err
		}
		inputs = append(inputs, in)
	}
	for _, f := range sortedUnique(append(files, keys(modFiles)...)) {
		in, err := gencache.File(f)
		switch {
		case errors.Is(err, fs.ErrNotExist) && modFiles[f]:
			// go.sum, vendor/modules.txt and go.work.sum are each optional,
			// and each changes the build when it appears.
			inputs = append(inputs, gencache.Absent(f))
			continue
		case err != nil:
			return gencache.Output{}, err
		}
		if info, err := os.Stat(f); err == nil && info.ModTime().After(start.Add(-recentEdit)) {
			noStore = true
		}
		inputs = append(inputs, in)
	}

	var body bytes.Buffer
	body.WriteString("// The packages this closure holds, for a reader; the store reads only\n// the inputs above.\nconst packages = [\n")
	for _, p := range pkgs {
		fmt.Fprintf(&body, "    %q,\n", p.ImportPath)
	}
	body.WriteString("]\n")
	return gencache.Output{Inputs: inputs, Body: body.Bytes(), NoStore: noStore}, nil
}

// pkg is the part of `go list -json` this producer reads.
type pkg struct {
	ImportPath string
	Dir        string
	Standard   bool
	Module     *struct{ GoMod string }

	GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles []string
	SwigFiles, SwigCXXFiles, SysoFiles, EmbedFiles, EmbedPatterns       []string
	IgnoredGoFiles, IgnoredOtherFiles                                   []string

	Error *struct{ Err string }
}

const listFields = "ImportPath,Dir,Standard,Module,GoFiles,CgoFiles,CFiles,CXXFiles,MFiles,HFiles,FFiles,SFiles," +
	"SwigFiles,SwigCXXFiles,SysoFiles,EmbedFiles,EmbedPatterns,IgnoredGoFiles,IgnoredOtherFiles,Error"

func list(dir string, roots []string) ([]pkg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", append([]string{"list", "-deps", "-json=" + listFields, "--"}, roots...)...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list in %s: %w: %s", dir, err, stderr.Bytes())
	}
	var pkgs []pkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p pkg
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list in %s: %w", dir, err)
		}
		if p.Error != nil {
			return nil, fmt.Errorf("go list in %s: %s: %s", dir, p.ImportPath, p.Error.Err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

func inDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sortedUnique(xs []string) []string {
	xs = slices.Clone(xs)
	slices.Sort(xs)
	return slices.Compact(xs)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
