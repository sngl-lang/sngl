// Package gencache stores the SNGL a producer generates, between builds.
//
// A producer is a step whose output the compiler reads but did not write: it
// runs another process or reads the host, and what it hands back is SNGL
// source -- declarations derived from a GTK install's introspection data, the
// value a `const func` Go function returned. Doing that on every build
// is most of what an unchanged build would otherwise spend.
//
// The split this package keeps is the one `sngl:x/gen/cache` describes. A
// generated file says what it was generated from, in a `cache.inputs`
// directive at its root; the store only decides where a previous answer is
// kept. Asked for a request, the store finds the file it last wrote for it,
// re-checks every recorded input against the world, and hands the file back
// when all of them still hold. Otherwise it runs the producer and keeps what
// it writes.
//
// The key is the request -- which producer, asked what -- plus the identity of
// the compiler asking and of the producer's own code where the compiler's does
// not cover it, since a producer's code is part of what its output depends on
// and nothing in the file could record that. The inputs are what validates the
// entry the key found.
package gencache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Request identifies one question put to a producer. Params are the whole of
// what the producer is asked; two requests with the same producer, params and
// identity are the same question.
type Request struct {
	Producer string
	Params   []string
	// Identity is the producer's own code, where the compiler's identity does
	// not already cover it: a producer written in SNGL is a package the
	// program imported, and editing it changes every answer while every input
	// it recorded still holds. Empty for a producer compiled into the
	// compiler, which compilerID already names.
	Identity string
}

func (r Request) String() string {
	return r.Producer + "(" + strings.Join(r.Params, ", ") + ")"
}

// Output is what a producer hands back: the inputs it read, recorded before
// it read them, and the SNGL it generated from them.
//
// Body is SNGL source and may begin with imports of its own. The store writes
// the file around it -- a header, the import of `sngl:x/gen/cache` and the
// directive -- so a producer never spells the vocabulary itself.
type Output struct {
	Inputs []Input
	Body   []byte
	// NoStore answers this request without keeping the answer: the producer
	// read something too new to trust its recorded value of, and the next
	// build asks again.
	NoStore bool
}

// A Producer answers a request. It is registered by name so that the store
// can answer an `entry` input, which names another producer's output, without
// the caller that recorded it being the one that checks it.
type Producer func(s *Store, params []string) (Output, error)

var (
	producersMu sync.RWMutex
	producers   = map[string]Producer{}
)

// Register makes a producer available under name. It is called from init, by
// the package that owns the step.
func Register(name string, p Producer) {
	producersMu.Lock()
	defer producersMu.Unlock()
	if _, dup := producers[name]; dup {
		panic("gencache: producer " + name + " registered twice")
	}
	producers[name] = p
}

func producer(name string) (Producer, bool) {
	producersMu.RLock()
	defer producersMu.RUnlock()
	p, ok := producers[name]
	return p, ok
}

// Env names the variables that configure the default store.
const (
	// DirEnv relocates the store. It exists for the reason the evaluator's
	// binary cache had one: pointing XDG_CACHE_HOME elsewhere also moves
	// GOCACHE, and a producer that runs the go command then rebuilds all of
	// it there.
	DirEnv = "SNGL_GENCACHE_DIR"
	// OffEnv, set to "off", makes every request run its producer and keeps
	// nothing: the switch for debugging a producer, or a pure function that
	// is not.
	OffEnv = "SNGL_GENCACHE"
)

// Store is a directory of generated files, one per request.
type Store struct {
	dir string // "" when the store is off
	id  string // the compiler's identity; see compilerID and ready

	readyOnce sync.Once

	mu     sync.Mutex
	got    map[string]*entryResult // per request key, this process's answer
	checks map[string]bool         // per input, this process's verdict
	goenv  map[string]goEnvResult  // per directory
	used   []Input                 // every answer Get handed out, in order
	pruned bool
}

type entryResult struct {
	once sync.Once
	data []byte
	err  error
}

var (
	defaultMu     sync.Mutex
	defaultStores = map[string]*Store{}
)

// Default is the store every producer in this process shares: under the
// user's cache directory, or DirEnv, or off when OffEnv says so.
//
// One per directory the environment names when it is asked, rather than one
// for the process: a process that runs several builds in turn -- a test
// harness running scripts in process -- has each build's environment decide,
// as a fresh process would. A store a build turned off is one it asked not
// to be answered from.
func Default() *Store {
	return defaultStore()
}

// ResetDefault forgets every store Default handed out, for a process that runs
// several builds in turn: a store memoizes what it found each input to be for
// its life, which is one build's, and the files may change between two.
func ResetDefault() {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	clear(defaultStores)
}

