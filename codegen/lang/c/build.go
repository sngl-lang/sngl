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
		if _, err := Host(str(2)).BuildDir(str(0), str(1), std); err != nil {
			return nil, err
		}
		return str(1), nil
	})
}
