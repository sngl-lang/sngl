package js

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolved is the result of resolving a js:// spec to a concrete file.
// importPath is the bare specifier as it should appear in the emitted JS
// `import ... from "<importPath>"` (e.g. "lodash", "./foo"). path is the
// absolute filesystem path of the entry file.
type resolved struct {
	importPath string
	path       string
}

var sourceExts = []string{".ts", ".tsx", ".d.ts", ".js", ".mjs", ".cjs"}

// resolveJSSpec turns a `js://` spec (without the scheme prefix) into a
// concrete file path on disk and the bare specifier to emit in JS imports.
//
//	"./foo.json"   → {importPath:"./foo.json", path:"<dir>/foo.json"}
//	"./foo"        → {importPath:"./foo",      path:"<dir>/foo/index.ts"}
//	"lodash"       → {importPath:"lodash",     path:"<.../node_modules/lodash/index.d.ts>"}
//	"foo/bar"      → {importPath:"foo/bar",    path:"<.../node_modules/foo/bar.ts>"} (subpath import)
func resolveJSSpec(spec, dir string) (*resolved, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getting cwd: %w", err)
		}
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "/") {
		path, err := resolveRelative(spec, dir)
		if err != nil {
			return nil, err
		}
		path, _ = filepath.Abs(path)
		return &resolved{importPath: spec, path: path}, nil
	}
	path, err := resolveBareSpecifier(spec, dir)
	if err != nil {
		return nil, err
	}
	path, _ = filepath.Abs(path)
	return &resolved{importPath: spec, path: path}, nil
}

// resolveRelative resolves a relative path: explicit file, directory's index,
// or extension fallback.
func resolveRelative(spec, dir string) (string, error) {
	abs := spec
	if !filepath.IsAbs(spec) {
		abs = filepath.Join(dir, spec)
	}
	if info, err := os.Stat(abs); err == nil {
		if info.IsDir() {
			return resolveDirIndex(abs)
		}
		return abs, nil
	}
	if path, ok := tryExtensions(abs); ok {
		return path, nil
	}
	return "", fmt.Errorf("file not found: %s", spec)
}

// tryExtensions checks `<base><ext>` for each supported source extension.
func tryExtensions(base string) (string, bool) {
	for _, ext := range sourceExts {
		candidate := base + ext
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

// resolveDirIndex picks `<dir>/index.<ext>` for the first matching ext.
func resolveDirIndex(dir string) (string, error) {
	for _, ext := range sourceExts {
		candidate := filepath.Join(dir, "index"+ext)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no index.{ts,tsx,d.ts,js,mjs,cjs} in %s", dir)
}

// resolveBareSpecifier walks node_modules from dir upward looking for the
// package, then picks its entry from package.json (`types`/`typings` first,
// then `main`, then `index.<ext>`).
func resolveBareSpecifier(spec, dir string) (string, error) {
	pkgName, subpath := splitPackageSpec(spec)
	pkgRoot, err := findPackageRoot(pkgName, dir)
	if err != nil {
		return "", err
	}
	if subpath != "" {
		target := filepath.Join(pkgRoot, subpath)
		if info, err := os.Stat(target); err == nil {
			if info.IsDir() {
				return resolveDirIndex(target)
			}
			return target, nil
		}
		if path, ok := tryExtensions(target); ok {
			return path, nil
		}
		return "", fmt.Errorf("subpath %q not found in package %s", subpath, pkgName)
	}
	return readPackageEntry(pkgRoot)
}

// splitPackageSpec splits "foo/bar/baz" into ("foo", "bar/baz") and
// "@scope/foo/bar" into ("@scope/foo", "bar"). Returns ("", "") for empty.
func splitPackageSpec(spec string) (pkgName, subpath string) {
	if spec == "" {
		return "", ""
	}
	if strings.HasPrefix(spec, "@") {
		parts := strings.SplitN(spec, "/", 3)
		if len(parts) < 2 {
			return spec, ""
		}
		pkgName = parts[0] + "/" + parts[1]
		if len(parts) == 3 {
			subpath = parts[2]
		}
		return
	}
	if before, after, ok := strings.Cut(spec, "/"); ok {
		return before, after
	}
	return spec, ""
}

// findPackageRoot walks up the directory tree looking for
// `<dir>/node_modules/<pkgName>`. Returns the absolute package root.
func findPackageRoot(pkgName, dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(abs, "node_modules", pkgName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("package %q not found in any node_modules", pkgName)
		}
		abs = parent
	}
}

// readPackageEntry picks the entry file for a package: types/typings, then
// main, then index.<ext>.
func readPackageEntry(pkgRoot string) (string, error) {
	pjPath := filepath.Join(pkgRoot, "package.json")
	data, err := os.ReadFile(pjPath)
	if err == nil {
		var pj struct {
			Types   string `json:"types"`
			Typings string `json:"typings"`
			Main    string `json:"main"`
			Module  string `json:"module"`
		}
		if err := json.Unmarshal(data, &pj); err == nil {
			for _, candidate := range []string{pj.Types, pj.Typings, pj.Module, pj.Main} {
				if candidate == "" {
					continue
				}
				path := filepath.Join(pkgRoot, candidate)
				if info, err := os.Stat(path); err == nil && !info.IsDir() {
					return path, nil
				}
				if path, ok := tryExtensions(filepath.Join(pkgRoot, strings.TrimSuffix(candidate, filepath.Ext(candidate)))); ok {
					return path, nil
				}
			}
		}
	}
	return resolveDirIndex(pkgRoot)
}
