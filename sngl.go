// Package sngl provides parsing, type-checking, optimization, and formatting
// for SNGL documents.
package sngl

import (
	"fmt"
	"io"
	"os"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// Parse reads SNGL source from r and returns the parsed document AST.
func Parse(filename string, r io.Reader) (*ast.Document, error) {
	return parser.Parse(filename, r)
}

// Format returns the formatted SNGL source for a document.
func Format(doc *ast.Document) string {
	return parser.Format(doc)
}

// FormatNode returns the formatted SNGL source for a single AST node.
func FormatNode(n ast.Node) string {
	return parser.FormatNode(n)
}

// Check type-checks a parsed SNGL document. dir is the directory of the source
// file, used to resolve relative import paths. It uses the default filesystem-based
// import resolver for directory imports and the registered scheme importers.
func Check(doc *ast.Document, dir string) error {
	return checker.Check(doc, os.DirFS(dir), dir, checker.DefaultResolver(), DefaultSchemeResolver(), BuildAPIConfig(doc), true)
}

// BuildAPIConfig resolves API namespaces from registered lang/platform providers
// based on the document's output declarations.
func BuildAPIConfig(doc *ast.Document) *checker.APIConfig {
	if len(doc.Outputs) == 0 {
		return nil
	}
	cfg := &checker.APIConfig{Namespaces: map[string]*ast.Document{}}
	seen := map[string]bool{}
	var resolvers []namedResolver
	for _, out := range doc.Outputs {
		if !seen[out.Lang] {
			seen[out.Lang] = true
			if lang := codegen.LookupLang(out.Lang); lang != nil {
				if ap, ok := lang.(codegen.APIProvider); ok {
					cfg.Namespaces[out.Lang] = ap.API()
				}
				if ar, ok := lang.(codegen.APIResolver); ok {
					resolvers = append(resolvers, namedResolver{out.Lang, ar})
				}
			}
		}
		if !seen[out.Platform] {
			seen[out.Platform] = true
			if plat := codegen.LookupPlatform(out.Platform); plat != nil {
				if ap, ok := plat.(codegen.APIProvider); ok {
					cfg.Namespaces[out.Platform] = ap.API()
				}
				if ar, ok := plat.(codegen.APIResolver); ok {
					resolvers = append(resolvers, namedResolver{out.Platform, ar})
				}
			}
		}
	}
	if len(resolvers) > 0 {
		byName := map[string]codegen.APIResolver{}
		for _, r := range resolvers {
			byName[r.name] = r.resolver
		}
		cfg.DynamicNS = func(namespace, name string) *ast.NativeDecls {
			if r, ok := byName[namespace]; ok {
				return r.ResolveAPI(name)
			}
			return nil
		}
	}
	if len(cfg.Namespaces) == 0 && cfg.DynamicNS == nil {
		return nil
	}
	return cfg
}

type namedResolver struct {
	name     string
	resolver codegen.APIResolver
}

// DefaultSchemeResolver returns a SchemeResolver that delegates to registered
// codegen scheme importers.
func DefaultSchemeResolver() checker.SchemeResolver {
	return func(scheme, uri, dir string) (*ast.NativeDecls, error) {
		imp := codegen.LookupScheme(scheme)
		if imp == nil {
			return nil, fmt.Errorf("unknown import scheme %q", scheme)
		}
		return imp.Resolve(uri, dir)
	}
}

// OptimizeConfig controls platform-specific AST transformations.
type OptimizeConfig = optimize.Config

// Optimize applies platform-specific transformations to a parsed and checked document.
func Optimize(doc *ast.Document, cfg OptimizeConfig) error {
	return optimize.Optimize(doc, cfg)
}
