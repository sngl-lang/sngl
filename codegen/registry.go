package codegen

import (
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"slices"
	"sort"
	"sync"

	"git.duckfam.us/jonathan/sngl/ir"
)

var (
	langMu    sync.RWMutex
	langs     = map[string]LangTranslator{}
	platMu    sync.RWMutex
	platforms = map[string]PlatformGenerator{}
)

// RegisterLang registers a language translator. Panics on duplicate.
func RegisterLang(l LangTranslator) {
	langMu.Lock()
	defer langMu.Unlock()
	name := l.LanguageIdentifier()
	if _, ok := langs[name]; ok {
		panic("codegen: duplicate lang registration: " + name)
	}
	langs[name] = l
	checker.RegisterTargetPackage("language/"+name, l)
}

// RegisterPlatform registers a platform generator. Panics on duplicate.
func RegisterPlatform(p PlatformGenerator) {
	platMu.Lock()
	defer platMu.Unlock()
	name := p.PlatformIdentifier()
	if _, ok := platforms[name]; ok {
		panic("codegen: duplicate platform registration: " + name)
	}
	platforms[name] = p
	checker.RegisterTargetPackage("platform/"+name, p)
}

// CollectPlatforms returns all registered platforms as checker.Platform slices,
// ordered by platform identifier.
func CollectPlatforms() []ir.Platform {
	platMu.RLock()
	defer platMu.RUnlock()
	names := make([]string, 0, len(platforms))
	for name := range platforms {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ir.Platform, 0, len(platforms))
	for _, name := range names {
		out = append(out, platforms[name])
	}
	return out
}

// CollectLangs returns all registered languages, ordered by identifier.
//
// A check that names every platform needs this too: a platform's own source may
// import sngl:language/<name>, and that resolves against this list whatever
// language the build is for.
func CollectLangs() []ir.Language {
	langMu.RLock()
	defer langMu.RUnlock()
	names := make([]string, 0, len(langs))
	for name := range langs {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ir.Language, 0, len(langs))
	for _, name := range names {
		out = append(out, langs[name])
	}
	return out
}

// LookupLang returns the translator for the given language, or nil. A
// language whose package is in lib/ is the Go translator its node names with
// #[gen.native], or the declared language when it names none.
func LookupLang(lang string) LangTranslator {
	langMu.RLock()
	l := langs[lang]
	langMu.RUnlock()
	if d, ok := l.(*DeclaredLang); ok {
		return d.resolve()
	}
	return l
}

// LookupPlatform returns the generator for the given platform, or nil. A
// platform whose package is in lib/ is the Go generator its node names with
// #[gen.native], or the declared platform when it names none.
func LookupPlatform(platform string) PlatformGenerator {
	platMu.RLock()
	p := platforms[platform]
	platMu.RUnlock()
	if d, ok := p.(*DeclaredPlatform); ok {
		return d.resolve()
	}
	return p
}

// Langs returns the names of all registered languages, sorted.
func Langs() []string {
	langMu.RLock()
	defer langMu.RUnlock()
	names := make([]string, 0, len(langs))
	for name := range langs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Platforms returns the names of all registered platforms, sorted.
func Platforms() []string {
	platMu.RLock()
	defer platMu.RUnlock()
	names := make([]string, 0, len(platforms))
	for name := range platforms {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// PlatformsForLang returns the platform names that support the given language, sorted.
func PlatformsForLang(lang string) []string {
	var names []string
	for _, name := range Platforms() {
		if slices.Contains(LookupPlatform(name).SupportedLangs(), lang) {
			names = append(names, name)
		}
	}
	return names
}
