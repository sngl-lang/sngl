package codegen

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

const snglModulePath = "duckfam.us/sngl"

// DetectHostGoMod walks up from the current working directory looking for
// the nearest go.mod, parses it, and returns (goVersion, goModExtra) suitable
// for seeding a temporary go.mod that aligns SNGL runtime imports with the
// host module's view of duckfam.us/sngl.
//
//   - If the host module IS duckfam.us/sngl, goModExtra contains a
//     `replace` directive pointing the sngl import at the host module root.
//   - Otherwise, if the host module has a `replace` for the sngl module, it is
//     copied (with filesystem targets rewritten to absolute paths).
//   - Otherwise, if the host module `require`s sngl, a matching `require`
//     directive is included so `go mod tidy` picks the same version.
//
// goVersion is the host's `go <ver>` line (always returned when a go.mod is
// found, even when the module is unrelated to SNGL).
//
// Returns ("", "") when no go.mod is found or any step fails (best-effort).
func DetectHostGoMod() (goVersion, goModExtra string) {
	// Env override: tests and tooling that chdir away from the user's host
	// module before invoking codegen can set SNGL_HOST_GO_MOD to the absolute
	// path of the go.mod they want this function to consult.
	var modPath string
	if env := os.Getenv("SNGL_HOST_GO_MOD"); env != "" {
		if info, err := os.Stat(env); err == nil && !info.IsDir() {
			modPath = env
		}
	}
	if modPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", ""
		}
		modPath = findGoMod(cwd)
	}
	if modPath == "" {
		return "", ""
	}
	data, err := os.ReadFile(modPath)
	if err != nil {
		return "", ""
	}
	mf, err := modfile.Parse(modPath, data, nil)
	if err != nil {
		return "", ""
	}
	modRoot := filepath.Dir(modPath)

	if mf.Go != nil {
		goVersion = mf.Go.Version
	}

	var lines []string

	if mf.Module != nil && mf.Module.Mod.Path == snglModulePath {
		lines = append(lines, "replace "+snglModulePath+" => "+modRoot)
	} else {
		// Copy any replace directives that target the sngl module.
		var hadReplace bool
		for _, r := range mf.Replace {
			if r.Old.Path != snglModulePath {
				continue
			}
			hadReplace = true
			newPath := r.New.Path
			if r.New.Version == "" && !filepath.IsAbs(newPath) {
				newPath = filepath.Join(modRoot, newPath)
			}
			line := "replace " + r.Old.Path
			if r.Old.Version != "" {
				line += " " + r.Old.Version
			}
			line += " => " + newPath
			if r.New.Version != "" {
				line += " " + r.New.Version
			}
			lines = append(lines, line)
		}
		if !hadReplace {
			for _, req := range mf.Require {
				if req.Mod.Path == snglModulePath {
					lines = append(lines, "require "+snglModulePath+" "+req.Mod.Version)
					break
				}
			}
		}
	}

	goModExtra = strings.Join(lines, "\n")
	return goVersion, goModExtra
}

// findGoMod walks upward from start looking for a go.mod file. Returns the
// absolute path to the file, or "" if none found.
func findGoMod(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, "go.mod")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// WriteGoMod synthesises go.mod (and go.sum) for a temporary module in dir
// that builds SNGL-generated Go code.
//
// It seeds the module with the host module's full require list and go.sum
// rather than leaving resolution to `go mod tidy`. Two reasons:
//
//   - Correctness: tidy resolves each dependency at its latest version, so
//     generated code was compiled against a different fyne/bubbletea than
//     the repo itself pins. Copying the host requires makes the generated
//     program see exactly the versions the compiler was built against.
//   - Speed: aligned versions share compiled artifacts with the host build
//     cache instead of duplicating the whole dependency tree, and the tidy
//     round-trip (~0.3s per invocation) disappears from every generate,
//     run, build and test.
//
// needTidy reports that the go.mod written is the bare minimum `go mod tidy`
// needs to start from, because no host go.mod was found to align with. When a
// host go.mod *was* found the module graph is usually complete, but not
// always — see NeedsModuleTidy, which is how a caller finds out for certain.
func WriteGoMod(dir, goVersion, extraDirectives string) (needTidy bool, err error) {
	mf := hostModFile()
	if mf == nil {
		detVer, detExtra := DetectHostGoMod()
		if goVersion == "" {
			goVersion = detVer
		}
		if goVersion == "" {
			goVersion = "1.23"
		}
		if extraDirectives == "" {
			extraDirectives = detExtra
		}
		mod := fmt.Sprintf("module tmp\n\ngo %s\n", goVersion)
		if extraDirectives != "" {
			mod += "\n" + strings.TrimRight(extraDirectives, "\n") + "\n"
		}
		return true, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644)
	}

	if goVersion == "" && mf.Go != nil {
		goVersion = mf.Go.Version
	}
	if goVersion == "" {
		goVersion = "1.23"
	}

	modRoot := filepath.Dir(mf.Syntax.Name)
	// Caller-supplied directives win over the host-derived ones for the
	// same replaced module, so `--opt goModExtra=...` stays authoritative.
	extraLines, overridden := splitDirectives(extraDirectives)
	var replaces []string
	for _, line := range hostReplaceLines(mf, modRoot) {
		if overridden[replaceOldPath(line)] {
			continue
		}
		replaces = append(replaces, line)
	}
	replaces = append(replaces, extraLines...)

	required := map[string]bool{}
	type req struct{ path, version string }
	requires := make([]req, 0, len(mf.Require)+1)
	for _, r := range mf.Require {
		required[r.Mod.Path] = true
		requires = append(requires, req{r.Mod.Path, r.Mod.Version})
	}
	// A replace only takes effect for a module the main module also
	// requires; the host go.mod may replace modules it reaches only
	// indirectly, so add a placeholder require for those.
	for _, line := range replaces {
		p := replaceOldPath(line)
		if p == "" || required[p] {
			continue
		}
		required[p] = true
		requires = append(requires, req{p, "v0.0.0"})
	}

	var b strings.Builder
	b.WriteString("module tmp\n\n")
	fmt.Fprintf(&b, "go %s\n", goVersion)
	if len(requires) > 0 {
		b.WriteString("\nrequire (\n")
		for _, r := range requires {
			fmt.Fprintf(&b, "\t%s %s\n", r.path, r.version)
		}
		b.WriteString(")\n")
	}
	for _, line := range replaces {
		b.WriteString("\n" + line + "\n")
	}

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(b.String()), 0o644); err != nil {
		return true, err
	}
	// go.sum must cover the copied requires, otherwise every build fails
	// with "missing go.sum entry".
	sum, err := os.ReadFile(filepath.Join(modRoot, "go.sum"))
	if err == nil {
		if err := os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o644); err != nil {
			return true, err
		}
	}

	return false, nil
}

