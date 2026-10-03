package trust

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/pkg/go/consteval"
)

// VocabPath is the package the config file's vocabulary is declared in, and
// vocabAlias the name a written file imports it under.
const (
	VocabPath  = "sngl:x/gen/trust"
	vocabAlias = "trust"
)

// Allow is one `trust.allow` of the config file: an origin and what it may do.
// An empty origin is `trust.everywhere`, which grants every project network
// access and nothing else.
type Allow struct {
	Origin Origin
	Grants []Grant
}

// String is the allow as the config file spells it, on one line.
func (a Allow) String() string {
	var b strings.Builder
	if a.Origin.Spec == "" {
		b.WriteString(vocabAlias + ".everywhere {")
		for _, g := range a.Grants {
			b.WriteString(" " + g.child())
		}
		b.WriteString(" }")
		return b.String()
	}
	fmt.Fprintf(&b, "%s.allow(origin=%s", vocabAlias, quote(a.Origin.Spec))
	if a.Origin.Module != "" {
		fmt.Fprintf(&b, ", module=%s", quote(a.Origin.Module))
	}
	if a.Origin.Digest != "" {
		fmt.Fprintf(&b, ", sha256=%s", quote(a.Origin.Digest))
	}
	b.WriteString(") {")
	for _, g := range a.Grants {
		b.WriteString(" " + g.child())
	}
	b.WriteString(" }")
	return b.String()
}

func (g Grant) child() string {
	switch g.Kind {
	case Command:
		s := fmt.Sprintf("%s.command(prefix=%s", vocabAlias, quoteList(g.Prefix))
		if len(g.BanFlags) > 0 {
			s += fmt.Sprintf(", banFlags=%s", quoteList(g.BanFlags))
		}
		return s + ")"
	case Env:
		return fmt.Sprintf("%s.env(name=%s)", vocabAlias, quote(g.Value))
	case File:
		return fmt.Sprintf("%s.file(path=%s)", vocabAlias, quote(g.Value))
	case Dir:
		return fmt.Sprintf("%s.dir(path=%s)", vocabAlias, quote(g.Value))
	case Net:
		return fmt.Sprintf("%s.net(host=%s)", vocabAlias, quote(g.Value))
	}
	return vocabAlias + ".eval()"
}

func quote(s string) string { return string(consteval.AppendQuote(nil, s)) }

func quoteList(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = quote(x)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// ReadConfig returns the allows the config file at path records, in order. A
// file that does not exist records none.
func ReadConfig(path string) ([]Allow, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseConfig(path, data)
}

func parseConfig(path string, data []byte) ([]Allow, error) {
	doc, err := parser.Parse(path, data)
	if err != nil {
		return nil, err
	}
	alias := ""
	for _, st := range doc.Stmts {
		if imp, ok := st.(*ast.Import); ok && imp.Path == VocabPath {
			alias = imp.Alias
		}
	}
	if alias == "" || alias == "." {
		return nil, fmt.Errorf("%s: does not import %s under a name", path, VocabPath)
	}
	var out []Allow
	for _, st := range doc.Stmts {
		name, args, block := node(st)
		if name == "" {
			continue
		}
		everywhere := name == alias+".everywhere"
		if name != alias+".allow" && !everywhere {
			return nil, fmt.Errorf("%s:%d: %s is not a %s.allow or a %s.everywhere", path, st.StmtPos().Line, name, alias, alias)
		}
		where := fmt.Sprintf("%s:%d", path, st.StmtPos().Line)
		props, err := readProps(args)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		a := Allow{Origin: Origin{Spec: props["origin"].s, Module: props["module"].s, Digest: props["sha256"].s}}
		switch {
		case everywhere:
			a.Origin = Origin{}
		case a.Origin.Spec == "":
			return nil, fmt.Errorf("%s: %s.allow names no origin", where, alias)
		case !IsOrigin(a.Origin.Spec):
			return nil, fmt.Errorf("%s: origin %q is an import path, which any repository may claim; a recorded grant names where the code came from", where, a.Origin.Spec)
		}
		for _, c := range block {
			cname, cargs, _ := node(c)
			kind, ok := strings.CutPrefix(cname, alias+".")
			if !ok {
				return nil, fmt.Errorf("%s:%d: %s is not a grant", path, c.StmtPos().Line, cname)
			}
			cp, err := readProps(cargs)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %w", path, c.StmtPos().Line, err)
			}
			g := Grant{Subject: a.Origin.Spec, Module: a.Origin.Module, Digest: a.Origin.Digest, Source: where}
			switch Kind(kind) {
			case Eval:
				g.Kind = Eval
			case Command:
				g.Kind, g.Prefix, g.BanFlags = Command, cp["prefix"].l, cp["banFlags"].l
				if len(g.Prefix) == 0 {
					return nil, fmt.Errorf("%s:%d: %s.command needs a prefix", path, c.StmtPos().Line, alias)
				}
			case Env:
				g.Kind, g.Value = Env, cp["name"].s
			case File:
				g.Kind, g.Value = File, cp["path"].s
			case Dir:
				g.Kind, g.Value = Dir, cp["path"].s
			case Net:
				g.Kind, g.Value = Net, strings.ToLower(cp["host"].s)
			default:
				return nil, fmt.Errorf("%s:%d: %s is not a grant", path, c.StmtPos().Line, cname)
			}
			if everywhere && g.Kind != Net {
				return nil, fmt.Errorf("%s:%d: %s.everywhere grants network access only; anything else names the code it trusts, in a %s.allow", path, c.StmtPos().Line, alias, alias)
			}
			a.Grants = append(a.Grants, g)
		}
		out = append(out, a)
	}
	return out, nil
}

