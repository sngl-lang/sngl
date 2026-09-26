package ir

import "git.duckfam.us/jonathan/sngl/ast"

// foreignAttrs renders a declaration's Foreign record back as the marks that
// would have set it.
//
// Without this a converted document dropped the whole of what makes a
// declaration foreign: `sngl dump --stage checked` printed
// `func shout(s string) string` for a declaration that *is* `strings.ToUpper`,
// and `bad dyn` for a field whose type SNGL cannot model, with the reason
// gone. Both read as ordinary declarations the backend would emit, which is
// the opposite of what they are.
//
// What it can render is what a *mark* set. An importer leaves Foreign.Scheme
// empty and answers "which language" with Origin's own Go type instead, and
// Origin is `any` of a type this package cannot name — so an importer-resolved
// declaration still comes back bare. Closing that is what replacing Origin
// with NativeDeclRef is for; until then this covers every declaration whose
// foreignness was written in source, which is every one a program can have.
func (c *converter) foreignAttrs(f Foreign, fn *Func) []ast.MacroAttr {
	var out []ast.MacroAttr
	if a, ok := c.correspondenceAttr(f, fn); ok {
		out = append(out, a)
	}
	if f.Unusable != "" {
		// Language-neutral, and deliberately a second mark rather than a flag:
		// a flag carries no payload and the reason is the point.
		out = append(out, c.attr("macro", "unusable", ast.NewStringLiteral(f.Unusable)))
	}
	return out
}

// correspondenceAttr renders the mark that says what a declaration *is*
// outside SNGL.
//
// Marked is the fork. It says a #[foreign] mark asserted the correspondence
// and the declaration is still the program's own, so a backend emits it; the
// language-specific marks say the host already has this and nothing is
// emitted. They are two different claims and print as two different marks.
func (c *converter) correspondenceAttr(f Foreign, fn *Func) (ast.MacroAttr, bool) {
	if f.Name == "" {
		return ast.MacroAttr{}, false
	}
	if f.Marked {
		// #[foreign("scheme://path", "Name", flags...)] -- the path carries
		// its scheme the way the mark writes it, and a field has neither.
		var args []ast.Expr
		if f.Path != "" {
			args = append(args, ast.NewStringLiteral(schemeURI(f.Scheme, f.Path)))
		}
		args = append(args, ast.NewStringLiteral(f.Name))
		args = append(args, foreignFlagIdents(fn)...)
		return c.attr("macro", "foreign", args...), true
	}
	switch f.Scheme {
	case "go":
		// go.native(path, name): a field names the identifier alone, having no
		// package of its own, and the mark's one-argument form is that.
		var args []ast.Expr
		if f.Path != "" {
			args = append(args, ast.NewStringLiteral(f.Path))
		}
		args = append(args, ast.NewStringLiteral(f.Name))
		return c.attr("language/go", "native", append(args, nativeFlagIdents(fn)...)...), true
	case "js", "kotlin":
		// native(name, module): the opposite order to Go's, which is a wart in
		// the marks rather than in this rendering -- follow each as declared.
		args := []ast.Expr{ast.NewStringLiteral(f.Name)}
		if f.Path != "" {
			args = append(args, ast.NewStringLiteral(f.Path))
		}
		return c.attr("language/"+f.Scheme, "native", append(args, nativeFlagIdents(fn)...)...), true
	case "c":
		// cnative(name) and nothing else: C is an ABI rather than a target, so
		// there is no module to name and Path is the constant "C".
		return c.attr("macro", "cnative", ast.NewStringLiteral(f.Name)), true
	}
	return ast.MacroAttr{}, false
}

// attr builds one mark, spelling its package the way this file imported it.
func (c *converter) attr(uri, name string, args ...ast.Expr) ast.MacroAttr {
	return ast.MacroAttr{Alias: c.aliasFor(uri), Name: name, Args: args}
}

// schemeURI rejoins what imports.ParseScheme split: #[foreign] writes the
// path with its scheme attached, and Foreign keeps the two apart.
func schemeURI(scheme, path string) string {
	if scheme == "" {
		return path
	}
	return scheme + "://" + path
}

// nativeFlagIdents renders the flags a language's `native` mark accepts. Only
// a function carries any: every one describes a call.
func nativeFlagIdents(fn *Func) []ast.Expr {
	if fn == nil {
		return nil
	}
	var out []ast.Expr
	for _, f := range []struct {
		on   bool
		name string
	}{
		{fn.HasErrorReturn, "fails"},
		{fn.NativeMethod, "method"},
		{fn.NativeNamedArgs, "named"},
		{fn.NativeSchedules, "schedules"},
		{fn.HasContextArg, "context"},
	} {
		if f.on {
			out = append(out, &ast.IdentExpr{Name: f.name})
		}
	}
	return out
}

// foreignFlagIdents renders the flags #[foreign] accepts. `native` is not
// among them: it is the flag that clears Marked, so a record printing through
// this branch never carried it.
func foreignFlagIdents(fn *Func) []ast.Expr {
	if fn == nil {
		return nil
	}
	var out []ast.Expr
	if fn.IsAsync {
		out = append(out, &ast.IdentExpr{Name: "async"})
	}
	return out
}