// NeedsModuleTidy reports whether a failed `go` command failed because of the
// module graph rather than the code, and so is worth retrying after
// `go mod tidy`.
//
// WriteGoMod seeds a temp module from the host's requires and go.sum, which
// covers the generated code's imports in the ordinary case and saves a tidy
// per invocation. It cannot cover every case: the host may require a module
// without importing the particular package the generated code reaches for,
// and that package's own dependencies are then absent from both files. A
// program importing charm.land/bubbles/v2/progress hit exactly that — the
// repo requires bubbles but imports no package that pulls in harmonica.
//
// These are load-phase failures: the go tool reports them before it compiles
// or runs anything, so retrying is safe.
func NeedsModuleTidy(output string) bool {
	for _, sig := range []string{
		"missing go.sum entry",
		"no required module provides package",
		"updates to go.mod needed",
		"to add it:",
		"is not in std",
	} {
		if strings.Contains(output, sig) {
			return true
		}
	}
	return false
}

// hostModFile locates and parses the host go.mod, or returns nil.
func hostModFile() *modfile.File {
	var modPath string
	if env := os.Getenv("SNGL_HOST_GO_MOD"); env != "" {
		if info, err := os.Stat(env); err == nil && !info.IsDir() {
			modPath = env
		}
	}
	if modPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil
		}
		modPath = findGoMod(cwd)
	}
	if modPath == "" {
		return nil
	}
	data, err := os.ReadFile(modPath)
	if err != nil {
		return nil
	}
	mf, err := modfile.Parse(modPath, data, nil)
	if err != nil {
		return nil
	}
	return mf
}

// hostReplaceLines renders the replace directives a temp module needs: the
// host's own replaces (filesystem targets made absolute) plus a replace
// pointing the sngl module at the host root when the host *is* sngl.
func hostReplaceLines(mf *modfile.File, modRoot string) []string {
	var lines []string
	if mf.Module != nil && mf.Module.Mod.Path == snglModulePath {
		lines = append(lines, "replace "+snglModulePath+" => "+modRoot)
	}
	for _, r := range mf.Replace {
		newPath := r.New.Path
		if r.New.Version == "" && !filepath.IsAbs(newPath) {
			newPath = filepath.Join(modRoot, newPath)
		}
		line := "replace " + r.Old.Path
		if r.Old.Version != "" {
			line += " " + r.Old.Version
		}
		line += " => " + newPath
		if r.New.Version != "" {
			line += " " + r.New.Version
		}
		lines = append(lines, line)
	}
	return lines
}

// splitDirectives breaks caller-supplied go.mod text into individual
// non-empty lines and reports which module paths its replace directives
// target.
func splitDirectives(extra string) (lines []string, replaced map[string]bool) {
	replaced = map[string]bool{}
	for line := range strings.SplitSeq(extra, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if p := replaceOldPath(line); p != "" {
			replaced[p] = true
		}
	}
	return lines, replaced
}

// replaceOldPath returns the replaced module path of a `replace X => Y`
// directive line, or "" when the line is not a replace.
func replaceOldPath(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "replace" {
		return ""
	}
	return fields[1]
}

// TidyModule runs `go mod tidy` in dir and returns its combined output.
func TidyModule(ctx context.Context, dir string) (string, error) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("go not found in PATH")
	}
	slog.Info("exec", "cmd", "go mod tidy", "dir", dir)
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, goPath, "mod", "tidy")
	cmd.Dir = dir
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	return out.String(), err
}