func defaultStore() *Store {
	dir := ""
	switch {
	case os.Getenv(OffEnv) == "off":
	case os.Getenv(DirEnv) != "":
		dir = os.Getenv(DirEnv)
	default:
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		dir = filepath.Join(base, "sngl", "gen")
	}
	defaultMu.Lock()
	defer defaultMu.Unlock()
	s, ok := defaultStores[dir]
	if !ok {
		s = Open(dir)
		defaultStores[dir] = s
	}
	return s
}

// Open returns a store kept in dir. An empty dir is a store that keeps
// nothing: every request runs its producer.
func Open(dir string) *Store {
	s := &Store{
		dir:    dir,
		got:    map[string]*entryResult{},
		checks: map[string]bool{},
		goenv:  map[string]goEnvResult{},
	}
	return s
}

// ready identifies the compiler, the first time a request needs a key or a
// path rather than when the store is opened: hashing the executable is the
// most expensive thing a store does, and a process that opens one and asks
// nothing -- an evaluator that reads no generated file -- should not pay it.
// A compiler that cannot be identified turns the store off.
func (s *Store) ready() {
	s.readyOnce.Do(func() {
		if s.dir == "" {
			return
		}
		if s.id = compilerID(filepath.Join(s.dir, "compiler")); s.id == "" {
			s.dir = ""
		}
	})
}

// Get returns the generated file for req: the stored one when every input it
// records still holds, and otherwise the one the producer writes now, which
// replaces it.
//
// The answer is memoized for the life of the store, so a request asked twice
// in one build -- or reached twice as another entry's input -- is checked
// once. A failed produce is not kept on disk: a producer failing says nothing
// durable about the request.
func (s *Store) Get(req Request) ([]byte, error) {
	key := s.key(req)
	s.mu.Lock()
	r, ok := s.got[key]
	if !ok {
		r = &entryResult{}
		s.got[key] = r
	}
	s.mu.Unlock()
	r.once.Do(func() {
		r.data, r.err = s.get(req, key)
		if r.err == nil {
			s.mu.Lock()
			s.used = append(s.used, Input{Kind: "entry", Props: []Prop{
				str("producer", req.Producer), list("params", req.Params), str("sha256", Digest(r.data)),
			}})
			s.mu.Unlock()
		}
	})
	return r.data, r.err
}

// Used is every generated file this store has handed out through Get, as the
// entry input something derived from them records. A process that folds
// generated source into what it computes -- the compile-time evaluator, whose
// functions may read a target's package -- reports it, so that what it
// computed is invalidated when that source is regenerated.
func (s *Store) Used() []Input {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.used)
}

func (s *Store) get(req Request, key string) ([]byte, error) {
	p, ok := producer(req.Producer)
	if !ok {
		return nil, fmt.Errorf("gencache: no producer %q", req.Producer)
	}
	if data, ok := s.lookup(req, key); ok {
		return data, nil
	}
	start := time.Now()
	out, err := p(s, req.Params)
	if err != nil {
		return nil, err
	}
	slog.Debug("gencache produce", "req", req.String(), "duration", time.Since(start))
	return s.put(req, key, out), nil
}

// Lookup returns the stored file for req when every input it records still
// holds.
//
// It is Get for a caller that answers many requests with one run -- the
// compile-time evaluator builds one program for every call it has no value
// for -- and so looks each up, produces the misses together, and Puts each
// answer. Unlike Get it is not memoized: such a caller asks once.
func (s *Store) Lookup(req Request) ([]byte, bool) {
	return s.lookup(req, s.key(req))
}

// Put stores out as req's answer and returns the file it wrote.
func (s *Store) Put(req Request, out Output) []byte {
	return s.put(req, s.key(req), out)
}

func (s *Store) lookup(req Request, key string) ([]byte, bool) {
	path := s.path(key)
	if path == "" {
		return nil, false
	}
	start := time.Now()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	why, err := s.Stale(data)
	if err != nil {
		why = err.Error()
	}
	if why != "" {
		slog.Debug("gencache stale", "req", req.String(), "why", why)
		return nil, false
	}
	now := time.Now()
	os.Chtimes(path, now, now) // eviction is by last use
	slog.Debug("gencache hit", "req", req.String(), "duration", time.Since(start))
	return data, true
}

func (s *Store) put(req Request, key string, out Output) []byte {
	data := Render(req, out)
	if path := s.path(key); path != "" && !out.NoStore {
		s.write(path, data)
	}
	return data
}

