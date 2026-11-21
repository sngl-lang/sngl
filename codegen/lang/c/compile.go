package c

import (
	"bytes"
	"cmp"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/ir"
)

// Standards are the C standards a program compiles as. The output holds to
// both: a construct only the later admits needs a build option of its own.
var Standards = []string{"c11", "c17"}

// OptimizeFlag is the flag the optimize option's member names: the value
// sngl:language/c's Optimization declares for it, so the enum is the one list
// of levels, and o0's, its first, where member is empty -- a harness that
// names none compiles as an unset option does.
func OptimizeFlag(member string) (string, error) {
	pkg, _ := checker.CheckLibPackage("language/c")
	if pkg == nil {
		return "", fmt.Errorf("sngl:language/c is not linked in")
	}
	for _, e := range pkg.Enums {
		if e.Name != "Optimization" || len(e.Members) == 0 {
			continue
		}
		m := e.Members[0]
		if member != "" {
			m = nil
			for _, x := range e.Members {
				if x.Name == member {
					m = x
				}
			}
		}
		if m == nil {
			return "", fmt.Errorf("optimize=%s: no member of %s", member, e.Name)
		}
		if lit, ok := m.Value.(*ir.Literal); ok && lit.Value != "" {
			return lit.Value, nil
		}
		return "", fmt.Errorf("%s.%s declares no flag", e.Name, m.Name)
	}
	return "", fmt.Errorf("sngl:language/c declares no Optimization")
}

// Flags are what every build of a program passes its compiler for std: the
// standard, strictly, and signed arithmetic that wraps as it does on Go and
// Kotlin. What generated code is entitled to leave unused -- a parameter a
// body does not read, a library function nothing calls -- is not an error.
// A frame larger than a page touches each page as it grows, so recursion
// without end stops at the guard page below the stack rather than step over
// it and write what lies beyond (testdata/run/clash.txtar).
func Flags(std string) []string {
	return []string{
		"-std=" + std, "-pedantic", "-Wall", "-Wextra", "-Werror",
		"-Wno-unused-parameter", "-Wno-unused-function", "-Wno-unused-variable", "-Wno-unused-but-set-variable",
		"-fwrapv", "-fstack-clash-protection",
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
	// Target is what it builds for, as a manifest's `target` line names it:
	// windows/amd64 or windows/386 for MinGW's, empty for the host's.
	Target string
}

// Matrix is every C compiler SNGL holds its output to: gcc, the reference,
// clang, and MinGW's for 64- and 32-bit Windows. Path is empty for one the
// host lacks.
func Matrix() []Compiler {
	out := []Compiler{
		{Name: "gcc"},
		{Name: "clang"},
		{Name: "x86_64-w64-mingw32-gcc", Windows: true, Target: "windows/amd64"},
		{Name: "i686-w64-mingw32-gcc", Windows: true, Target: "windows/386"},
	}
	for i, c := range out {
		if p, err := exec.LookPath(c.Name); err == nil {
			out[i].Path = p
		}
	}
	return out
}

// Compilers are the compilers of the Matrix on the host.
func Compilers() []Compiler {
	var out []Compiler
	for _, c := range Matrix() {
		if c.Path != "" {
			out = append(out, c)
		}
	}
	return out
}

// Exe is the file a build into out writes: MinGW names a Windows executable
// out.exe, whatever out says.
func (c Compiler) Exe(out string) string {
	if c.Windows && !strings.HasSuffix(out, ".exe") {
		return out + ".exe"
	}
	return out
}

// Build compiles sources, in dir, into out under std, with extra flags after
// the standard ones (a library's, from its c.link); the program is at
// c.Exe(out). The compiler's own output is the error when it fails.
func (c Compiler) Build(dir string, sources []string, out, std string, extra ...string) error {
	args := append(Flags(std), "-o", c.Exe(out))
	args = append(args, sources...)
	args = append(args, extra...)
	args = append(args, "-lm")
	slog.Info("exec", "cmd", c.Name, "args", args, "dir", dir)
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
// names. A `target <os>/<arch>` line says the program is built for a system
// other than the host's, which a Compiler's Target names. A line beginning
// with `#` is a comment.
const BuildFile = "sngl.build"

// Manifest is the BuildFile for a program linking the pkg-config packages
// named and those links name, and the flags links name.
func Manifest(links []*ir.CLink, pkgConfig ...string) []byte {
	var b strings.Builder
	b.WriteString("# What this program links, which `sngl build` reads.\n")
	pkgs := slices.Clone(pkgConfig)
	for _, l := range links {
		for _, p := range l.PkgConfig {
			if !slices.Contains(pkgs, p) {
				pkgs = append(pkgs, p)
			}
		}
	}
	for _, p := range pkgs {
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

// TargetOf is the target dir's BuildFile names, empty for the host.
func TargetOf(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, BuildFile))
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if t, ok := strings.CutPrefix(strings.TrimSpace(line), "target "); ok {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

// ForTarget is the compiler that builds for target: cc, $CC or cc for the
// host, and for another system the Matrix's compiler for it.
func ForTarget(target, cc string) (Compiler, error) {
	if target == "" {
		return Host(cc), nil
	}
	for _, c := range Matrix() {
		if c.Target != target {
			continue
		}
		if c.Path == "" {
			return Compiler{}, fmt.Errorf("building for %s wants %s, which is not on PATH", target, c.Name)
		}
		return c, nil
	}
	return Compiler{}, fmt.Errorf("no C compiler builds for %s", target)
}

// Launch is the command running the program at exe: itself, or under Wine
// where it is a Windows program and the host is not Windows.
func Launch(exe string) []string {
	if strings.HasSuffix(exe, ".exe") && runtime.GOOS != "windows" {
		return []string{"wine", exe}
	}
	return []string{exe}
}

// BuildDir compiles the program written in dir into out under std at the
// optimize option's member (OptimizeFlag): every .c file there is a
// translation unit, and its BuildFile says what it links.
func (c Compiler) BuildDir(dir, out, std, optimize string) (string, error) {
	opt, err := OptimizeFlag(optimize)
	if err != nil {
		return "", err
	}
	sources, err := filepath.Glob(filepath.Join(dir, "*.c"))
	if err != nil || len(sources) == 0 {
		return "", fmt.Errorf("no C sources in %s", dir)
	}
	for i, s := range sources {
		sources[i] = filepath.Base(s)
	}
	extra := []string{opt}
	if data, err := os.ReadFile(filepath.Join(dir, BuildFile)); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if t, ok := strings.CutPrefix(line, "target "); ok && t != c.Target {
				return "", fmt.Errorf("%s builds for %s, and %s for %s", dir, t, c.Name, cmp.Or(c.Target, "the host"))
			}
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
