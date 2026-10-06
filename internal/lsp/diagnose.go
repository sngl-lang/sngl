package lsp

import (
	"net/url"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/build"
	"git.duckfam.us/jonathan/sngl/internal/lspcore"
)

// analyze parses and type-checks a file, returning LSP diagnostics.
func (s *Server) analyze(fs *fileState) []Diagnostic {
	uri := fs.URI
	filename := uriToPath(uri)
	dir := filepath.Dir(filename)

	// Imports resolve as a build's do, so a plugin's handler runs here too --
	// under the server's grants, never prompting.
	resolver := build.NewResolver(dir)
	resolver.Trust = s.trust
	doc, coreDiags := lspcore.Analyze(fs.Content, filename, build.ProjectFS(dir), dir, resolver)
	if doc != nil {
		fs.Doc = doc
	}

	diags := make([]Diagnostic, len(coreDiags))
	for i, d := range coreDiags {
		diags[i] = coreDiagToLSP(d)
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
