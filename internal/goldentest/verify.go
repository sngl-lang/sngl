package goldentest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/toolchain"
	"errors"
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

// verify holds each target to its record.
//
// The ordinary run does not compile. It compares the digest of what was
// generated against the digest in the record, and that is the whole of it --
// no toolchain is consulted, nothing is probed, and the answer does not depend
// on what the host has installed. A plain Go container can run it.
//
// Compiling happens under -update and -verify, and there a missing tool is a
// failure rather than a skip. A golden may not be regenerated without running
// the tooling: allowed to skip, -update would rewrite the generated code and
// leave the record describing bytes nobody built, which is the one state the
// record exists to make impossible.
//
//   - the digest matches and the status agrees — nothing to do.
//   - it does not, and update or force is set — compile, and write what the
//     toolchain said.
//   - it does not, and neither is set — a failure naming -update, the same
//     answer a golden mismatch gives, for the same reason.
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
			// so a record would be a claim about a machine that does not
			// exist. The directive is the claim instead, and it names why.
			t.Logf("%s: unverifiable — %s", name, ex.reason)
			continue
		}

		// What the record must say for this target, so a directive added or
		// removed without the output moving is still caught below.
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
				t.Errorf("%s: no record; regenerate with -update on a host that has the toolchain", name)
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

		// From here the toolchain must answer. Anything that stops it is a
		// failure: the record about to be written would otherwise say a build
		// happened that did not.
		if reason := toolchain.Unavailable(tg.lang, tg.platform); reason != "" {
			t.Errorf("%s: cannot verify — %s", name, reason)
			if recorded {
				out[name] = have
			}
			continue
		}
		if reason := toolchain.PresentsWindows(tg.platform); reason != "" {
			t.Errorf("%s: cannot verify — %s", name, reason)
			if recorded {
				out[name] = have
			}
			continue
		}

		output, err := toolchain.Build(t.Context(), tg.files, tg.lang, tg.platform)
		if se, ok := errors.AsType[*toolchain.SkipError](err); ok {
			// A gap the probe could not see until the build revealed it --
			// still a gap, still not something to record around.
			t.Errorf("%s: cannot verify — %s", name, se.Reason)
			if recorded {
				out[name] = have
			}
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

func sortedStrings[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
