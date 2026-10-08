package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"duckfam.us/sngl/codegen"
)

// testRuns, when set, answers a test launch from a record of an earlier one
// with the same inputs. Only the script harness sets it.
var testRuns *runRecords

// A runRecord is what one launch of a generated test program produced: its
// results or its error, and the snapshots it wrote. Digest names the inputs --
// the generated program and the snapshots it was compared against.
type runRecord struct {
	Digest    string                `json:"-"`
	Results   []*codegen.TestResult `json:"results,omitempty"`
	Error     string                `json:"error,omitempty"`
	Snapshots map[string][]byte     `json:"snapshots,omitempty"`
}

type runRecords struct {
	mu      sync.Mutex
	update  bool
	have    map[string]runRecord
	written map[string]runRecord
	seen    map[string]int
}

func newRunRecords(have map[string]runRecord, update bool) *runRecords {
	return &runRecords{update: update, have: have, written: map[string]runRecord{}, seen: map[string]int{}}
}

func parseRunRecord(name string, data []byte) (runRecord, error) {
	head, body, _ := bytes.Cut(data, []byte("\n"))
	rec := runRecord{Digest: strings.TrimSpace(string(head))}
	if !strings.HasPrefix(rec.Digest, "sha256:") {
		return rec, fmt.Errorf("%s: first line is not a digest", name)
	}
	if err := json.Unmarshal(body, &rec); err != nil {
		return rec, fmt.Errorf("%s: %w", name, err)
	}
	return rec, nil
}

func (r runRecord) format() []byte {
	body, _ := json.MarshalIndent(r, "", "  ")
	return fmt.Appendf(nil, "%s\n%s\n", r.Digest, body)
}

// Written returns every record reached this run, keyed by archive name.
func (rr *runRecords) Written() map[string]runRecord {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return maps.Clone(rr.written)
}

// answer replays the record for this launch when its digest matches, and
// otherwise runs launch -- only under update, since a run that is not
// recorded is one CI has no answer for.
func (rr *runRecords) answer(key, genDir, snapDir string, launch func() ([]*codegen.TestResult, error)) ([]*codegen.TestResult, error) {
	rr.mu.Lock()
	rr.seen[key]++
	name := fmt.Sprintf("testrun/%s/%d", key, rr.seen[key])
	have, recorded := rr.have[name]
	rr.mu.Unlock()

	before, err := readTree(snapDir)
	if err != nil {
		return nil, err
	}
	gen, err := readTree(genDir)
	if err != nil {
		return nil, err
	}
	updating := os.Getenv("SNGL_UPDATE_SNAPSHOTS") == "1"
	want := runDigest(gen, before, updating)

	if recorded && have.Digest == want && !rr.update {
		rr.keep(name, have)
		for rel, data := range have.Snapshots {
			p := filepath.Join(snapDir, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(p, data, 0o644); err != nil {
				return nil, err
			}
		}
		if have.Error != "" {
			return nil, errors.New(have.Error)
		}
		return cloneResults(have.Results), nil
	}
	if !rr.update {
		if recorded {
			rr.keep(name, have)
			return nil, fmt.Errorf("%s: the generated test program changed since it was run; rerun the script with -update", name)
		}
		return nil, fmt.Errorf("%s: no record of this test run; rerun the script with -update on a host with the toolchain", name)
	}

	results, err := launch()
	if skip, ok := errors.AsType[*codegen.SkipError](err); ok {
		return nil, fmt.Errorf("%s: cannot record — %s", name, skip.Reason)
	}
	rec := runRecord{Digest: want}
	if err != nil {
		rec.Error = err.Error()
	}
	rec.Results = cloneResults(results)
	for _, r := range rec.Results {
		zeroDurations(r)
	}
	after, rerr := readTree(snapDir)
	if rerr != nil {
		return nil, rerr
	}
	for rel, data := range after {
		if !bytes.Equal(before[rel], data) {
			if rec.Snapshots == nil {
				rec.Snapshots = map[string][]byte{}
			}
			rec.Snapshots[rel] = data
		}
	}
	rr.keep(name, rec)
	return results, err
}

func (rr *runRecords) keep(name string, rec runRecord) {
	rr.mu.Lock()
	rr.written[name] = rec
	rr.mu.Unlock()
}

// Durations are the one field two runs of one program never agree on.
func zeroDurations(r *codegen.TestResult) {
	r.Duration = 0
	for _, c := range r.Children {
		zeroDurations(c)
	}
}

func runDigest(gen, snapshots map[string][]byte, updating bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "update-snapshots=%v\x00", updating)
	for _, tree := range []struct {
		prefix string
		files  map[string][]byte
	}{{"gen/", gen}, {"snapshots/", snapshots}} {
		for _, name := range slices.Sorted(maps.Keys(tree.files)) {
			fmt.Fprintf(h, "%s%s\x00%d\x00", tree.prefix, name, len(tree.files[name]))
			h.Write(tree.files[name])
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)[:16])
}

func readTree(root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == root {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	return out, err
}

// Callers prefix a result's Component in place, which must reach neither the
// record nor the next replay of it.
func cloneResults(rs []*codegen.TestResult) []*codegen.TestResult {
	if rs == nil {
		return nil
	}
	data, _ := json.Marshal(rs)
	var out []*codegen.TestResult
	_ = json.Unmarshal(data, &out)
	return out
}
