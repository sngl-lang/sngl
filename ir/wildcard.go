package ir

import (
	"regexp"
	"sync"
)

// A wildcard pattern matches a whole name or nothing: `a` does not stand for
// `data`. The pattern is stored as written, so the anchors are added here
// rather than in the declaration, and \A...\z rather than ^...$ so a pattern
// cannot opt into matching a line of a multi-line name.
var (
	wildcardMu    sync.Mutex
	wildcardCache = map[string]*regexp.Regexp{}
)

// CompileWildcard compiles a wildcard pattern, reporting a syntax error
// against the pattern as written rather than the anchored form.
func CompileWildcard(pattern string) (*regexp.Regexp, error) {
	wildcardMu.Lock()
	defer wildcardMu.Unlock()
	if re, ok := wildcardCache[pattern]; ok {
		return re, nil
	}
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

// MatchesWildcard reports whether a pattern covers name. A pattern that failed
// to compile was reported where it was written, so it matches nothing here
// rather than being reported again.
func MatchesWildcard(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	re, err := CompileWildcard(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(name)
}
