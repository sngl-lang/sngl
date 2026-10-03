// Package trust decides whether code a project brought may reach the host.
//
// The threat is a malicious repository: cloning one and running any `sngl`
// command in it must not run a process, read the environment or read outside
// the project on the repository's say-so. Two kinds of code ask. A plugin --
// SNGL folded at build time -- reaches the host only through sngl:x/gen, one
// call at a time, so a grant names the call: a command prefix, a variable, a
// file, a directory. An evaluated Go or JavaScript package is native code with
// the user's privileges, so a grant names the package and is total.
//
// A grant comes from three places, and they are a union: a flag, for one
// invocation; SNGL_ALLOW, for every invocation that inherits it; and the
// user's config file, os.UserConfigDir()/sngl/trust.sngl, which is never read
// from the project. A flag may name code by its import path, since whoever
// typed it is in the project it names. SNGL_ALLOW and the config file name it
// by its origin -- the directory it is on disk, the module version that
// pinned it -- because an import path is a name the repository chooses, and an
// ambient grant by name reaches any repository that claims the name.
package trust

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Kind is what a grant allows.
type Kind string

const (
	// Eval runs an evaluated package's code: build it and call its pure
	// functions.
	Eval Kind = "eval"
	// Command runs a process whose argv begins with the grant's prefix.
	Command Kind = "command"
	// Env reads an environment variable.
	Env Kind = "env"
	// File reads one file outside the import root.
	File Kind = "file"
	// Dir reads a directory, and everything below it, outside the import root.
	Dir Kind = "dir"
	// Net contacts a host over the network: a git: or http: import fetching
	// what is not yet in the cache. A grant names the host exactly, or with a
	// leading `*.` its subdomains and not itself.
	Net Kind = "net"
)

// Origin is where code came from: what a recorded grant is bound to.
type Origin struct {
	// Spec is the origin as a grant spells it: `dir:<absolute path>` for code
	// on disk, `go:<module>@<version>` for a dependency go.sum pins, or the
	// URI a fetched package was imported by.
	Spec string
	// Module is the go.mod module path a Go package on disk belongs to. A
	// recorded grant for one holds it too, so a directory reused for another
	// project is asked about again.
	Module string
	// Digest is the content a fetched package was granted for, so a changed
	// one is asked about again. Code on disk in the project carries none: an
	// edit the user makes is not a reason to ask them.
	Digest string
}

// DirOrigin is the origin of code in dir, symlinks resolved so two spellings
// of one directory are one origin.
func DirOrigin(dir string) Origin {
	return Origin{Spec: "dir:" + Canonical(dir)}
}

// Canonical is path made absolute with its symlinks resolved, as far as they
// resolve: a path that does not exist keeps the part that does not.
func Canonical(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	dir, base := filepath.Split(abs)
	if dir == abs || dir == "" {
		return abs
	}
	return filepath.Join(Canonical(filepath.Clean(dir)), base)
}