// key is the file name a request is stored under: the request, the compiler
// that asked it and the producer's identity. Params are length-prefixed, so no
// two lists of them run together into one key.
func (s *Store) key(req Request) string {
	s.ready()
	h := sha256.New()
	fmt.Fprintf(h, "gencache v2\x00%s\x00%s\x00%s\x00%d\x00", s.id, req.Identity, req.Producer, len(req.Params))
	for _, p := range req.Params {
		fmt.Fprintf(h, "%d:%s\x00", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func (s *Store) path(key string) string {
	s.ready()
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, key[:2], key+".sngl")
}

// write stores data at path, atomically: a reader in another process sees
// the old file or the new one and never half of one. Failing to store is not
// an error -- the build has its answer, and a cache that cannot be written is
// a slower build rather than a broken one.
func (s *Store) write(path string, data []byte) {
	s.pruneOnce()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Debug("gencache write", "err", err)
		return
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		slog.Debug("gencache write", "err", err)
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		slog.Debug("gencache write", "err", errors.Join(werr, cerr))
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		slog.Debug("gencache write", "err", err)
	}
}

// Digest is how an `entry` input records another output: the SHA-256 of the
// file the store holds for it.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

const (
	// maxBytes bounds the store. What grows it is requests nobody asks again
	// -- an edited argument is a new request -- so without a budget a day of
	// work leaves every superseded answer behind.
	maxBytes = 512 << 20
	// maxAge is the floor under the budget: an entry unused this long goes
	// whether the store is over budget or not. A compiler memo goes at the
	// same age, since every build that uses one marks it used.
	maxAge = 30 * 24 * time.Hour
	// tmpGrace is how old a temporary file may be before it is taken for one
	// a crashed write left behind. A write takes milliseconds; this is far
	// past that, so a live one is never removed from under its writer.
	tmpGrace = time.Hour
)

// pruneOnce enforces maxAge and then maxBytes, least recently used first,
// once per process and only when something is about to be written: a build
// that hits everything leaves the store as it found it. It removes the side
// files too -- compiler memos unused for maxAge, and temporaries older than
// tmpGrace -- which nothing else ever would. Errors are ignored throughout,
// for the reason write gives.
func (s *Store) pruneOnce() {
	s.mu.Lock()
	if s.pruned {
		s.mu.Unlock()
		return
	}
	s.pruned = true
	s.mu.Unlock()
	prune(s.dir, maxBytes, maxAge)
}

func prune(dir string, budget int64, age time.Duration) {
	type file struct {
		path string
		used time.Time
		size int64
	}
	var files []file
	var total int64
	memoDir := filepath.Join(dir, "compiler")
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		since := time.Since(info.ModTime())
		switch {
		case isTemp(d.Name()):
			if since > tmpGrace {
				os.Remove(path)
			}
			return nil
		case filepath.Dir(path) == memoDir:
			if since > age {
				os.Remove(path)
			}
			return nil
		case !strings.HasSuffix(path, ".sngl"):
			return nil
		}
		if since > age {
			os.Remove(path)
			return nil
		}
		files = append(files, file{path, info.ModTime(), info.Size()})
		total += info.Size()
		return nil
	})
	if total <= budget {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].used.Before(files[j].used) })
	for _, f := range files {
		if total <= budget {
			return
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}

// isTemp reports whether name is a temporary file one of the store's writes
// made: an entry's `.tmp-*` beside it, or a memo's `<memo>.tmp<pid>`.
func isTemp(name string) bool {
	return strings.Contains(name, ".tmp")
}

// Render writes the file a store keeps for req: a header naming the request,
// the directive recording out.Inputs, and out.Body.
func Render(req Request, out Output) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by sngl for %s. DO NOT EDIT.\n\n", req)
	fmt.Fprintf(&b, "import %s %q\n\n", vocabAlias, VocabPath)
	fmt.Fprintf(&b, "%s.inputs {\n", vocabAlias)
	for _, in := range out.Inputs {
		b.WriteString("    ")
		in.render(&b, vocabAlias)
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	if len(out.Body) > 0 {
		b.WriteByte('\n')
		b.Write(out.Body)
		if !bytes.HasSuffix(out.Body, []byte("\n")) {
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

// VocabPath is the package the directive's vocabulary is declared in, and
// vocabAlias the name a rendered file imports it under.
const (
	VocabPath  = "sngl:x/gen/cache"
	vocabAlias = "cache"
)

// Body returns the part of a rendered file a producer wrote, for a caller
// that wants the generated SNGL without the directive in front of it.
func Body(data []byte) []byte {
	const end = "\n}\n"
	i := bytes.Index(data, []byte(vocabAlias+".inputs {\n"))
	if i < 0 {
		return data
	}
	j := bytes.Index(data[i:], []byte(end))
	if j < 0 {
		return data
	}
	return bytes.TrimLeft(data[i+j+len(end):], "\n")
}
