package bubbletea

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Run implements codegen.Runner. Delegates execution to the language's LangRunner.
func (g *Generator) Run(dir string, opts *ir.StructLit, args []string) error {
	var cfg Config
	if err := codegen.ApplyOptions(&cfg, opts); err != nil {
		return fmt.Errorf("bubbletea: %w", err)
	}
	cfg = cfg.withDefaults()

	lang := codegen.LookupLang(cfg.Lang)
	if lang == nil {
		return fmt.Errorf("bubbletea: unknown language %q", cfg.Lang)
	}
	lr, ok := lang.(codegen.LangRunner)
	if !ok {
		return fmt.Errorf("bubbletea: language %q does not support direct execution", cfg.Lang)
	}
	return lr.RunDir(dir, cfg.GoVersion, args)
}
