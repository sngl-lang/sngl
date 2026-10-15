package testlaunch

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

	"golang.org/x/tools/txtar"

	"duckfam.us/sngl/codegen"
)

// A Record is what one launch of a generated test program produced: its
// results or its error, and the snapshots it wrote. Digest names the inputs --
// the generated program and the snapshots it was compared against.
type Record struct {
	Digest    string                `json:"-"`
	Results   []*codegen.TestResult `json:"results,omitempty"`
	Error     string                `json:"error,omitempty"`
	Snapshots map[string][]byte     `json:"snapshots,omitempty"`
}

type Records struct {
	mu      sync.Mutex
	update  bool
	verify  bool
	have    map[string]Record
	written map[string]Record
	seen    map[string]int
}

// NewRecords answers launches from have. update runs a launch with no record,
// or whose inputs moved, and records it; verify runs every launch, and fails
// one that disagrees with its record unless update rewrites it.
func NewRecords(have map[string]Record, update, verify bool) *Records {
	return &Records{update: update, verify: verify, have: have, written: map[string]Record{}, seen: map[string]int{}}
}

func parseRecord(name string, data []byte) (Record, error) {
	head, body, _ := bytes.Cut(data, []byte("\n"))
	rec := Record{Digest: strings.TrimSpace(string(head))}
	if !strings.HasPrefix(rec.Digest, "sha256:") {
		return rec, fmt.Errorf("%s: first line is not a digest", name)
	}
	if err := json.Unmarshal(body, &rec); err != nil {
		return rec, fmt.Errorf("%s: %w", name, err)
	}
	return rec, nil
}

func (r Record) format() []byte {
	body, _ := json.MarshalIndent(r, "", "  ")
	return fmt.Appendf(nil, "%s\n%s\n", r.Digest, body)
}

// Written returns every record reached this run, keyed by archive name.
func (rr *Records) Written() map[string]Record {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return maps.Clone(rr.written)
}

// answer replays the record for this launch when its digest matches, and
// otherwise runs launch -- only under update, since a run that is not
// recorded is one CI has no answer for. A toolchain moves no digest, which is
// what verify is for.
func (rr *Records) answer(key, genDir, snapDir string, launch func() ([]*codegen.TestResult, error)) ([]*codegen.TestResult, error) {
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

	matched := recorded && have.Digest == want
	if matched && !rr.verify {
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
	if !rr.update && !matched {
		if recorded {
			rr.keep(name, have)
			return nil, fmt.Errorf("%s: the generated test program changed since it was run; rerun its test with -update", name)
		}
		return nil, fmt.Errorf("%s: no record of this test run; rerun its test with -update on a host with the toolchain", name)
	}

	results, err := launch()
	if skip, ok := errors.AsType[*codegen.SkipError](err); ok {
		return nil, fmt.Errorf("%s: cannot record — %s", name, skip.Reason)
	}
	rec := Record{Digest: want}
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
	if !rr.update {
		rr.keep(name, have)
		if !bytes.Equal(rec.format(), have.format()) {
			return nil, fmt.Errorf("%s: a rerun disagrees with the record:\n%s\nrerun its test with -update", name, rec.format())
		}
		return results, err
	}
	rr.keep(name, rec)
	return results, err
}

func (rr *Records) keep(name string, rec Record) {
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

// RecordPrefix is the archive directory holding a fixture's run records.
const RecordPrefix = "testrun/"

// SplitArchive separates an archive's run records from its other files.
func SplitArchive(a *txtar.Archive) (map[string]Record, []txtar.File, error) {
	records := map[string]Record{}
	var rest []txtar.File
	for _, f := range a.Files {
		if !strings.HasPrefix(f.Name, RecordPrefix) {
			rest = append(rest, f)
			continue
		}
		rec, err := parseRecord(f.Name, f.Data)
		if err != nil {
			return nil, nil, err
		}
		records[f.Name] = rec
	}
	return records, rest, nil
}

// WriteArchive writes file's other files and then written, unless that is what
// a already holds. Records reached this run replace every record the archive
// held, so one no longer launched goes with it. The other files are read
// again first: the golden harness rewrites them in a component fixture, perhaps
// since a was read.
func WriteArchive(file string, a *txtar.Archive, rest []txtar.File, written map[string]Record) error {
	if now, err := txtar.ParseFile(file); err == nil {
		if _, fresh, err := SplitArchive(now); err == nil {
			rest = fresh
		}
	}
	out := &txtar.Archive{Comment: a.Comment, Files: slices.Clone(rest)}
	for _, name := range slices.Sorted(maps.Keys(written)) {
		out.Files = append(out.Files, txtar.File{Name: name, Data: written[name].format()})
	}
	data := txtar.Format(out)
	if bytes.Equal(data, txtar.Format(a)) {
		return nil
	}
	return ReplaceFile(file, data)
}

// ReplaceFile writes data to path through a file beside it, so a reader never
// sees it half written.
func ReplaceFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
