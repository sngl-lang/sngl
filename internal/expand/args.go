package expand

import (
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Args holds a macro's evaluated arguments, addressable by parameter name.
type Args struct {
	params []Param
	vals   []argVal
}

type argVal struct {
	set    bool // false when an optional parameter was omitted
	str    string
	num    int64
	ident  string
	idents []string // ArgIdent + Variadic
	expr   ast.Expr
}

func (a Args) index(name string) int {
	for i, p := range a.params {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// Has reports whether an (optional) argument was supplied.
func (a Args) Has(name string) bool {
	i := a.index(name)
	return i >= 0 && a.vals[i].set
}

// String returns an ArgString parameter's value ("" if absent).
func (a Args) String(name string) string {
	if i := a.index(name); i >= 0 {
		return a.vals[i].str
	}
	return ""
}

// Int returns an ArgInt parameter's value (0 if absent).
func (a Args) Int(name string) int64 {
	if i := a.index(name); i >= 0 {
		return a.vals[i].num
	}
	return 0
}

// Ident returns an ArgIdent parameter's identifier name ("" if absent).
func (a Args) Ident(name string) string {
	if i := a.index(name); i >= 0 {
		return a.vals[i].ident
	}
	return ""
}

// Expr returns an ArgExpr parameter's raw expression (nil if absent).
func (a Args) Expr(name string) ast.Expr {
	if i := a.index(name); i >= 0 {
		return a.vals[i].expr
	}
	return nil
}

// evalArgs validates raw macro arguments against the declared parameters and
// evaluates each to its kind. Arguments are positional; optional parameters
// (which must come last) may be omitted from the tail, and a trailing variadic
// parameter takes however many remain.
func evalArgs(params []Param, raw []ast.Expr) (Args, error) {
	required, variadic := 0, false
	for i, p := range params {
		if p.Variadic {
			if i != len(params)-1 {
				return Args{}, fmt.Errorf("variadic parameter %q must be last", p.Name)
			}
			variadic = true
			continue
		}
		if !p.Optional {
			required++
		}
	}
	if len(raw) < required || (!variadic && len(raw) > len(params)) {
		return Args{}, fmt.Errorf("expected %s, got %d", arityDesc(required, len(params), variadic), len(raw))
	}
	vals := make([]argVal, len(params))
	for i := range params {
		if params[i].Variadic {
			rest := raw[min(i, len(raw)):]
			idents := make([]string, 0, len(rest))
			for _, e := range rest {
				v, err := evalArg(params[i], e)
				if err != nil {
					return Args{}, fmt.Errorf("argument %q: %w", params[i].Name, err)
				}
				idents = append(idents, v.ident)
			}
			vals[i] = argVal{set: true, idents: idents}
			break
		}
		if i >= len(raw) {
			break // remaining params are optional and omitted
		}
		v, err := evalArg(params[i], raw[i])
		if err != nil {
			return Args{}, fmt.Errorf("argument %q: %w", params[i].Name, err)
		}
		vals[i] = v
	}
	return Args{params: params, vals: vals}, nil
}

func evalArg(p Param, e ast.Expr) (argVal, error) {
	switch p.Kind {
	case ArgString:
		s, err := ast.EvalString(e)
		if err != nil {
			return argVal{}, err
		}
		return argVal{set: true, str: s}, nil
	case ArgInt:
		n, err := ast.EvalInt(e)
		if err != nil {
			return argVal{}, err
		}
		return argVal{set: true, num: n}, nil
	case ArgIdent:
		id, ok := e.(*ast.IdentExpr)
		if !ok {
			return argVal{}, fmt.Errorf("expected an identifier")
		}
		if len(p.Enum) > 0 && !slices.Contains(p.Enum, id.Name) {
			return argVal{}, fmt.Errorf("unknown value %q (want one of: %s)", id.Name, strings.Join(p.Enum, ", "))
		}
		return argVal{set: true, ident: id.Name}, nil
	case ArgExpr:
		return argVal{set: true, expr: e}, nil
	default:
		return argVal{}, fmt.Errorf("unknown parameter kind %d", p.Kind)
	}
}

func arityDesc(required, total int, variadic bool) string {
	if variadic {
		if required == 1 {
			return "at least 1 argument"
		}
		return fmt.Sprintf("at least %d arguments", required)
	}
	if required == total {
		if total == 1 {
			return "1 argument"
		}
		return fmt.Sprintf("%d arguments", total)
	}
	return fmt.Sprintf("%d to %d arguments", required, total)
}

// Empty when the mark supplied none.
func (a Args) Idents(name string) []string {
	if i := a.index(name); i >= 0 {
		return a.vals[i].idents
	}
	return nil
}