func node(st ast.Stmt) (string, ast.ArgList, []ast.Stmt) {
	switch n := st.(type) {
	case *ast.VisualNode:
		return n.TargetName(), n.Args, n.Block.Stmts
	case *ast.CallStmt:
		sel, ok := n.Call.Func.(*ast.SelectExpr)
		if !ok {
			return "", ast.ArgList{}, nil
		}
		id, ok := sel.Operand.(*ast.IdentExpr)
		if !ok {
			return "", ast.ArgList{}, nil
		}
		return id.Name + "." + sel.Field, n.Call.Args, nil
	}
	return "", ast.ArgList{}, nil
}

type prop struct {
	s string
	l []string
}

func readProps(args ast.ArgList) (map[string]prop, error) {
	out := map[string]prop{}
	for _, a := range args.Args {
		var arg ast.Arg
		switch v := a.(type) {
		case ast.Arg:
			arg = v
		case *ast.Arg:
			arg = *v
		default:
			return nil, errors.New("an argument that is not a value")
		}
		if arg.Name == "" {
			return nil, errors.New("arguments are named")
		}
		switch v := arg.Value.(type) {
		case *ast.LiteralExpr:
			s, ok := v.StringValue()
			if !ok {
				return nil, fmt.Errorf("%s is not a string", arg.Name)
			}
			out[arg.Name] = prop{s: s}
		case *ast.ListExpr:
			var xs []string
			for _, e := range v.Elements {
				lit, ok := e.(*ast.LiteralExpr)
				if !ok {
					return nil, fmt.Errorf("%s is not a list of strings", arg.Name)
				}
				s, ok := lit.StringValue()
				if !ok {
					return nil, fmt.Errorf("%s is not a list of strings", arg.Name)
				}
				xs = append(xs, s)
			}
			out[arg.Name] = prop{l: xs}
		default:
			return nil, fmt.Errorf("%s is not a literal", arg.Name)
		}
	}
	return out, nil
}

// Load adds every grant the config file at path records.
func (p *Policy) Load(path string) error {
	allows, err := ReadConfig(path)
	if err != nil {
		return err
	}
	for _, a := range allows {
		p.Add(a.Grants...)
	}
	return nil
}

const configHeader = "// Grants recorded by `sngl trust` and the build's prompt. Read only from\n// here: a project's own copy of this file is never consulted.\n\nimport " + vocabAlias + " \"" + VocabPath + "\"\n\n"

// AppendConfig records grants at the end of the config file at path, one
// `trust.allow` per origin, creating the file if it does not exist. It
// returns the lines it wrote.
func AppendConfig(path string, gs []Grant) error {
	_, err := Record(path, gs)
	return err
}

// Record is AppendConfig, returning the lines written.
func Record(path string, gs []Grant) ([]string, error) {
	var lines []string
	var allows []Allow
	for _, g := range gs {
		o := Origin{Spec: g.Subject, Module: g.Module, Digest: g.Digest}
		if n := len(allows); n > 0 && allows[n-1].Origin == o {
			allows[n-1].Grants = append(allows[n-1].Grants, g)
			continue
		}
		allows = append(allows, Allow{Origin: o, Grants: []Grant{g}})
	}
	for _, a := range allows {
		lines = append(lines, a.String())
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = []byte(configHeader)
	} else if err != nil {
		return nil, err
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	for _, l := range lines {
		data = append(data, l+"\n"...)
	}
	if _, err := parseConfig(path, data); err != nil {
		return nil, fmt.Errorf("the file would not read back: %w", err)
	}
	return lines, writeAtomic(path, data)
}

// Remove deletes the allows `sel` names from the config file at path -- a
// 1-based number as `sngl trust --list` prints it, or an origin -- and
// returns them.
func Remove(path, sel string) ([]Allow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	allows, err := parseConfig(path, data)
	if err != nil {
		return nil, err
	}
	doc, err := parser.Parse(path, data)
	if err != nil {
		return nil, err
	}
	n, numbered := strconv.Atoi(sel)
	var removed []Allow
	var keep []ast.Stmt
	i := 0
	for _, st := range doc.Stmts {
		if name, _, _ := node(st); name == "" {
			keep = append(keep, st)
			continue
		}
		a := allows[i]
		i++
		if numbered == nil && i == n || numbered != nil && sameOrigin(a.Origin.Spec, sel) {
			removed = append(removed, a)
			continue
		}
		keep = append(keep, st)
	}
	if len(removed) == 0 {
		return nil, fmt.Errorf("%s records no allow %q (see `sngl trust --list`)", path, sel)
	}
	doc.Stmts = keep
	return removed, writeAtomic(path, []byte(parser.Format(doc)))
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".trust-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
