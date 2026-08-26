package checker

import (
	"fmt"
	"regexp"
	"sync"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A wildcard pattern matches a whole name or nothing: `a` does not stand for
// `data`. The pattern is stored as written, so the anchors are added here
// rather than in the IR, and \A...\z rather than ^...$ so a pattern cannot opt
// into matching a line of a multi-line name.
var (
	wildcardMu    sync.Mutex
	wildcardCache = map[string]*regexp.Regexp{}
)

func compileWildcard(pattern string) (*regexp.Regexp, error) {
	wildcardMu.Lock()
	defer wildcardMu.Unlock()
	if re, ok := wildcardCache[pattern]; ok {
		return re, nil
	}
	// Compile the pattern as written first, so a syntax error names what the
	// author typed rather than the anchors added around it.
	if _, err := regexp.Compile(pattern); err != nil {
		return nil, err
	}
	re, err := regexp.Compile(`\A(?:` + pattern + `)\z`)
	if err != nil {
		return nil, err
	}
	wildcardCache[pattern] = re
	return re, nil
}

// wildcardMatches reports whether a wildcard pattern accepted at check time
// covers name. A pattern that failed to compile was reported where it was
// written, so it matches nothing here rather than being reported again.
func wildcardMatches(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	re, err := compileWildcard(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

// wildcardProp returns the prop of comp whose wildcard pattern covers name.
// A declared prop of that name beats every wildcard, so the caller looks one
// up first; two wildcards covering one name is ambiguous and reported by the
// caller, which knows the use site.
func wildcardProp(comp *ir.Component, name string) (*ir.Prop, bool, error) {
	var found *ir.Prop
	for _, p := range comp.Props {
		if !wildcardMatches(p.Wildcard, name) {
			continue
		}
		if found != nil {
			return nil, false, fmt.Errorf("prop %q matches both wildcard props %q and %q on component %s", name, found.Name, p.Name, comp.Name)
		}
		found = p
	}
	return found, found != nil, nil
}

// wildcardComponent returns the component of pkg whose wildcard pattern covers
// name — the declaration a name nobody declared resolves to. A declared name
// beats every wildcard, so the caller looks one up first.
func wildcardComponent(pkg *ir.Package, name string) (*ir.Component, error) {
	if pkg == nil {
		return nil, nil
	}
	var found *ir.Component
	for _, comp := range pkg.Components {
		if !wildcardMatches(comp.Wildcard, name) {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%q matches both wildcard components %s and %s", name, found.Name, comp.Name)
		}
		found = comp
	}
	return found, nil
}
