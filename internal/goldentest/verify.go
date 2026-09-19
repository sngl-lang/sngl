package goldentest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"testing"

	"errors"
	"git.duckfam.us/jonathan/sngl/internal/toolchain"
)

// recordPrefix is the archive directory holding what a host toolchain said
// about the golden beside it.
const recordPrefix = "run/"

// A record is one target's verification: the host compiled this exact
// generated output, and ran whatever tests it carried, and it passed.
//
// The digest is the point of the file. Without it a record states that some
// output passed and not which, so a codegen change would leave a stale `pass`
// standing over output nobody had compiled. With it, "has this been verified"
// is answerable from the archive alone -- which is what lets the default run
// skip the compiler, and lets CI skip it too, on a runner with no warm cache
// and no memory of the last pipeline.
//
// It is not the golden's digest. The two travel together today and need not:
// a target may be verified from generated output the golden does not show, as
// a fixture carrying tests is, and keying off the golden would then claim a
// verification of bytes the toolchain never saw.
// The status is `pass`, or `broken` for a target a `broken` directive
// quarantines. Both are verifications and both are cached the same way: a
// quarantined defect that recompiled on every run would make the default run
// pay for exactly the fixtures nobody has got to yet, and the point of a
// record is that a toolchain result already reached is not reached again.
type record struct {
	status string
	digest string
}

func (r record) String() string { return r.status + " " + r.digest + "\n" }

func parseRecord(name string, data []byte) (record, error) {
	fields := strings.Fields(string(data))
	if len(fields) != 2 || (fields[0] != "pass" && fields[0] != "broken") {
		return record{}, fmt.Errorf("%s: want `pass <digest>` or `broken <digest>`, got %q", name, strings.TrimSpace(string(data)))
	}
	return record{status: fields[0], digest: fields[1]}, nil
}

// digest fingerprints what the toolchain is handed. Sorted, and with the names
// in the hash beside the content: two targets whose files differ only in where
// they were written are two different programs.
func digest(files map[string][]byte) string {
	h := sha256.New()
	for _, name := range sortedKeys(files) {
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(files[name]))
		h.Write(files[name])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)[:16])
}

// target is one lang/platform pair and the generated files belonging to it,
// keyed by their path relative to the target's own directory -- which is what
// a compiler wants, the `out/<lang>/<platform>/` prefix being this harness's
// filing rather than part of the program.
type target struct {
	lang, platform string
	files          map[string][]byte
}

func (tg target) recordName() string { return recordPrefix + tg.lang + "/" + tg.platform }

// targetsOf regroups generated output by the target that produced it.
func targetsOf(got map[string][]byte) []target {
	byDir := map[string]map[string][]byte{}
	for _, name := range sortedKeys(got) {
		rest := strings.TrimPrefix(name, goldenPrefix)
		lang, rest, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}
		plat, file, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}
		dir := lang + "/" + plat
		if byDir[dir] == nil {
			byDir[dir] = map[string][]byte{}
		}
		byDir[dir][file] = got[name]
	}
	out := make([]target, 0, len(byDir))
	for _, dir := range sortedStrings(byDir) {
		lang, plat, _ := strings.Cut(dir, "/")
		out = append(out, target{lang: lang, platform: plat, files: byDir[dir]})
	}
	return out
}

