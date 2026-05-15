package codegen

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

const snglModulePath = "git.duckfam.us/jonathan/sngl"

// DetectHostGoMod walks up from the current working directory looking for
// the nearest go.mod, parses it, and returns (goVersion, goModExtra) suitable
// for seeding a temporary go.mod that aligns SNGL runtime imports with the
// host module's view of git.duckfam.us/jonathan/sngl.
//
//   - If the host module IS git.duckfam.us/jonathan/sngl, goModExtra contains a
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
	cwd, err := os.Getwd()
	if err != nil {
		return "", ""
	}
	modPath := findGoMod(cwd)
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
