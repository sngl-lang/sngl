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

// InstallOverlay writes pkg's overlay onto the declarations it overrides, so
// every phase after the check finds a program's override where it finds a
// target package's. Installing twice installs once.
//
// It writes the library declarations pkg reaches, so pkg is a build's copy
// (ClonePackage), never the checked package every later build reads.
func InstallOverlay(pkg *Package) {
	if pkg == nil {
		return
	}
	for sym, ov := range pkg.Overlay.Overrides {
		var plat, lang *map[string]Override
		switch d := sym.(type) {
		case *Component:
			plat, lang = &d.PlatformOverrides, &d.LanguageOverrides
		case *Func:
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
