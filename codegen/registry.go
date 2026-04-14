package codegen

import (
	"slices"
	"sync"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

var (
	langMu    sync.RWMutex
	langs     = map[string]LangTranslator{}
	platMu    sync.RWMutex
	platforms = map[string]PlatformGenerator{}
)

// RegisterLang registers a language translator. Panics on duplicate or
// if the provider's PkgSource fails to parse.
func RegisterLang(l LangTranslator) {
	langMu.Lock()
	defer langMu.Unlock()
	name := l.Lang()
	if _, ok := langs[name]; ok {
		panic("codegen: duplicate lang registration: " + name)
	}
	validatePkgSource(l.PkgSource(), name)
	langs[name] = l
}

// RegisterPlatform registers a platform generator. Panics on duplicate or
// if the provider's PkgSource fails to parse.
func RegisterPlatform(p PlatformGenerator) {
	platMu.Lock()
	defer platMu.Unlock()
	name := p.Platform()
	if _, ok := platforms[name]; ok {
		panic("codegen: duplicate platform registration: " + name)
	}
	validatePkgSource(p.PkgSource(), name)
	platforms[name] = p
}

// validatePkgSource parses a .sngl package source at registration time
// and panics if it contains syntax errors.
func validatePkgSource(src, name string) {
	if src == "" {
		return
	}
	// TODO: Re-enable validation once platform .sngl files use v2 syntax.
	// The v2 parser does not yet support qualified component names (e.g., sngl.vbox).
	_, _ = parser.Parse(name+".sngl", []byte(src))
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

// Langs returns the names of all registered languages.
func Langs() []string {
	langMu.RLock()
	defer langMu.RUnlock()
	names := make([]string, 0, len(langs))
	for name := range langs {
		names = append(names, name)
	}
	return names
}

// Platforms returns the names of all registered platforms.
func Platforms() []string {
	platMu.RLock()
	defer platMu.RUnlock()
	names := make([]string, 0, len(platforms))
	for name := range platforms {
		names = append(names, name)
	}
	return names
}

// PlatformsForLang returns the platform names that support the given language.
func PlatformsForLang(lang string) []string {
	platMu.RLock()
	defer platMu.RUnlock()
	var names []string
	for name, p := range platforms {
		if slices.Contains(p.SupportedLangs(), lang) {
			names = append(names, name)
		}
	}
	return names
}
