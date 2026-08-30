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

// LookupLang returns the translator for the given language, or nil.
func LookupLang(lang string) LangTranslator {
	langMu.RLock()
	defer langMu.RUnlock()
	return langs[lang]
}

// LookupPlatform returns the generator for the given platform, or nil.
func LookupPlatform(platform string) PlatformGenerator {
	platMu.RLock()
	defer platMu.RUnlock()
	return platforms[platform]
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
	platMu.RLock()
	defer platMu.RUnlock()
	var names []string
	for name, p := range platforms {
		if slices.Contains(p.SupportedLangs(), lang) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