// verify holds each target to its record, and is where the compiler does or
// does not run.
//
// The rule, and it is the whole design:
//
//   - the digest matches the record — the toolchain has already seen these
//     exact bytes and passed. Nothing runs.
//   - it does not, and update is set — run the toolchain and write what it
//     said. This is the only path that compiles in the ordinary course of
//     work, and it runs for the changed targets alone.
//   - it does not, and update is not set — a failure naming -update, the same
//     answer a golden mismatch gives, for the same reason.
//   - force is set — run the toolchain whatever the record says. A record goes
//     stale when the *toolchain* changes under it, which no fixture edit
//     announces, so something has to ask.
//
// A host missing the toolchain skips, and on update leaves whatever record was
// committed untouched: a verification that did not happen must not be written
// down as one.
func verify(t *testing.T, tgts []target, records map[string]record, exempt map[string]exemption, update, force bool) map[string]record {
	t.Helper()
	out := map[string]record{}
	seen := map[string]bool{}
	for _, tg := range tgts {
		name := tg.recordName()
		have, recorded := records[name]
		want := digest(tg.files)
		ex, exempted := exempt[tg.lang+"/"+tg.platform]
		seen[tg.lang+"/"+tg.platform] = true

		if exempted && !ex.broken {
			// Nothing to run and nothing to record: no host can compile this,
			// so a record would be a claim about a machine that does not exist.
			t.Logf("%s: unverifiable — %s", name, ex.reason)
			continue
		}
		if reason := toolchain.Unavailable(tg.lang, tg.platform); reason != "" {
			if recorded {
				out[name] = have
			}
			t.Logf("%s: %s", name, reason)
			continue
		}
		// Only asked of output that actually runs something. Plain generated
		// output has no test file, so the toolchain compiles it and presents
		// nothing -- gating that on a compositor skipped gtk4's compile for a
		// window it was never going to open.
		if runsTests(tg.files) {
			if reason := toolchain.PresentsWindows(tg.platform); reason != "" {
				if recorded {
					out[name] = have
				}
				t.Logf("%s: %s", name, reason)
				continue
			}
		}
		// What the record must say for this target, so a directive added or
		// removed without the output moving is still caught by the digest
		// check below rather than silently inheriting the old verdict.
		status := "pass"
		if ex.broken {
			status = "broken"
		}
		if recorded && have.digest == want && have.status == status && !force {
			out[name] = have
			continue
		}
		if !update && !force {
			switch {
			case !recorded:
				t.Errorf("%s: never verified on a host that could; run -update", name)
			case have.digest != want:
				t.Errorf("%s: generated output changed since it was verified; run -update", name)
			default:
				// Same bytes, different verdict asked for: a `broken`
				// directive added over a passing target or removed from a
				// failing one. Saying "the output changed" here sent a reader
				// looking for a codegen diff that is not there.
				t.Errorf("%s: recorded %s but the fixture now declares %s; run -update", name, have.status, status)
			}
			if recorded {
				out[name] = have
			}
			continue
		}

		output, err := toolchain.Build(t.Context(), tg.files, tg.lang, tg.platform)
		if se, ok := errors.AsType[*toolchain.SkipError](err); ok {
			if recorded {
				out[name] = have
			}
			t.Logf("%s: %s", name, se.Reason)
			continue
		}
		// The toolchain's own output is the whole of what makes either verdict
		// diagnosable, and it is long -- t.Log rather than the failure message,
		// so a run reporting several targets stays readable.
		if ex.broken {
			// Quarantine that cannot rot. The directive stands only while the
			// defect does; fix it and the fixture fails until the line goes.
			if err == nil {
				t.Errorf("%s: marked broken on line %d (%s), but it compiles now — drop the directive", name, ex.line, ex.reason)
				continue
			}
			t.Logf("%s: broken as declared — %s\n%s", name, ex.reason, output)
			out[name] = record{status: "broken", digest: want}
			continue
		}
		if err != nil {
			t.Logf("%s: toolchain output:\n%s", name, output)
			t.Errorf("%s: %v", name, err)
			continue
		}
		out[name] = record{status: "pass", digest: want}
	}
	// A directive naming a target the fixture does not generate asserts
	// nothing, and reads in the archive exactly like one that does.
	for _, spec := range sortedStrings(exempt) {
		if !seen[spec] {
			t.Errorf("line %d: %s names %s, which this fixture does not generate", exempt[spec].line, directiveName(exempt[spec]), spec)
		}
	}
	return out
}

func directiveName(e exemption) string {
	if e.broken {
		return "broken"
	}
	return "unverifiable"
}

// runsTests reports whether the generated output carries tests the toolchain
// will execute rather than merely compile.
func runsTests(files map[string][]byte) bool {
	for name := range files {
		if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, "Test.kt") {
			return true
		}
	}
	return false
}

func sortedStrings[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
