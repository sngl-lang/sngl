package html

import (
	"fmt"
	"html"
	"maps"
	"slices"
	"strings"

	"duckfam.us/sngl/internal/asset"
	"duckfam.us/sngl/ir"
)

// sharedConstAsset is a package const a static site writes once, as a script
// every page that reads it loads, rather than into each page's own script.
type sharedConstAsset struct {
	file htmlAssetFile
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
	var named map[string]bool
	for _, c := range g.pkg.Consts {
		if !g.sharesConst(c) {
			continue
		}
		if named == nil {
			named = scriptIdents(script)
		}
		if !named[c.Name] {
			continue
		}
		a := g.sharedConst(c)
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

// scriptIdents is every name script reads as a free identifier: each run of
// identifier characters not reached through a `.`. A shared const is linked
// into a page whose script names it.
//
// One pass per page. It was a regexp per shared const per page, each scanning
// the whole script, which on the docs site -- a thousand pages -- was a third
// of the compiler's CPU.
func scriptIdents(script string) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < len(script); {
		if !isJSIdentByte(script[i]) {
			i++
			continue
		}
		start := i
		for i < len(script) && isJSIdentByte(script[i]) {
			i++
		}
		if start == 0 || script[start-1] != '.' {
			out[script[start:i]] = true
		}
	}
	return out
}

func isJSIdentByte(b byte) bool {
	return b == '_' || b == '$' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}
