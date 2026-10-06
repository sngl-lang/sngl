package gencache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// probe is a registered producer whose inputs a test chooses and whose runs
// it counts. Each test registers its own, since the registry is global.
type probe struct {
	runs   atomic.Int32
	inputs func(s *Store) ([]Input, error)
}

func newProbe(t *testing.T, inputs func(s *Store) ([]Input, error)) (string, *probe) {
	t.Helper()
	p := &probe{inputs: inputs}
	name := fmt.Sprintf("test.%s.%d", t.Name(), probeSeq.Add(1))
	Register(name, func(s *Store, params []string) (Output, error) {
		n := p.runs.Add(1)
		ins, err := p.inputs(s)
		if err != nil {
			return Output{}, err
		}
		return Output{Inputs: ins, Body: fmt.Appendf(nil, "const run = %d\n", n)}, nil
	})
	return name, p
}

var probeSeq atomic.Int32

// build is one compilation: a fresh store over dir, as a new process would
// open it.
func build(t *testing.T, dir, producer string) string {
	t.Helper()
	data, err := Open(dir).Get(Request{Producer: producer, Params: []string{"q"}})
	if err != nil {
		t.Fatal(err)
	}
	return string(Body(data))
}

func fileInput(path string) func(*Store) ([]Input, error) {
	return func(*Store) ([]Input, error) {
		in, err := File(path)
		return []Input{in}, err
	}
}

// A request whose inputs have not changed is answered from the store, by a
// later build that never ran the producer.
func TestUnchangedInputsSkipTheProducer(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src")
	os.WriteFile(src, []byte("one"), 0o644)
	name, p := newProbe(t, fileInput(src))

	first := build(t, dir, name)
	if second := build(t, dir, name); second != first {
		t.Errorf("second build got %q, want the stored %q", second, first)
	}
	if n := p.runs.Load(); n != 1 {
		t.Errorf("producer ran %d times, want 1", n)
	}
}

// Every kind of input invalidates the file that recorded it when the world
// stops agreeing with it.
func TestChangedInputInvalidates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inputs func(tmp string) func(*Store) ([]Input, error)
		change func(t *testing.T, tmp string)
	}{
		{
			name:   "file",
			inputs: func(tmp string) func(*Store) ([]Input, error) { return fileInput(filepath.Join(tmp, "f")) },
			change: func(t *testing.T, tmp string) { os.WriteFile(filepath.Join(tmp, "f"), []byte("edited"), 0o644) },
		},
		{
			name: "absent",
			inputs: func(tmp string) func(*Store) ([]Input, error) {
				return func(*Store) ([]Input, error) { return []Input{Absent(filepath.Join(tmp, "probe"))}, nil }
			},
			change: func(t *testing.T, tmp string) { os.WriteFile(filepath.Join(tmp, "probe"), nil, 0o644) },
		},
		{
			name: "dir",
			inputs: func(tmp string) func(*Store) ([]Input, error) {
				return func(*Store) ([]Input, error) {
					in, err := Dir(tmp)
					return []Input{in}, err
				}
			},
			change: func(t *testing.T, tmp string) { os.WriteFile(filepath.Join(tmp, "added"), nil, 0o644) },
		},
		{
			name: "godir",
			inputs: func(tmp string) func(*Store) ([]Input, error) {
				return func(*Store) ([]Input, error) {
					in, err := GoDir(tmp)
					return []Input{in}, err
				}
			},
			change: func(t *testing.T, tmp string) { os.WriteFile(filepath.Join(tmp, "added.go"), nil, 0o644) },
		},
		{
			name: "env",
			inputs: func(string) func(*Store) ([]Input, error) {
				return func(*Store) ([]Input, error) { return []Input{Env("SNGL_GENCACHE_TEST_VAR")}, nil }
			},
			change: func(t *testing.T, _ string) { t.Setenv("SNGL_GENCACHE_TEST_VAR", "changed") },
		},
		{
			name: "unsetenv",
			inputs: func(string) func(*Store) ([]Input, error) {
				return func(*Store) ([]Input, error) { return []Input{Env("SNGL_GENCACHE_TEST_UNSET")}, nil }
			},
			change: func(t *testing.T, _ string) { t.Setenv("SNGL_GENCACHE_TEST_UNSET", "") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SNGL_GENCACHE_TEST_VAR", "original")
			os.Unsetenv("SNGL_GENCACHE_TEST_UNSET")
			dir, tmp := t.TempDir(), t.TempDir()
			os.WriteFile(filepath.Join(tmp, "f"), []byte("original"), 0o644)
			name, p := newProbe(t, tc.inputs(tmp))

			first := build(t, dir, name)
			tc.change(t, tmp)
			if again := build(t, dir, name); again == first {
				t.Errorf("after the input changed, the build got the stored %q", again)
			}
			if n := p.runs.Load(); n != 2 {
				t.Errorf("producer ran %d times, want 2", n)
			}
		})
	}
}

