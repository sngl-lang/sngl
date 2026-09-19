package goldentest

import (
	"fmt"
	"strings"
)

// An exemption is one target the host toolchain is not asked to pass.
//
// There are two reasons for that and they must not share a spelling. The
// existing pair in internal/testutil says why: a missing tool and an
// unimplemented feature were nearly read off one list, and the comment there
// records what that would have cost -- a genuinely broken toolchain hiding as
// a feature gap. The same hazard, one level up.
//
//	unverifiable out/go/bubbletea -- imports example.com/gfx, which does not exist
//	broken       out/go/bubbletea -- emits map[i18n.PluralKey]string where i18n.Plural takes map[any]string
//
// `unverifiable` is a property of the fixture: it describes a foreign API that
// is deliberately fictional, or reaches a network, so no host anywhere can
// compile what it generates. It is permanent and says nothing about the
// compiler.
//
// `broken` is a defect. The generated code does not compile and somebody has
// to fix it. It is quarantine, and the only honest quarantine is one that
// cannot rot: the toolchain still runs, and the directive passes only while
// the code still fails. Fix the defect and the fixture fails until the
// directive goes -- which is the same discipline NOFMT is held to, and the
// reason a `broken` target is not simply skipped.
type exemption struct {
	line     int
	lang     string
	platform string
	broken   bool
	reason   string
}

func (e exemption) target() string { return e.lang + "/" + e.platform }

// parseExemptions reads the `unverifiable` and `broken` directives out of the
// archive comment.
func parseExemptions(comment string) (map[string]exemption, error) {
	out := map[string]exemption{}
	for i, raw := range strings.Split(comment, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
		var broken bool
		rest, ok := strings.CutPrefix(line, "unverifiable")
		if !ok {
			if rest, ok = strings.CutPrefix(line, "broken"); !ok {
				continue
			}
			broken = true
		}
		if rest != "" && !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t") {
			continue
		}
		e, err := parseExemption(strings.TrimSpace(rest), broken)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w\n\t%s", i+1, err, line)
		}
		e.line = i + 1
		if prev, dup := out[e.target()]; dup {
			return nil, fmt.Errorf("line %d: %s is already exempted on line %d", e.line, e.target(), prev.line)
		}
		out[e.target()] = e
	}
	return out, nil
}

func parseExemption(rest string, broken bool) (exemption, error) {
	name := "unverifiable"
	if broken {
		name = "broken"
	}
	form := fmt.Sprintf("want: %s out/<lang>/<platform> -- reason", name)

	target, reason, ok := strings.Cut(rest, " -- ")
	target = strings.TrimSpace(target)
	if !ok || strings.TrimSpace(reason) == "" {
		return exemption{}, fmt.Errorf("%s names no reason (%s)", name, form)
	}
	spec, ok := strings.CutPrefix(target, goldenPrefix)
	if !ok {
		return exemption{}, fmt.Errorf("%s target %q is not a golden path (%s)", name, target, form)
	}
	lang, platform, ok := strings.Cut(strings.TrimSuffix(spec, "/"), "/")
	if !ok || lang == "" || platform == "" {
		return exemption{}, fmt.Errorf("%s target %q is not <lang>/<platform> (%s)", name, target, form)
	}
	return exemption{lang: lang, platform: platform, broken: broken, reason: strings.TrimSpace(reason)}, nil
}
