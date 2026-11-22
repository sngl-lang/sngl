package c

import (
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

func init() {
	codegen.RegisterCommand("c.buildProgram", func(_ *ir.StructLit, args []any) (any, error) {
		str := func(i int) string {
			if i < len(args) {
				s, _ := args[i].(string)
				return s
			}
			return ""
		}
		std := str(3)
		if std == "" {
			std = "c17"
		}
		cc, err := ForTarget(TargetOf(str(0)), str(2))
		if err != nil {
			return nil, err
		}
		if _, err := cc.BuildDir(str(0), str(1), std, str(4)); err != nil {
			return nil, err
		}
		return cc.Exe(str(1)), nil
	})
	codegen.RegisterCommand("c.launch", func(_ *ir.StructLit, args []any) (any, error) {
		exe, _ := args[0].(string)
		var out []any
		for _, a := range Launch(exe) {
			out = append(out, a)
		}
		return out, nil
	})
}
