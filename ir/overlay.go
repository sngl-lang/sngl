package ir

import "maps"

// Overlay is what a program adds to declarations a library package owns: its
// overrides of library components and functions. A check records them here
// rather than in the library, so the library a check reads is the library the
// next check reads. What a build does with the program reads them off the
// declarations, which InstallOverlay writes them onto.
//
// A program's methods on a library type are the overlay's other half, held on
// the program's package-root scope (Scope.Added) since a method is found by a
// scope lookup.
type Overlay struct {
	Overrides map[Symbol]*Overrides `json:"-"`
}

// Overrides are a declaration's per-target bodies, as a Component's and a
// Func's PlatformOverrides and LanguageOverrides hold them.
type Overrides struct {
	Platform map[string]Override
	Language map[string]Override
}

// Of is the map an override for the tier kind is recorded in.
func (o *Overrides) Of(kind BuiltinKind) *map[string]Override {
	if kind == BuiltinFamilyLanguage {
		return &o.Language
	}
	return &o.Platform
}

// OverridesOf is the override maps sym carries itself, for a Component or a
// Func; nil for any other symbol.
func OverridesOf(sym Symbol) *Overrides {
	switch d := sym.(type) {
	case *Component:
		return &Overrides{Platform: d.PlatformOverrides, Language: d.LanguageOverrides}
	case *Func:
		return &Overrides{Platform: d.PlatformOverrides, Language: d.LanguageOverrides}
	}
	return nil
}

// For is the overlay entry of sym, made on first use.
func (o *Overlay) For(sym Symbol) *Overrides {
	if o.Overrides == nil {
		o.Overrides = map[Symbol]*Overrides{}
	}
	ov := o.Overrides[sym]
	if ov == nil {
		ov = &Overrides{}
		o.Overrides[sym] = ov
	}
	return ov
}

// InstallOverlay writes the overrides pkg reached onto the declarations they
// override -- the library's (Targets), then the program's own (Overlay) --
// so every phase after the check finds an override on the declaration.
// Installing twice installs once.
//
// It writes the library declarations pkg reaches, so pkg is a build's copy
// (ClonePackage), never the checked package every later build reads.
func InstallOverlay(pkg *Package) {
	if pkg == nil {
		return
	}
	installOverlay(pkg.Targets)
	installOverlay(pkg.Overlay)
}

func installOverlay(o Overlay) {
	for sym, ov := range o.Overrides {
		var plat, lang *map[string]Override
		switch d := sym.(type) {
		case *Component:
			d.mustWrite("installing an override of " + d.Name)
			plat, lang = &d.PlatformOverrides, &d.LanguageOverrides
		case *Func:
			d.mustWrite("installing an override of " + d.Name)
			plat, lang = &d.PlatformOverrides, &d.LanguageOverrides
		default:
			continue
		}
		install(plat, ov.Platform)
		install(lang, ov.Language)
	}
}

func install(dst *map[string]Override, src map[string]Override) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]Override, len(src))
	}
	maps.Copy((*dst), src)
}

// Any reports whether o holds an override for any target.
func (o Overrides) Any() bool { return len(o.Platform) > 0 || len(o.Language) > 0 }

// Merged is o with add's overrides beside its own, as a new overlay: o and its
// maps are left as they are, since a check may still be reading them.
func (o Overlay) Merged(add Overlay) Overlay {
	if len(add.Overrides) == 0 {
		return o
	}
	out := Overlay{Overrides: maps.Clone(o.Overrides)}
	if out.Overrides == nil {
		out.Overrides = make(map[Symbol]*Overrides, len(add.Overrides))
	}
	for sym, ov := range add.Overrides {
		merged := &Overrides{}
		if held := out.Overrides[sym]; held != nil {
			merged.Platform, merged.Language = maps.Clone(held.Platform), maps.Clone(held.Language)
		}
		install(&merged.Platform, ov.Platform)
		install(&merged.Language, ov.Language)
		out.Overrides[sym] = merged
	}
	return out
}

// OverridesOf is every override sym has as pkg sees it: its own, the
// library's and the program's. A checked package reads them this way; a
// build's copy has them installed on the declaration.
func (pkg *Package) OverridesOf(sym Symbol) Overrides {
	all := OverridesOf(sym)
	if all == nil {
		return Overrides{}
	}
	out := Overrides{Platform: all.Platform, Language: all.Language}
	for _, o := range []Overlay{pkg.Targets, pkg.Overlay} {
		if added := o.Overrides[sym]; added != nil {
			out.Platform, out.Language = withOverrides(out.Platform, added.Platform), withOverrides(out.Language, added.Language)
		}
	}
	return out
}

func withOverrides(own, added map[string]Override) map[string]Override {
	if len(added) == 0 {
		return own
	}
	out := maps.Clone(own)
	install(&out, added)
	return out
}
