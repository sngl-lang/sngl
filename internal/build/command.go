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
	plat, err := targetNode("platform/"+t.Platform, ir.BuiltinPlatform)
	if err != nil {
		return nil, err
	}
	lang, err := targetNode("language/"+t.Lang, ir.BuiltinLanguage)
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
		if h := commandIn(n.node.Body, kind); h != nil {
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
	env, err := interp.BuildEnv(c.pkg, "")
	if err != nil {
		return err
	}
	for _, p := range c.node.Props {
		var v any
		if e, ok := codegen.OptionField(t.Options, p.Name); ok {
			v, err = env.Eval(e)
		} else if p.Default != nil {
			v, err = env.Eval(p.Default)
		}
		if err != nil {
			return fmt.Errorf("%s option %s: %w", c.node.DisplayName(), p.Name, err)
		}
		env.Set(p.Sym, v)
	}
	env.SetBuildHost(&commandHost{opts: t.Options})
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

// commandHost answers the build-only calls a command handler makes: gen.shell,
// and whatever a target registered with codegen.RegisterCommand.
type commandHost struct {
	opts *ir.StructLit
}

func (h *commandHost) Call(id string, _ *ir.Func, _ *ir.Call, args []any) (any, error) {
	if id == "gen.shell" {
		return shell(args)
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
