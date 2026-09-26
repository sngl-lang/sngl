// Package none is a marker LangTranslator used by the html platform to
// select static-site mode. It carries no translation logic — its methods are
// never called on the static path (html routes inline-JS emission through
// the js translator internally) and html rejects route mode with lang=none.
package none

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterLang(&Translator{})
}

type Translator struct{}

func (t *Translator) LanguageIdentifier() string { return "none" }
func (t *Translator) Description() string {
	return "Sentinel language for static output modes that need no backing code."
}

func (t *Translator) GenerateIdentifier(name *ir.Ident) string { return name.Name }
func (t *Translator) TranslateIRLiteral(e ir.Expr) string      { return "" }
func (t *Translator) ExportName(name string) string            { return name }

// NewFileEmitter returns an unimplemented stub. The none lang isn't
// expected to be the target of source-file emission today.
func (t *Translator) NewFileEmitter(sink codegen.Sink, opts codegen.FileOptions) codegen.FileEmitter {
	return &codegen.UnimplementedFileEmitter{Lang: "none"}
}
