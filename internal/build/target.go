package build

import (
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Target is one language/platform pair to build, with the options it carries.
type Target struct {
	Lang     string
	Platform string
	Options  *ir.StructLit
}

// IsInterpreted reports whether a target runs the IR rather than being
// translated into a language.
//
// `--lang none` says the program is not translated; something else runs it.
// For html that something is the browser, and the platform genuinely generates
// a static site. For every other platform it is the interpreter.
func IsInterpreted(t Target) bool {
	return t.Lang == "none" && t.Platform != "html"
}

// SelectedTargets is the caller's own target selection, as the checker takes
// it. One of `--lang`/`--platform` alone is still a target: a platform
// selected without a language loads that platform's package, which carries its
// overrides.
func SelectedTargets(lang, plat string) []ir.StaticTarget {
	if lang == "" && plat == "" {
		return nil
	}
	return []ir.StaticTarget{{Platform: plat, Language: lang}}
}

// ResolveLangPlat completes a partial selection. A platform alone takes its
// first supported language; a language alone is an error, since nothing says
// what to generate for.
func ResolveLangPlat(cliLang, cliPlat string) (string, string, error) {
	if cliPlat != "" && cliLang == "" {
		plat := codegen.LookupPlatform(cliPlat)
		if plat == nil {
			return "", "", fmt.Errorf("unknown platform %q (available: %v)", cliPlat, codegen.Platforms())
		}
		langs := plat.SupportedLangs()
		if len(langs) == 0 {
			return "", "", fmt.Errorf("platform %q has no supported languages", cliPlat)
		}
		return langs[0], cliPlat, nil
	}
	if cliLang != "" && cliPlat == "" {
		return "", "", fmt.Errorf("--platform is required when --lang is specified")
	}
	return cliLang, cliPlat, nil
}

// ValidateOutputs reports an output block naming a target that does not exist,
// or a platform/language pair that is not supported.
func ValidateOutputs(pkg *ir.Package) error {
	for _, out := range pkg.Outputs {
		var pos ast.Pos
		if out.AST != nil {
			pos = out.AST.Pos
		}
		lang := codegen.LookupLang(out.Lang)
		if lang == nil {
			return fmt.Errorf("%s: unknown language %q (available: %v)", pos, out.Lang, codegen.Langs())
		}
		plat := codegen.LookupPlatform(out.Platform)
		if plat == nil {
			return fmt.Errorf("%s: unknown platform %q (available: %v)", pos, out.Platform, codegen.Platforms())
		}
		if !slices.Contains(plat.SupportedLangs(), out.Lang) {
			return fmt.Errorf("%s: platform %q does not support language %q (supported: %v)", pos, out.Platform, out.Lang, plat.SupportedLangs())
		}
	}
	return nil
}

// ParseCLIOpts converts a --opt key=value slice into a map. Empty entries and
// the cobra-default sentinel "[]" (left over from flag reset between
// in-process test invocations) are filtered.
func ParseCLIOpts(optSlice []string) map[string]string {
	out := make(map[string]string)
	for _, kv := range optSlice {
		if kv == "" || kv == "[]" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// ResolveTargets is the targets to build: the one the caller selected, or
// every one the package's own output blocks declare.
func ResolveTargets(pkg *ir.Package, cliLang, cliPlat string, cliOpts map[string]string) ([]Target, error) {
	if cliLang != "" && cliPlat != "" {
		// Seed from a matching output declaration so flags select the target
		// without discarding its declared options (e.g. stylesheet="..."). CLI
		// --opt values still override below. Falls back to empty options when
		// the requested target isn't declared in any output block.
		var base *ir.StructLit
		for _, o := range pkg.Outputs {
			if o.Lang == cliLang && o.Platform == cliPlat {
				base = o.Options
				break
			}
		}
		t := Target{Lang: cliLang, Platform: cliPlat, Options: cloneStructLit(base)}
		if err := applyCLIOpts(t.Options, cliOpts); err != nil {
			return nil, err
		}
		return []Target{t}, nil
	}
	var targets []Target
	for _, o := range pkg.Outputs {
		opts := cloneStructLit(o.Options)
		// Errors from per-target validation are deferred — an opt may be
		// valid for one target's schema and unknown to another. Below we
		// only error if no target accepts the key.
		_ = applyCLIOpts(opts, cliOpts)
		targets = append(targets, Target{Lang: o.Lang, Platform: o.Platform, Options: opts})
	}
	if err := validateCLIOptsAcrossTargets(cliOpts, targets); err != nil {
		return nil, err
	}
	return targets, nil
}

// A key valid for at least one target passes: per-target ApplyOptions drops it
// on the others on purpose, since an opt name often varies by platform.
func validateCLIOptsAcrossTargets(kv map[string]string, targets []Target) error {
	if len(kv) == 0 {
		return nil
	}
	var anySchema bool
	for _, t := range targets {
		if t.Options != nil && t.Options.Def != nil {
			anySchema = true
			break
		}
	}
	if !anySchema {
		return nil
	}
	for k := range kv {
		matched := buildOnlyOpts[k]
		for _, t := range targets {
			if t.Options != nil && optionFieldType(t.Options, k) != nil {
				matched = true
				break
			}
		}
		if !matched {
			var available []string
			seen := make(map[string]bool)
			for _, t := range targets {
				if t.Options == nil || t.Options.Def == nil {
					continue
				}
				for _, f := range t.Options.Def.Fields {
					if !seen[f.Name] {
						seen[f.Name] = true
						available = append(available, f.Name)
					}
				}
			}
			return fmt.Errorf("unknown --opt %q (available: %v)", k, available)
		}
	}
	return nil
}

// The Fields slice is copied so a per-target option mutation (CLI overlay,
// projectDir injection) cannot reach the IR shared across targets.
func cloneStructLit(src *ir.StructLit) *ir.StructLit {
	if src == nil {
		return &ir.StructLit{}
	}
	out := &ir.StructLit{Type: src.Type, Def: src.Def}
	out.Fields = append(out.Fields, src.Fields...)
	return out
}

// ApplyCLIOpts overlays --opt values onto one target's options.
func ApplyCLIOpts(opts *ir.StructLit, kv map[string]string) error { return applyCLIOpts(opts, kv) }

// buildOnlyOpts are the options no *target* declares, because the compiler
// reads them itself whatever is being built for -- so the per-target scoping
// below does not apply to them. A key a platform's schema does not mention is
// normally another platform's; these are nobody's, and a program whose
// `output` block carries a schema had them dropped.
var buildOnlyOpts = map[string]bool{
	"rootComponent": true,
	"projectDir":    true,
}

// A non-string field's value is parsed as a SNGL const expression and checked
// against the declared type. An unknown type (no Def, or the field absent from
// it) is wrapped as a raw string literal, so simple cases work with no checker
// pass.
func applyCLIOpts(opts *ir.StructLit, kv map[string]string) error {
	if len(kv) == 0 {
		return nil
	}
	for k, v := range kv {
		ft := optionFieldType(opts, k)
		// A key this target does not declare is left to the cross-target
		// validation in ResolveTargets, so one --opt can be scoped to
		// whichever output declares it.
		if opts != nil && opts.Def != nil && ft == nil && !buildOnlyOpts[k] {
			continue
		}
		if ft != nil && ft.Kind != ir.TypeString {
			parsed, err := parseConstOption(v, ft)
			if err == nil {
				codegen.SetOptionField(opts, k, parsed)
				continue
			}
			// Fall back to string-as-raw on parse failure; the platform
			// will surface a clearer error from ApplyOptions.
		}
		codegen.SetOptionField(opts, k, v)
	}
	return nil
}

// Mirrors codegen.ApplyOptions, because the optimizer needs the flag before
// any platform Config struct is populated.
func optionBool(opts *ir.StructLit, name string) bool {
	v, ok := codegen.OptionField(opts, name)
	if !ok {
		return false
	}
	lit, ok := v.(*ir.Literal)
	if !ok {
		return false
	}
	return lit.Value == "true"
}

func optionFieldType(opts *ir.StructLit, name string) *ir.Type {
	if opts == nil || opts.Def == nil {
		return nil
	}
	for _, f := range opts.Def.Fields {
		if f.Name == name {
			return f.Type
		}
	}
	return nil
}

// Raw is set so codegen.ApplyOptions reads the same shape as a source-declared
// option.
func parseConstOption(raw string, t *ir.Type) (*ir.Literal, error) {
	switch t.Kind {
	case ir.TypeBool:
		switch raw {
		case "true", "false":
			return &ir.Literal{Type: ir.TypBool, Value: raw}, nil
		}
		return nil, fmt.Errorf("expected bool, got %q", raw)
	case ir.TypeInt:
		return &ir.Literal{Type: ir.TypInt, Value: raw}, nil
	case ir.TypeFloat:
		return &ir.Literal{Type: ir.TypFloat, Value: raw}, nil
	}
	if ir.IsColorStruct(t) {
		// A colour arrives as a raw hex string, which is what ApplyOptions
		// reads out of .Value anyway.
		return &ir.Literal{Type: ir.TypString, Value: raw}, nil
	}
	return &ir.Literal{Type: t, Value: raw}, nil
}
