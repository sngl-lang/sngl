package lsp

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/gencache"
	"duckfam.us/sngl/internal/trust"
)

// A plugin's handler runs while the server checks a file, under the grants it
// was started with and never a prompt: a refused call is a diagnostic at the
// import that reached the plugin, naming the flag that would allow it, and a
// grant in the config file is honoured.
func TestPluginRefusalIsDiagnosticAtImport(t *testing.T) {
	t.Setenv(gencache.OffEnv, "off")
	gencache.ResetDefault()
	t.Cleanup(gencache.ResetDefault)

	dir := t.TempDir()
	app := filepath.Join(dir, "app.sngl")
	src := "import x \"run:x\"\nimport \"./pc\"\n\nconst v = x.line\n"
	write(t, app, src)
	write(t, filepath.Join(dir, "pc", "pc.sngl"), `import gen "sngl:x/gen"

gen.scheme(name="run", @generate(out, importPath) {
    var p = gen.exec(cmd=["sh", "-c", "echo hi"])
    var line = ""
    for var l = p.stdout {
        line = l
    }
    out.write("x.sngl", "const line = \"" + line + "\"\n")
})
`)

	srv := NewWithTrust(&trust.Policy{Prompt: refuseAll{t}})
	diags := srv.analyze(srv.ws.open("file://"+app, src, 1))
	var found bool
	for _, d := range diags {
		if d.Range.Start.Line == 0 && strings.Contains(d.Message, `--allow-command="./pc=sh"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("want the refusal at the import, naming the flag; got %+v", diags)
	}

	// The same call, granted in the config file.
	config := filepath.Join(t.TempDir(), "trust.sngl")
	g := trust.GrantFor(trust.Request{Kind: trust.Command, Prefix: []string{"sh"}}, trust.DirOrigin(filepath.Join(dir, "pc")))
	if err := trust.AppendConfig(config, []trust.Grant{g}); err != nil {
		t.Fatal(err)
	}
	policy := &trust.Policy{}
	if err := policy.Load(config); err != nil {
		t.Fatal(err)
	}
	srv = NewWithTrust(policy)
	for _, d := range srv.analyze(srv.ws.open("file://"+app, src, 1)) {
		t.Errorf("granted in the config file, still: %s", d.Message)
	}
}

// The grants a flag or SNGL_ALLOW gave the server are logged, so one nobody
// meant to give an editor's server is visible; the config file's are not.
func TestLogsGrantsFromFlagsAndEnvironment(t *testing.T) {
	p := &trust.Policy{}
	flag, err := trust.ParseFlag(trust.Command, "./pc=go list")
	if err != nil {
		t.Fatal(err)
	}
	env, err := trust.ParseEnv("env=dir:/somewhere=HOME")
	if err != nil {
		t.Fatal(err)
	}
	p.Add(flag)
	p.Add(env...)
	p.Add(trust.Grant{Kind: trust.Env, Subject: "dir:/elsewhere", Value: "PATH", Source: "config"})
	srv := New()
	var buf bytes.Buffer
	srv.log = log.New(&buf, "", 0)
	srv.trust = p
	srv.logGrants()
	got := buf.String()
	for _, want := range []string{`--allow-command grants trust.command(prefix=["go", "list"]) to ./pc`, `SNGL_ALLOW grants trust.env(name="HOME") to dir:/somewhere`} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "PATH") {
		t.Errorf("logged a config grant:\n%s", got)
	}
}

// refuseAll fails the test if the server ever prompts.
type refuseAll struct{ t *testing.T }

func (r refuseAll) Ask(trust.Request, trust.Origin) trust.Answer {
	r.t.Error("the server prompted")
	return trust.No
}

func write(t *testing.T, path, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}
