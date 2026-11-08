package c

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// Standards are the C standards a program compiles as. The output holds to
// both: a construct only the later admits needs a build option of its own.
var Standards = []string{"c11", "c17"}

// Flags are what every build of a program passes its compiler for std: the
// standard, strictly, and signed arithmetic that wraps as it does on Go and
// Kotlin. What generated code is entitled to leave unused -- a parameter a
// body does not read, a library function nothing calls -- is not an error.
func Flags(std string) []string {
	return []string{
		"-std=" + std, "-pedantic", "-Wall", "-Wextra", "-Werror",
		"-Wno-unused-parameter", "-Wno-unused-function", "-Wno-unused-variable", "-Wno-unused-but-set-variable",
		"-fwrapv",
	}
}

// Compiler is a C compiler on the host.
type Compiler struct {
	// Name is how a test names it: gcc, clang, or a MinGW cross compiler's
	// triple.
	Name string
	Path string
	// Windows says it builds a Windows executable, which runs under Wine.
	Windows bool
}

// Compilers are the C compilers on the host that SNGL holds its output to:
// gcc, the reference, clang, and MinGW's for 64- and 32-bit Windows.
func Compilers() []Compiler {
	var out []Compiler
	for _, c := range []Compiler{
		{Name: "gcc"},
		{Name: "clang"},
		{Name: "x86_64-w64-mingw32-gcc", Windows: true},
		{Name: "i686-w64-mingw32-gcc", Windows: true},
	} {
		if p, err := exec.LookPath(c.Name); err == nil {
			c.Path = p
			out = append(out, c)
		}
	}
	return out
}

// Build compiles sources, in dir, into out under std, with extra flags after
// the standard ones (a library's, from its c.link). The compiler's own
// output is the error when it fails.
func (c Compiler) Build(dir string, sources []string, out, std string, extra ...string) error {
	args := append(Flags(std), "-o", out)
	args = append(args, sources...)
	args = append(args, extra...)
	args = append(args, "-lm")
	cmd := exec.Command(c.Path, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w\n%s", c.Name, strings.Join(args, " "), err, stderr.String())
	}
	return nil
}

// WriteRuntime writes the C runtime into dir, beside the program.
func WriteRuntime(dir string) error {
	for name, data := range Runtime() {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// BuildFile names the manifest a C program is written with, which says what
// it links beyond the C library, a line each: `pkg-config <name>`, a
// library pkg-config gives the flags of, and `flag <flag>`, one a c.link
// names. A line beginning with `#` is a comment.
const BuildFile = "sngl.build"

// Manifest is the BuildFile for a program linking the pkg-config packages
// named, and the flags links name.
func Manifest(links []*ir.CLink, pkgConfig ...string) []byte {
	var b strings.Builder
	b.WriteString("# What this program links, which `sngl build` reads.\n")
	for _, p := range pkgConfig {
		b.WriteString("pkg-config " + p + "\n")
	}
	var seen []string
	for _, l := range links {
		for _, f := range append(slices.Clone(l.CFlags), l.LDFlags...) {
			if !slices.Contains(seen, f) {
				seen = append(seen, f)
				b.WriteString("flag " + f + "\n")
			}
		}
	}
	return []byte(b.String())
}

// Links includes the headers links name in the file c renders, and is the
// sources they ship, by name, written beside the program.
func Links(c *Context, links []*ir.CLink) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, l := range links {
		c.Include(l.Include, l.System)
		for _, src := range l.Sources {
			if prev, ok := out[src.Name]; ok && string(prev) != src.Text {
				return nil, fmt.Errorf("two c.links ship a source named %s", src.Name)
			}
			out[src.Name] = []byte(src.Text)
		}
	}
	return out, nil
}

// BuildDir compiles the program written in dir into out under std: every .c
// file there is a translation unit, and its BuildFile says what it links.
func (c Compiler) BuildDir(dir, out, std string) (string, error) {
	sources, err := filepath.Glob(filepath.Join(dir, "*.c"))
	if err != nil || len(sources) == 0 {
		return "", fmt.Errorf("no C sources in %s", dir)
	}
	for i, s := range sources {
		sources[i] = filepath.Base(s)
	}
	var extra []string
	if data, err := os.ReadFile(filepath.Join(dir, BuildFile)); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if f, ok := strings.CutPrefix(line, "flag "); ok {
				extra = append(extra, f)
				continue
			}
			name, ok := strings.CutPrefix(line, "pkg-config ")
			if !ok {
				continue
			}
			flags, err := exec.Command("pkg-config", "--cflags", "--libs", strings.TrimSpace(name)).Output()
			if err != nil {
				return "", fmt.Errorf("pkg-config %s: %w", name, err)
			}
			extra = append(extra, strings.Fields(string(flags))...)
		}
	}
	if err := c.Build(dir, sources, out, std, extra...); err != nil {
		return "", err
	}
	return "", nil
}

// Host is the compiler a build runs where none is named: $CC, or cc.
func Host(cc string) Compiler {
	if cc == "" {
		cc = os.Getenv("CC")
	}
	if cc == "" {
		cc = "cc"
	}
	p, err := exec.LookPath(cc)
	if err != nil {
		p = cc
	}
	return Compiler{Name: filepath.Base(cc), Path: p, Windows: strings.Contains(cc, "mingw")}
}
