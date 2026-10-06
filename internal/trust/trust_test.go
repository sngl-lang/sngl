package trust

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseEnvSplitsOnUnescapedSemicolons(t *testing.T) {
	gs, err := ParseEnv(` eval=dir:/src/docs ; command=dir:/src/pc=sh -c ; file=dir:/src/pc=/a\;b ;; env=go:example.com/x@v1.0.0=A\\B`)
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 4 {
		t.Fatalf("got %d grants, want 4: %+v", len(gs), gs)
	}
	if gs[0].Kind != Eval || gs[0].Subject != "dir:/src/docs" {
		t.Errorf("eval: %+v", gs[0])
	}
	if gs[1].Kind != Command || !slices.Equal(gs[1].Prefix, []string{"sh", "-c"}) {
		t.Errorf("command: %+v", gs[1])
	}
	if gs[2].Value != "/a;b" {
		t.Errorf(`\; is a literal semicolon: %+v`, gs[2])
	}
	if gs[3].Subject != "go:example.com/x@v1.0.0" || gs[3].Value != `A\B` {
		t.Errorf(`\\ is a literal backslash: %+v`, gs[3])
	}
}

func TestParseEnvRefusesBlanketAndNamedGrants(t *testing.T) {
	for _, v := range []string{"all", "allow-all"} {
		if _, err := ParseEnv(v); err == nil || !strings.Contains(err.Error(), "pass --allow-all") {
			t.Errorf("%q: %v", v, err)
		}
	}
	// An import path is a name any repository may claim.
	if _, err := ParseEnv("eval=go:example.com/docs"); err == nil || !strings.Contains(err.Error(), "names an origin") {
		t.Errorf("import path: %v", err)
	}
	if _, err := ParseEnv("command=./pc=sh"); err == nil {
		t.Error("a relative import path is a name too")
	}
}

func TestCommandGrantCoversItsPrefixAndNoMore(t *testing.T) {
	req := func(prefix ...string) Request {
		return Request{Kind: Command, Subject: Subject{Name: "./pc"}, Cmd: append(slices.Clone(prefix), "x"), Prefix: prefix, BanFlags: []string{"-toolexec"}}
	}
	p := &Policy{}
	p.Add(Grant{Kind: Command, Subject: "./pc", Prefix: []string{"go"}})
	if err := p.Check(req("go", "list")); err != nil {
		t.Errorf("a broader grant covers a narrower declared prefix: %v", err)
	}
	if err := p.Check(req("gofmt")); err == nil {
		t.Error("a prefix is whole words")
	}
	p = &Policy{}
	p.Add(Grant{Kind: Command, Subject: "pc", Prefix: []string{"go", "list"}})
	if err := p.Check(req("go")); err == nil {
		t.Error("a narrower grant covers a broader declared prefix")
	}
	if err := p.Check(req("go", "list")); err != nil {
		t.Errorf("./pc and pc are one package: %v", err)
	}
	// A recorded grant holds the bans the call made when it was granted.
	p = &Policy{}
	p.Add(Grant{Kind: Command, Subject: "./pc", Prefix: []string{"go"}, BanFlags: []string{"-toolexec", "-exec"}})
	if err := p.Check(req("go")); err == nil {
		t.Error("a call banning less than it did when granted is asked again")
	}
}

func TestOriginGrantBindsModuleAndDigest(t *testing.T) {
	dir := t.TempDir()
	o := DirOrigin(dir)
	o.Module = "example.com/a"
	sub := func(o Origin) Subject { return Subject{Name: "go:example.com/a/lib", Origin: o} }
	p := &Policy{}
	p.Add(Grant{Kind: Eval, Subject: o.Spec, Module: "example.com/a"})
	if err := p.Check(Request{Kind: Eval, Subject: sub(o)}); err != nil {
		t.Errorf("the origin granted: %v", err)
	}
	other := o
	other.Module = "example.com/b"
	if err := p.Check(Request{Kind: Eval, Subject: sub(other)}); err == nil {
		t.Error("the directory reused for another module is asked again")
	}
	if err := p.Check(Request{Kind: Eval, Subject: sub(DirOrigin(t.TempDir()))}); err == nil {
		t.Error("another directory with the same import path is not the origin granted")
	}

	p = &Policy{}
	p.Add(Grant{Kind: Eval, Subject: "git://example.com/p@v1", Digest: "abc"})
	if err := p.Check(Request{Kind: Eval, Subject: Subject{Origin: Origin{Spec: "git://example.com/p@v1", Digest: "def"}}}); err == nil {
		t.Error("a fetched package whose content changed is asked again")
	}
}

func TestDirGrantCoversWhatIsBelowIt(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	p := &Policy{}
	p.Add(Grant{Kind: Dir, Subject: "./pc", Value: filepath.Join(dir, "a")})
	in := Request{Kind: Dir, Subject: Subject{Name: "./pc"}, Value: Canonical(filepath.Join(dir, "a", "b"))}
	if err := p.Check(in); err != nil {
		t.Errorf("below the granted directory: %v", err)
	}
	out := Request{Kind: Dir, Subject: Subject{Name: "./pc"}, Value: Canonical(dir + "-a")}
	if err := p.Check(out); err == nil {
		t.Error("a sibling sharing the granted directory's name as a prefix is not below it")
	}
	file := Request{Kind: File, Subject: Subject{Name: "./pc"}, Value: Canonical(filepath.Join(dir, "a", "b", "f.pc"))}
	if err := p.Check(file); err != nil {
		t.Errorf("a file below the granted directory: %v", err)
	}
	fileOut := Request{Kind: File, Subject: Subject{Name: "./pc"}, Value: Canonical(filepath.Join(dir, "f.pc"))}
	if err := p.Check(fileOut); err == nil {
		t.Error("a file beside the granted directory is not below it")
	}
	fg := &Policy{}
	fg.Add(Grant{Kind: File, Subject: "./pc", Value: filepath.Join(dir, "a")})
	if err := fg.Check(in); err == nil {
		t.Error("a file grant does not cover a listing")
	}
}