// A godir input reads a listing the way the go command does, so a name it
// ignores -- a test's `_scratch` directory beside the package, an editor's
// dotfile -- leaves the stored file valid.
func TestGoDirIgnoresWhatGoIgnores(t *testing.T) {
	dir, tmp := t.TempDir(), t.TempDir()
	name, p := newProbe(t, func(*Store) ([]Input, error) {
		in, err := GoDir(tmp)
		return []Input{in}, err
	})

	first := build(t, dir, name)
	os.Mkdir(filepath.Join(tmp, "_scratch"), 0o755)
	os.WriteFile(filepath.Join(tmp, ".swp"), nil, 0o644)
	if again := build(t, dir, name); again != first {
		t.Errorf("after adding names the go command ignores, the build got %q, want the stored %q", again, first)
	}
	if n := p.runs.Load(); n != 1 {
		t.Errorf("producer ran %d times, want 1", n)
	}
}

// An absent input is a path that resolves to nothing, which is what a probe
// asking os.Stat sees -- so a dangling link recorded as absent stays absent,
// rather than reading as present on every check, and stops being absent when
// its target appears.
func TestAbsentFollowsLinks(t *testing.T) {
	dir, tmp := t.TempDir(), t.TempDir()
	target, link := filepath.Join(tmp, "target"), filepath.Join(tmp, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	name, p := newProbe(t, func(*Store) ([]Input, error) { return []Input{Absent(link)}, nil })

	first := build(t, dir, name)
	if again := build(t, dir, name); again != first || p.runs.Load() != 1 {
		t.Fatalf("a dangling link recorded as absent was stale: producer ran %d times, want 1", p.runs.Load())
	}
	os.WriteFile(target, nil, 0o644)
	build(t, dir, name)
	if n := p.runs.Load(); n != 2 {
		t.Errorf("after the link's target appeared, producer ran %d times, want 2", n)
	}
}

// An entry input follows the output it names: when that output's own inputs
// change, it is produced again, its digest moves, and the entry depending on
// it is stale too -- even though nothing the dependent recorded directly
// changed.
func TestEntryInputFollowsItsDependency(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src")
	os.WriteFile(src, []byte("one"), 0o644)
	lower, lp := newProbe(t, fileInput(src))
	upper, up := newProbe(t, func(s *Store) ([]Input, error) {
		in, _, err := s.Entry(Request{Producer: lower, Params: []string{"dep"}})
		return []Input{in}, err
	})

	build(t, dir, upper)
	build(t, dir, upper)
	if lp.runs.Load() != 1 || up.runs.Load() != 1 {
		t.Fatalf("unchanged: lower ran %d, upper ran %d; want 1 and 1", lp.runs.Load(), up.runs.Load())
	}
	os.WriteFile(src, []byte("two"), 0o644)
	build(t, dir, upper)
	if lp.runs.Load() != 2 || up.runs.Load() != 2 {
		t.Errorf("after the dependency's input changed: lower ran %d, upper ran %d; want 2 and 2", lp.runs.Load(), up.runs.Load())
	}
}

// A store that is off keeps nothing, so every build runs the producer.
func TestOffStoreRunsEveryTime(t *testing.T) {
	name, p := newProbe(t, func(*Store) ([]Input, error) { return nil, nil })
	build(t, "", name)
	build(t, "", name)
	if n := p.runs.Load(); n != 2 {
		t.Errorf("producer ran %d times, want 2", n)
	}
}

// An input this compiler does not know is one it cannot check, so the file is
// produced again rather than trusted.
func TestUnknownInputIsStale(t *testing.T) {
	dir := t.TempDir()
	name, p := newProbe(t, func(*Store) ([]Input, error) {
		return []Input{{Kind: "fromTheFuture", Props: []Prop{str("x", "y")}}}, nil
	})
	build(t, dir, name)
	build(t, dir, name)
	if n := p.runs.Load(); n != 2 {
		t.Errorf("producer ran %d times, want 2", n)
	}
}

// The key carries the compiler: a different compiler asking the same request
// finds nothing stored for it.
func TestCompilerIsPartOfTheKey(t *testing.T) {
	s := Open(t.TempDir())
	req := Request{Producer: "p", Params: []string{"a"}}
	before := s.key(req)
	s.id = "another compiler"
	if s.key(req) == before {
		t.Error("two compilers share a key")
	}
}

// Params are length-prefixed into the key, so two lists that concatenate to
// the same string are two requests.
func TestParamsDoNotRunTogether(t *testing.T) {
	s := Open("")
	a := s.key(Request{Producer: "p", Params: []string{"ab", "c"}})
	b := s.key(Request{Producer: "p", Params: []string{"a", "bc"}})
	if a == b {
		t.Error("[ab c] and [a bc] share a key")
	}
}

// What Render writes, ReadInputs reads back, including values that need
// escaping.
func TestRenderReadRoundTrip(t *testing.T) {
	want := []Input{
		{Kind: "file", Props: []Prop{str("path", `/a "quoted" \path`), str("sha256", "00ff")}},
		{Kind: "env", Props: []Prop{str("name", "X"), str("value", "line\nbreak\ttab")}},
		{Kind: "entry", Props: []Prop{str("producer", "go.deps"), list("params", []string{"/d", "x/y"}), str("sha256", "ab")}},
	}
	data := Render(Request{Producer: "p", Params: []string{"q"}}, Output{Inputs: want, Body: []byte("const x = 1\n")})
	got, err := ReadInputs(data)
	if err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("round trip:\ngot  %v\nwant %v\n%s", got, want, data)
	}
	if b := string(Body(data)); b != "const x = 1\n" {
		t.Errorf("Body = %q", b)
	}
}

