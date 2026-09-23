package html

import (
	"fmt"
	"html"
	"maps"
	"regexp"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/asset"
	"git.duckfam.us/jonathan/sngl/ir"
)

// sharedConstAsset is a package const a static site writes once, as a script
// every page that reads it loads, rather than into each page's own script.
type sharedConstAsset struct {
	file htmlAssetFile
	ref  *regexp.Regexp
	// linked is set by the first page that loads it, which is what writes
	// the file: a const no page reads is not worth one.
	linked bool
}

// sharedConstGlobal is the object the asset scripts hang their values on. A
// const bound as a global of its own name would contend with the window's
// properties -- `name`, `status` and `top` are all taken.
const sharedConstGlobal = "globalThis.__snglShared"

// sharesConst reports whether c is written to a shared asset instead of the
// page. Only a list or a map is: those are what the optimizer leaves as a
// reference at every read (optimize.sharedAggregateConsts), so every page
// that reads one would otherwise carry a copy of all of it.
func (g *htmlGen) sharesConst(c *ir.Var) bool {
	if !g.shareConsts || c.Init == nil || c.Type == nil {
		return false
	}
	if c.Type.Kind != ir.TypeList && c.Type.Kind != ir.TypeMap {
		return false
	}
	if !slices.Contains(g.pkg.Consts, c) {
		return false
	}
	return g.sharedConst(c) != nil
}

// sharedConst returns c's asset, or nil when c's literal needs a helper only
// a page's own script defines.
func (g *htmlGen) sharedConst(c *ir.Var) *sharedConstAsset {
	s := g.shared
	if a, ok := s.constAssets[c]; ok {
		return a
	}
	if s.constAssets == nil {
		s.constAssets = map[*ir.Var]*sharedConstAsset{}
	}
	before := maps.Clone(g.ctx.Helpers)
	lit := g.literalToJS(c.Init)
	if !maps.Equal(before, g.ctx.Helpers) {
		clear(g.ctx.Helpers)
		maps.Copy(g.ctx.Helpers, before)
		s.constAssets[c] = nil
		return nil
	}
	data := fmt.Appendf(nil, "%s = %s || {};\n%s.%s = %s;\n", sharedConstGlobal, sharedConstGlobal, sharedConstGlobal, c.Name, lit)
	name := c.Name + ".js"
	if !g.noCacheBust {
		name = asset.HashedName(name, data)
	}
	a := &sharedConstAsset{
		file: htmlAssetFile{name: "assets/consts/" + name, bytes: data},
		ref:  regexp.MustCompile(`(^|[^A-Za-z0-9_$.])` + regexp.QuoteMeta(c.Name) + `($|[^A-Za-z0-9_$])`),
	}
	s.constAssets[c] = a
	return a
}

// linkSharedConsts binds each shared const the page's script reads to the
// value its asset carries, and returns the tags that load those assets.
// Binding at the top of the script is what lets the state initializer, which
// is written first, read one.
func (g *htmlGen) linkSharedConsts(script string) (tags, linked string) {
	if !g.shareConsts || g.pkg == nil {
		return "", script
	}
	var tagBuf, bind strings.Builder
	for _, c := range g.pkg.Consts {
		if !g.sharesConst(c) {
			continue
		}
		a := g.sharedConst(c)
		if !a.ref.MatchString(script) {
			continue
		}
		if !a.linked {
			a.linked = true
			g.shared.constFiles = append(g.shared.constFiles, a.file)
		}
		fmt.Fprintf(&tagBuf, "<script src=\"/%s\"></script>\n", html.EscapeString(a.file.name))
		fmt.Fprintf(&bind, "const %s = %s.%s;\n", c.Name, sharedConstGlobal, c.Name)
	}
	if bind.Len() == 0 {
		return "", script
	}
	return tagBuf.String(), bind.String() + script
}