func TestAllowingAddsKindsAndKeepsGrants(t *testing.T) {
	p := &Policy{}
	p.Add(Grant{Kind: Env, Subject: "./pc", Value: "HOME"})
	q := p.Allowing(Net)
	if err := q.Check(Request{Kind: Net, Subject: Subject{Name: "./x"}, Value: "example.com"}); err != nil {
		t.Errorf("the allowed kind: %v", err)
	}
	if err := q.Check(Request{Kind: Env, Subject: Subject{Name: "./pc"}, Value: "HOME"}); err != nil {
		t.Errorf("a grant the policy held: %v", err)
	}
	if err := q.Check(Request{Kind: Command, Subject: Subject{Name: "./pc"}, Cmd: []string{"sh"}, Prefix: []string{"sh"}}); err == nil {
		t.Error("a kind neither allowed nor granted")
	}
	if err := p.Check(Request{Kind: Net, Subject: Subject{Name: "./x"}, Value: "example.com"}); err == nil {
		t.Error("Allowing changed the policy it copied")
	}
}

func TestLibraryAndNilPolicy(t *testing.T) {
	var p *Policy
	r := Request{Kind: Env, Subject: Subject{Name: "./pc"}, Value: "HOME"}
	var ref *Refusal
	if err := p.Check(r); !errors.As(err, &ref) {
		t.Errorf("a nil policy refuses: %v", err)
	}
	r.Subject.Library = true
	if err := p.Check(r); err != nil {
		t.Errorf("a library is trusted: %v", err)
	}
}

type answer Answer

func (a answer) Ask(Request, Origin) Answer { return Answer(a) }

func TestPromptAlwaysRecordsByOrigin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sngl", "trust.sngl")
	dir := t.TempDir()
	p := &Policy{Prompt: answer(Always), ConfigPath: path}
	r := Request{Kind: Env, Subject: Subject{Name: "./pc", Origin: DirOrigin(dir)}, Value: "HOME"}
	if err := p.Check(r); err != nil {
		t.Fatal(err)
	}
	allows, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(allows) != 1 || allows[0].Origin.Spec != DirOrigin(dir).Spec || allows[0].Grants[0].Value != "HOME" {
		t.Fatalf("recorded %+v", allows)
	}
	// Once answers for the rest of the invocation and records nothing.
	path2 := filepath.Join(t.TempDir(), "trust.sngl")
	p = &Policy{Prompt: answer(Once), ConfigPath: path2}
	if err := p.Check(r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path2); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("once wrote the config: %v", err)
	}
	p.Prompt = answer(No)
	if err := p.Check(r); err != nil {
		t.Errorf("a grant given once holds for the invocation: %v", err)
	}
}

func TestRemoveByNumberAndOrigin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.sngl")
	if err := AppendConfig(path, []Grant{
		{Kind: Eval, Subject: "dir:/a"},
		{Kind: Env, Subject: "dir:/b", Value: "X"},
		{Kind: Eval, Subject: "dir:/c"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(path, "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(path, "dir:/c"); err != nil {
		t.Fatal(err)
	}
	allows, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(allows) != 1 || allows[0].Origin.Spec != "dir:/a" {
		t.Fatalf("left %+v", allows)
	}
	if _, err := Remove(path, "7"); err == nil {
		t.Error("removing nothing is an error")
	}
}

func TestNetGrantMatchesHostsExactlyOrBelow(t *testing.T) {
	for _, c := range []struct {
		pattern, host string
		want          bool
	}{
		{"github.com", "github.com", true},
		{"github.com", "GitHub.com:443", true},
		{"github.com", "api.github.com", false},
		{"*.github.com", "api.github.com", true},
		{"*.github.com", "github.com", false},
		{"*.github.com", "evilgithub.com", false},
	} {
		if got := HostMatches(c.pattern, c.host); got != c.want {
			t.Errorf("HostMatches(%q, %q) = %v, want %v", c.pattern, c.host, got, c.want)
		}
	}
	project := Subject{Name: ".", Origin: DirOrigin(t.TempDir())}
	other := Subject{Name: ".", Origin: DirOrigin(t.TempDir())}
	p := &Policy{}
	p.Add(Grant{Kind: Net, Subject: project.Origin.Spec, Value: "github.com"})
	if err := p.Check(Request{Kind: Net, Subject: project, Value: "github.com"}); err != nil {
		t.Errorf("the project granted: %v", err)
	}
	if err := p.Check(Request{Kind: Net, Subject: other, Value: "github.com"}); err == nil {
		t.Error("another project is not granted")
	}
	p.Add(Grant{Kind: Net, Value: "github.com"})
	if err := p.Check(Request{Kind: Net, Subject: other, Value: "github.com"}); err != nil {
		t.Errorf("everywhere: %v", err)
	}
}