// A stored file that cannot be read is produced again, not reported: the
// store is a cache, and a corrupt entry is a slower build.
func TestCorruptEntryIsProducedAgain(t *testing.T) {
	dir := t.TempDir()
	name, p := newProbe(t, func(*Store) ([]Input, error) { return nil, nil })
	build(t, dir, name)
	s := Open(dir)
	path := s.path(s.key(Request{Producer: name, Params: []string{"q"}}))
	if err := os.WriteFile(path, []byte("not sngl {{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := build(t, dir, name); !strings.Contains(got, "const run = 2") {
		t.Errorf("got %q", got)
	}
	if n := p.runs.Load(); n != 2 {
		t.Errorf("producer ran %d times, want 2", n)
	}
}

// Used reports every answer Get handed out -- one a producer asked for through
// an entry input included -- as the entry input that records it, and what it
// reports parses back.
func TestUsedReportsWhatGetHandedOut(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	os.WriteFile(src, []byte("x"), 0o644)
	lower, _ := newProbe(t, fileInput(src))
	upper, _ := newProbe(t, func(s *Store) ([]Input, error) {
		in, _, err := s.Entry(Request{Producer: lower, Params: []string{"q"}})
		return []Input{in}, err
	})
	s := Open(t.TempDir())
	if _, err := s.Get(Request{Producer: upper, Params: []string{"q"}}); err != nil {
		t.Fatal(err)
	}
	used := s.Used()
	var lines []string
	for _, in := range used {
		lines = append(lines, in.String())
	}
	joined := strings.Join(lines, "\n")
	if len(used) != 2 || !strings.Contains(joined, lower) || !strings.Contains(joined, upper) {
		t.Fatalf("Used() = %s, want both answers", joined)
	}
	back, err := ParseInputs(lines)
	if err != nil || fmt.Sprint(back) != fmt.Sprint(used) {
		t.Errorf("ParseInputs(Used()) = %v, %v; want %v", back, err, used)
	}
}

// TestEnvRecordsNoValue holds Env to recording a variable by its digest: the
// store is a directory on disk that outlives the build, and a variable may
// hold a token.
func TestEnvRecordsNoValue(t *testing.T) {
	t.Setenv("SNGL_GENCACHE_TEST_SECRET", "hunter2")
	in := Env("SNGL_GENCACHE_TEST_SECRET")
	for _, p := range in.Props {
		if p.Value == "hunter2" {
			t.Fatalf("Env recorded the value itself: %+v", in)
		}
	}
	if in.Get("sha256") == "" {
		t.Fatalf("Env recorded no digest: %+v", in)
	}
}

// A setting invalidates what recorded it when it changes, and a store that
// outlives the change -- a batch producer looking one request up twice, as
// the plugin runner does -- asks it again rather than remembering the first
// answer.
func TestSettingInvalidatesWithinOneStore(t *testing.T) {
	val := "a"
	name := "test.setting." + t.Name()
	RegisterSetting(name, func() string { return val })
	in, err := Setting(name)
	if err != nil {
		t.Fatal(err)
	}
	s := Open(t.TempDir())
	req := Request{Producer: "test.setting", Params: []string{"q"}}
	s.Put(req, Output{Inputs: []Input{in}, Body: []byte("const v = 1\n")})
	if _, ok := s.Lookup(req); !ok {
		t.Fatal("lookup missed with the setting unchanged")
	}
	val = "b"
	if _, ok := s.Lookup(req); ok {
		t.Error("lookup hit after the setting changed")
	}
	if _, err := Setting("test.setting.nobody"); err == nil {
		t.Error("Setting of an unregistered name succeeded")
	}
}
