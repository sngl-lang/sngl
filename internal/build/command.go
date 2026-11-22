package build

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/interp"
	"duckfam.us/sngl/ir"
)

// A command is what `sngl run` and `sngl build` do with a target's output:
// a `gen.run` or `gen.build` in the body of the target's build-tree node,
// whose handler runs in the interpreter with the node's props bound to the
// options the target was built with. A target that writes none does not have
// the command -- the capability polarity, applied to the CLI.

// command is one handler a target's node holds.
type command struct {
	handler *ir.EventHandler
	node    *ir.Component
	pkg     *ir.Package
}

// findCommand is t's command of the given kind: the platform node's override
// for t's language, else the platform node's body, else the language node's.
func findCommand(t Target, kind ir.BuiltinKind) (*command, error) {
	plat, err := targetNode("platform/"+t.Platform, ir.BuiltinFamilyPlatform)
	if err != nil {
		return nil, err
	}
	lang, err := targetNode("language/"+t.Lang, ir.BuiltinFamilyLanguage)
	if err != nil {
		return nil, err
	}
	if plat.node != nil {
		if b, ok := plat.node.LanguageOverrides[t.Lang]; ok {
			if h := commandIn(b.Stmts, kind); h != nil {
				return &command{handler: h, node: plat.node, pkg: plat.pkg}, nil
			}
		}
	}
	for _, n := range []*command{plat, lang} {
		if n.node == nil {
			continue
		}
		if h := commandIn(n.node.Stmts, kind); h != nil {
			return &command{handler: h, node: n.node, pkg: n.pkg}, nil
		}
	}
	return nil, nil
}

func targetNode(uri string, tier ir.BuiltinKind) (*command, error) {
	pkg, diags := checker.CheckLibPackage(uri)
	for _, d := range diags {
		if d.Severity == ir.Error {
			return nil, fmt.Errorf("%s: %s", uri, d.Msg)
		}
	}
	return &command{node: ir.TargetNodeOf(pkg, tier), pkg: pkg}, nil
}

// commandIn is the handler of the command of the given kind a node's body
// holds.
func commandIn(stmts []ir.Stmt, kind ir.BuiltinKind) *ir.EventHandler {
	for _, st := range stmts {
		ni, ok := st.(*ir.NodeInst)
		if !ok || ni.Component == nil || ni.Component.Builtin != kind {
			continue
		}
		for i := range ni.Handlers {
			return &ni.Handlers[i]
		}
	}
	return nil
}

// HasCommand reports whether t can run kind.
func HasCommand(t Target, kind ir.BuiltinKind) bool {
	c, err := findCommand(t, kind)
	return err == nil && c != nil
}

// RunCommand runs t's command of the given kind with args, which are the
// handler's parameters in order: `dir` and `args` for gen.run, `dir` and
// `out` for gen.build.
func RunCommand(t Target, kind ir.BuiltinKind, args ...any) error {
	c, err := findCommand(t, kind)
	if err != nil {
		return err
	}
	what := "run"
	if kind == ir.BuiltinGenBuild {
		what = "build"
	}
	if c == nil {
		return fmt.Errorf("neither platform %q nor language %q says how to %s what it generates", t.Platform, t.Lang, what)
	}
	env, err := c.bind(t)
	if err != nil {
		return err
	}
	fn := c.handler.Func
	if len(args) > len(fn.Params) {
		args = args[:len(fn.Params)]
	}
	_, err = env.CallUserFuncValues(fn, args)
	if raised, ok := errors.AsType[*interp.RaisedError](err); ok {
		msg, _ := raised.Event["message"].(string)
		return fmt.Errorf("%s: %s", c.node.DisplayName(), msg)
	}
	if err != nil && !interp.IsReturn(err) {
		return err
	}
	return nil
}

// bind is an interpreter for c's node with each of its props bound to the
// option t sets, or else to its default -- which may ask gen.release(), so the
// build host answers before the first is read.
func (c *command) bind(t Target) (*interp.Env, error) {
	env, err := interp.BuildEnv(c.pkg, "", nil)
	if err != nil {
		return nil, err
	}
	env.SetBuildHost(&commandHost{opts: t.Options, release: t.Release})
	for _, p := range c.node.Props {
		var v any
		if e, ok := codegen.OptionField(t.Options, p.Name); ok {
			v, err = env.Eval(e)
		} else if p.Default != nil {
			v, err = env.Eval(p.Default)
		}
		if err != nil {
			return nil, fmt.Errorf("%s option %s: %w", c.node.DisplayName(), p.Name, err)
		}
		env.Set(p.Sym, v)
	}
	return env, nil
}

// ResolveOptions is t's options with each scalar or enum option t's language
// node declares and t leaves unset bound to its default, as this build reads
// it: what a test launcher, which compiles outside the node's own command, is
// handed. A list or a slot is no build option a launcher reads, and is left
// out. An option set as text -- `sngl test --opt`, which no output block
// types -- is read against the type the node declares for it, so an enum's
// member is refused here, before a record could answer for the launch, as
// `sngl build` refuses it (applyCLIOpts).
func ResolveOptions(t Target) (*ir.StructLit, error) {
	out := cloneStructLit(t.Options)
	lang, err := targetNode("language/"+t.Lang, ir.BuiltinFamilyLanguage)
	if err != nil || lang.node == nil {
		return out, err
	}
	for _, p := range lang.node.Props {
		v, set := codegen.OptionField(out, p.Name)
		_, isText := v.(*ir.Literal)
		if !set || !isText || p.Type == nil || p.Type.Kind != ir.TypeEnum {
			continue
		}
		member, err := enumOption(p.Name, codegen.OptionString(out, p.Name), p.Type)
		if err != nil {
			return nil, err
		}
		codegen.SetOptionField(out, p.Name, member)
	}
	t.Options = out
	env, err := lang.bind(t)
	if err != nil {
		return nil, err
	}
	for _, p := range lang.node.Props {
		if _, set := codegen.OptionField(out, p.Name); set || p.Default == nil {
			continue
		}
		v, _ := env.Value(p.Sym)
		switch v.(type) {
		case string, bool, int, int64, float64:
			codegen.SetOptionField(out, p.Name, v)
		}
	}
	return out, nil
}

// commandHost answers the build-only calls a command handler makes: gen.shell,
// gen.release, and whatever a target registered with codegen.RegisterCommand.
type commandHost struct {
	opts    *ir.StructLit
	release bool
}

func (h *commandHost) Call(id string, _ *ir.Func, _ *ir.Call, args []any) (any, error) {
	switch id {
	case "gen.shell":
		return shell(args)
	case "gen.release":
		return h.release, nil
	}
	if fn := codegen.LookupCommand(id); fn != nil {
		return fn(h.opts, args)
	}
	return nil, fmt.Errorf("%s does not answer in a command handler", id)
}

// shell runs a process on the user's terminal and returns its exit status.
func shell(args []any) (any, error) {
	var cmd []string
	if len(args) > 0 {
		xs, _ := args[0].([]any)
		for _, x := range xs {
			s, _ := x.(string)
			cmd = append(cmd, s)
		}
	}
	if len(cmd) == 0 {
		return nil, errors.New("gen.shell: no command")
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	if len(args) > 1 {
		c.Dir, _ = args[1].(string)
	}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	slog.Info("exec", "cmd", cmd, "dir", c.Dir)
	err := c.Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("gen.shell: %w", err)
	}
	return 0, nil
}