// Within reports whether p lies at or below dir. Both are canonical.
func Within(p, dir string) bool {
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// Subject is the code asking.
type Subject struct {
	// Name is how a flag and a message spell it: a plugin's import path from
	// the root (`./pc`), or an evaluated package's (`go:example.com/docs`).
	Name string
	// Origin is where it came from. For an evaluated package it may be unknown
	// until a recorded grant needs comparing, which is what Resolve answers.
	Origin  Origin
	Resolve func() (Origin, error)
	// Library is code shipped in the sngl: tree, compiled into the binary the
	// user chose to run: trusted, as a Go target's code is.
	Library bool
}

func (s *Subject) origin() (Origin, error) {
	if s.Origin.Spec == "" && s.Resolve != nil {
		o, err := s.Resolve()
		if err != nil {
			return Origin{}, err
		}
		s.Origin = o
		s.Resolve = nil
	}
	return s.Origin, nil
}

// Request is one thing a subject asks to do.
type Request struct {
	Kind    Kind
	Subject Subject
	// Value is the variable for Env and the canonical path for File and Dir.
	Value string
	// Cmd is the argv a Command runs, Prefix what the call declared it begins
	// with and BanFlags the flags it declared it never passes.
	Cmd, Prefix, BanFlags []string
	// Root is the import root, which a read outside of is what is asked.
	Root string
}

// Grant is one allowance.
type Grant struct {
	Kind Kind
	// Subject names the code: an origin spec, or -- from a flag only -- an
	// import path.
	Subject string
	Module  string
	Digest  string
	// Value is the variable, file or directory; Prefix the command prefix.
	Value  string
	Prefix []string
	// BanFlags, on a recorded command grant, is what the call banned when it
	// was granted: a plugin that stops banning one is asked again.
	BanFlags []string
	// Source says where the grant came from, for the LSP's log and `sngl trust
	// --list`.
	Source string
}

// IsOrigin reports whether a grant's subject is an origin rather than an
// import path.
func IsOrigin(subject string) bool {
	return strings.HasPrefix(subject, "dir:") || strings.Contains(subject, "@")
}

// Prompter asks the user about a refused request, and is nil where nobody
// can be asked.
type Prompter interface {
	Ask(r Request, origin Origin) Answer
}

// Answer is what the user said at a prompt.
type Answer int

const (
	No Answer = iota
	Once
	Always
)

// Policy is every grant one invocation holds.
type Policy struct {
	mu     sync.Mutex
	all    bool
	grants []Grant
	// Prompt is consulted when nothing grants a request. Nil refuses.
	Prompt Prompter
	// ConfigPath is the config file an Always answer is recorded in.
	ConfigPath string
}

// AllowAll is a policy that grants everything: --allow-all, and the test
// harnesses that build their own fixtures.
func AllowAll() *Policy { return &Policy{all: true} }

// Add appends grants.
func (p *Policy) Add(gs ...Grant) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.grants = append(p.grants, gs...)
}

// SetAll makes the policy grant everything.
func (p *Policy) SetAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.all = true
}

// Grants returns every grant the policy holds, in the order added.
func (p *Policy) Grants() []Grant {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.grants)
}

// Check answers r: nil when it is allowed, and otherwise a *Refusal. A nil
// policy refuses everything a library does not ask.
func (p *Policy) Check(r Request) error {
	if r.Subject.Library {
		return nil
	}
	if p == nil {
		return &Refusal{Request: r}
	}
	p.mu.Lock()
	all, grants, prompt := p.all, slices.Clone(p.grants), p.Prompt
	p.mu.Unlock()
	if all {
		return nil
	}
	for _, g := range grants {
		ok, err := g.covers(&r)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	if prompt == nil {
		return &Refusal{Request: r}
	}
	origin, err := r.Subject.origin()
	if err != nil {
		return err
	}
	switch prompt.Ask(r, origin) {
	case Once:
		p.Add(grantFor(r, origin, "prompt"))
		return nil
	case Always:
		g := grantFor(r, origin, "prompt")
		p.Add(g)
		if p.ConfigPath != "" {
			if err := AppendConfig(p.ConfigPath, []Grant{g}); err != nil {
				return fmt.Errorf("recording the grant in %s: %w", p.ConfigPath, err)
			}
		}
		return nil
	}
	return &Refusal{Request: r}
}

// grantFor is the narrowest grant answering r, bound to origin.
func grantFor(r Request, origin Origin, source string) Grant {
	g := Grant{Kind: r.Kind, Subject: origin.Spec, Module: origin.Module, Digest: origin.Digest, Source: source}
	switch r.Kind {
	case Command:
		g.Prefix = slices.Clone(r.Prefix)
		g.BanFlags = slices.Clone(r.BanFlags)
	case Env, File, Dir:
		g.Value = r.Value
	case Net:
		g.Value = stripPort(r.Value)
	}
	return g
}

// GrantFor is the grant `sngl trust` records for r.
func GrantFor(r Request, origin Origin) Grant { return grantFor(r, origin, "") }

// covers reports whether g allows r.
func (g Grant) covers(r *Request) (bool, error) {
	if g.Kind != r.Kind {
		return false, nil
	}
	switch r.Kind {
	case Command:
		if len(g.Prefix) > len(r.Prefix) || !slices.Equal(g.Prefix, r.Prefix[:len(g.Prefix)]) {
			return false, nil
		}
		for _, f := range g.BanFlags {
			if !slices.Contains(r.BanFlags, f) {
				return false, nil
			}
		}
	case Env:
		if g.Value != r.Value {
			return false, nil
		}
	case File:
		if Canonical(g.Value) != r.Value {
			return false, nil
		}
	case Dir:
		if !Within(r.Value, Canonical(g.Value)) {
			return false, nil
		}
	case Net:
		if !HostMatches(g.Value, r.Value) {
			return false, nil
		}
	}
	if g.Subject == "" {
		// Everywhere: a host granted to every project.
		return true, nil
	}
	if !IsOrigin(g.Subject) {
		return samePath(g.Subject, r.Subject.Name), nil
	}
	o, err := r.Subject.origin()
	if err != nil {
		return false, err
	}
	if o.Spec == "" || !sameOrigin(g.Subject, o.Spec) {
		return false, nil
	}
	if g.Module != "" && g.Module != o.Module {
		return false, nil
	}
	if g.Digest != "" && g.Digest != o.Digest {
		return false, nil
	}
	return true, nil
}

// HostMatches reports whether host is one pattern names: the host itself, or
// with a leading `*.` any host below it but not it. Case is ignored, as DNS
// does, and so is a port.
func HostMatches(pattern, host string) bool {
	pattern, host = strings.ToLower(pattern), strings.ToLower(stripPort(host))
	if rest, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(host, "."+rest)
	}
	return pattern == host
}

