package codegen

import "sync"

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
	name := l.Lang()
	if _, ok := langs[name]; ok {
		panic("codegen: duplicate lang registration: " + name)
	}
	langs[name] = l
}

// RegisterPlatform registers a platform generator. Panics on duplicate.
func RegisterPlatform(p PlatformGenerator) {
	platMu.Lock()
	defer platMu.Unlock()
	name := p.Platform()
	if _, ok := platforms[name]; ok {
		panic("codegen: duplicate platform registration: " + name)
	}
	platforms[name] = p
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
