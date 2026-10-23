package lsp

import (
	"net/url"
	"path/filepath"
	"strings"

	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lspcore"
)

// analyze parses and type-checks a file, returning LSP diagnostics.
func (s *Server) analyze(fs *fileState) []Diagnostic {
	uri := fs.URI
	filename := uriToPath(uri)
	dir := filepath.Dir(filename)
	if isLibraryCopy(filename) {
		return nil
	}

	// Imports resolve as a build's do, so a plugin's handler runs here too --
	// under the server's grants, never prompting.
	resolver := build.NewResolver(dir)
	resolver.Trust = s.trust
	siblings, broken := s.siblings(filename)
	langs, plats := build.RegisteredTargets()
	a := lspcore.AnalyzePackageChecked(fs.Content, filename, siblings, &checker.Config{
		FS:              build.ProjectFS(dir),
		Dir:             dir,
		Resolver:        resolver,
		IsMain:          true,
		Languages:       langs,
		Platforms:       plats,
		TargetsComplete: true,
	})
	coreDiags := a.Diags
	for _, name := range broken {
		coreDiags = append(coreDiags, lspcore.Diagnostic{
			Severity: lspcore.SeverityError,
			Source:   "sngl",
			Message:  name + " does not parse, so nothing it declares is in scope here",
		})
	}
	if a.Doc != nil {
		fs.Doc, fs.Pkg = a.Doc, a.Pkg
		files := []string{filename}
		for _, sib := range siblings {
			files = append(files, lspcore.FileOf(sib))
		}
		fs.Index = lspcore.BuildIndex(a.Pkg, files, s.source)
	}

	diags := make([]Diagnostic, 0, len(coreDiags))
	for _, d := range coreDiags {
		// A sibling's problems are published on the sibling when it is open,
		// and reported by the build when it is not.
		if d.File == "" {
			diags = append(diags, coreDiagToLSP(d))
		}
	}
	return diags
}

func coreDiagToLSP(d lspcore.Diagnostic) Diagnostic {
	return Diagnostic{
		Range: Range{
			Start: Position{Line: d.Range.Start.Line, Character: d.Range.Start.Character},
			End:   Position{Line: d.Range.End.Line, Character: d.Range.End.Character},
		},
		Severity: DiagSeverity(d.Severity),
		Source:   d.Source,
		Message:  d.Message,
	}
}

func uriToPath(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		u, err := url.Parse(uri)
		if err == nil {
			return u.Path
		}
	}
	return uri
}