func stripPort(host string) string {
	if strings.HasPrefix(host, "[") {
		if i := strings.Index(host, "]"); i >= 0 {
			return host[1:i]
		}
	}
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[:i], ":") {
		return host[:i]
	}
	return host
}

// samePath compares two import paths as a flag and a message spell them:
// `./pc` and `pc` are one package, and so are `js:./lib` and `js:lib`.
func samePath(a, b string) bool {
	clean := func(s string) string {
		scheme, rest, ok := strings.Cut(s, ":")
		if !ok || strings.Contains(scheme, "/") || strings.Contains(scheme, ".") {
			scheme, rest = "", s
		}
		if strings.HasPrefix(rest, ".") || !strings.Contains(rest, ".") {
			rest = path.Clean(rest)
		}
		return scheme + ":" + rest
	}
	return clean(a) == clean(b)
}

func sameOrigin(a, b string) bool {
	if da, ok := strings.CutPrefix(a, "dir:"); ok {
		if db, ok := strings.CutPrefix(b, "dir:"); ok {
			return Canonical(da) == Canonical(db)
		}
		return false
	}
	return a == b
}

// Refusal is a request nothing granted. Its message names the flag that
// would allow it and the `sngl trust` line that would record it.
type Refusal struct {
	Request Request
}

func (e *Refusal) Error() string {
	r := e.Request
	name := r.Subject.Name
	switch r.Kind {
	case Command:
		flag := fmt.Sprintf("--allow-command=%q", name+"="+strings.Join(r.Prefix, " "))
		return fmt.Sprintf("%s may not run %s: running it needs %s (or `sngl trust %s` to record it)", name, ShellQuote(r.Cmd), flag, flag)
	case Env:
		flag := fmt.Sprintf("--allow-env=%q", name+"="+r.Value)
		return fmt.Sprintf("%s may not read $%s: reading it needs %s (or `sngl trust %s` to record it)", name, r.Value, flag, flag)
	case File:
		flag := fmt.Sprintf("--allow-file=%q", name+"="+r.Value)
		return fmt.Sprintf("%s may not read %s, outside the import root %s: reading it needs %s (or `sngl trust %s` to record it)", name, r.Value, r.Root, flag, flag)
	case Dir:
		flag := fmt.Sprintf("--allow-dir=%q", name+"="+r.Value)
		return fmt.Sprintf("%s may not list %s, outside the import root %s: listing it needs %s (or `sngl trust %s` to record it)", name, r.Value, r.Root, flag, flag)
	case Net:
		flag := fmt.Sprintf("--allow-net=%q", stripPort(r.Value))
		return fmt.Sprintf("fetching from %s needs %s (or `sngl trust %s` to record it for this project, or with --everywhere for every project)", stripPort(r.Value), flag, flag)
	}
	flag := fmt.Sprintf("--allow-eval=%q", name)
	return fmt.Sprintf("running %s needs %s (or `sngl trust %s` to record it)", name, flag, flag)
}

