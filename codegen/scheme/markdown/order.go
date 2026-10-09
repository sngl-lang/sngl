package markdown

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	snglast "duckfam.us/sngl/ast"
)

// mdPackage is where #[md.order] is declared.
const mdPackage = "sngl:ui/markup/md"

// orderKey is the Frontmatter field a page's children sort by, and the value a
// page that omits it sorts as.
type orderKey struct {
	name, typ string
	def       sortValue
}

type sortValue struct {
	n float64
	s string
}

func (a sortValue) compare(b sortValue) int {
	return cmp.Or(cmp.Compare(a.n, b.n), cmp.Compare(a.s, b.s))
}

// findOrderKey reads #[md.order] off a declared Frontmatter. doc is the file
// declaring it, whose imports say what the mark's alias names.
func findOrderKey(doc *snglast.Document, sd *snglast.StructDef, file, label string) (*orderKey, error) {
	aliases := map[string]bool{}
	for _, s := range doc.Stmts {
		if im, ok := s.(*snglast.Import); ok && im.Path == mdPackage {
			if im.IsDot() {
				aliases[""] = true
			} else {
				aliases[im.Alias] = true
			}
		}
	}
	var key *orderKey
	for _, f := range sd.Fields() {
		marked := slices.ContainsFunc(f.Attrs, func(a snglast.MacroAttr) bool {
			return a.Name == "order" && aliases[a.Alias]
		})
		if !marked || len(f.Names) != 1 {
			continue // the checker reports a mark on several names
		}
		if key != nil {
			return nil, fmt.Errorf("md: %s/%s: #[md.order] is on both %q and %q; pages are sorted by one key", label, file, key.name, f.Names[0])
		}
		nt, _ := f.Type.(*snglast.NamedType)
		if nt == nil || nt.Package != "" {
			return nil, nil // the checker refuses a type that is not a sort key
		}
		k := &orderKey{name: f.Names[0], typ: nt.Name}
		if f.Default != nil {
			v, err := k.eval(f.Default)
			if err != nil {
				return nil, fmt.Errorf("md: %s/%s: the default of the order key %q: %w; the importer sorts before anything is checked", label, file, k.name, err)
			}
			k.def = v
		}
		key = k
	}
	return key, nil
}

func (k *orderKey) eval(e snglast.Expr) (sortValue, error) {
	switch k.typ {
	case "string":
		s, err := snglast.EvalString(e)
		return sortValue{s: s}, err
	case "int":
		n, err := snglast.EvalInt(e)
		return sortValue{n: float64(n)}, err
	case "float":
		neg := false
		if u, ok := e.(*snglast.UnaryExpr); ok && u.Op == snglast.UnaryNeg {
			neg, e = true, u.Operand
		}
		lit, ok := e.(*snglast.LiteralExpr)
		if !ok || (lit.Kind != snglast.LiteralFloat && lit.Kind != snglast.LiteralInt) {
			return sortValue{}, fmt.Errorf("not a number literal")
		}
		n, err := strconv.ParseFloat(lit.Raw, 64)
		if neg {
			n = -n
		}
		return sortValue{n: n}, err
	}
	return sortValue{}, fmt.Errorf("a sort key is an int, a float or a string, not %s", k.typ)
}

// value is what p sorts as.
func (k *orderKey) value(p *page, label string) (sortValue, error) {
	for _, c := range p.front {
		if c.name != k.name {
			continue
		}
		if c.typ != k.typ && !(c.typ == "int" && k.typ == "float") {
			return sortValue{}, fmt.Errorf("md: %s/%s: the order key %q is %s here and %s in Frontmatter", label, p.file, k.name, c.typ, k.typ)
		}
		var n float64
		var err error
		switch c.typ {
		case "string":
			return sortValue{s: c.raw}, nil
		case "int":
			var i int64
			i, err = strconv.ParseInt(c.raw, 0, 64)
			n = float64(i)
		default:
			n, err = strconv.ParseFloat(c.raw, 64)
		}
		if err != nil {
			return sortValue{}, fmt.Errorf("md: %s/%s: the order key %q: %w", label, p.file, k.name, err)
		}
		return sortValue{n: n}, nil
	}
	return k.def, nil
}

// sortChildren orders every page's children by the key, keeping path order
// among equal keys.
func (k *orderKey) sortChildren(pages []*page, label string) error {
	values := map[*page]sortValue{}
	for _, p := range pages {
		v, err := k.value(p, label)
		if err != nil {
			return err
		}
		values[p] = v
	}
	for _, p := range pages {
		slices.SortStableFunc(p.children, func(a, b *page) int { return values[a].compare(values[b]) })
	}
	return nil
}