// ShellQuote spells argv the way a shell would read it back.
func ShellQuote(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = quoteArg(a)
	}
	return strings.Join(out, " ")
}

func quoteArg(a string) string {
	if a == "" {
		return "''"
	}
	safe := true
	for _, c := range a {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_@%+=:,./-", c)) {
			safe = false
			break
		}
	}
	if safe {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
}

// ParseFlag reads one --allow-<kind> value: the code it names, then `=` and
// what it may do, except for an eval grant, which names the code alone. The
// first `=` splits them, since no import path holds one.
func ParseFlag(kind Kind, v string) (Grant, error) {
	g := Grant{Kind: kind, Source: "--allow-" + string(kind)}
	if kind == Net {
		// A host alone is every project's; `<origin>=<host>` one project's.
		subject, host, ok := strings.Cut(v, "=")
		if !ok {
			subject, host = "", v
		}
		if host == "" || strings.ContainsAny(host, "/ ") {
			return Grant{}, fmt.Errorf("--allow-net=%q: want a host, or *.<domain> for its subdomains", v)
		}
		g.Subject, g.Value = subject, strings.ToLower(host)
		return g, nil
	}
	if kind == Eval {
		if v == "" {
			return Grant{}, errors.New("--allow-eval needs a package: --allow-eval=\"go:<import path>\"")
		}
		g.Subject = v
		return g, nil
	}
	subject, value, ok := strings.Cut(v, "=")
	if !ok || subject == "" || strings.TrimSpace(value) == "" {
		return Grant{}, fmt.Errorf("--allow-%s=%q: want <package>=<%s>", kind, v, flagValueName(kind))
	}
	g.Subject = subject
	switch kind {
	case Command:
		g.Prefix = strings.Fields(value)
	default:
		g.Value = value
	}
	return g, nil
}

func flagValueName(k Kind) string {
	switch k {
	case Command:
		return "command prefix"
	case Env:
		return "variable"
	case File:
		return "file"
	}
	return "directory"
}

// EnvVar is the environment variable carrying grants.
const EnvVar = "SNGL_ALLOW"

// ParseEnv reads SNGL_ALLOW: `;`-separated `kind=value` entries in the flags'
// own spelling, `\;` and `\\` escaped, each naming its code by origin.
func ParseEnv(v string) ([]Grant, error) {
	var out []Grant
	for _, entry := range splitEnv(v) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		kind, value, _ := strings.Cut(entry, "=")
		k := Kind(strings.TrimSpace(kind))
		switch k {
		case "all", "allow-all":
			return nil, fmt.Errorf("%s: %q grants everything, which an environment variable may not: pass --allow-all", EnvVar, string(k))
		case Eval, Command, Env, File, Dir, Net:
		default:
			return nil, fmt.Errorf("%s: %q is not a grant: want eval, command, env, file, dir or net", EnvVar, entry)
		}
		g, err := ParseFlag(k, strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", EnvVar, err)
		}
		if !IsOrigin(g.Subject) && !(k == Net && g.Subject == "") {
			return nil, fmt.Errorf("%s: %s names %q by its import path, which any repository may claim; an environment grant names an origin (dir:<path>, go:<module>@<version>)", EnvVar, entry, g.Subject)
		}
		g.Source = EnvVar
		out = append(out, g)
	}
	return out, nil
}

func splitEnv(v string) []string {
	var out []string
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		switch {
		case v[i] == '\\' && i+1 < len(v) && (v[i+1] == ';' || v[i+1] == '\\'):
			b.WriteByte(v[i+1])
			i++
		case v[i] == ';':
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteByte(v[i])
		}
	}
	return append(out, b.String())
}

// ConfigPath is the user's config file, or "" when the platform has no config
// directory.
func ConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "sngl", "trust.sngl")
}
