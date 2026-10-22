package interp

import (
	"errors"

	"duckfam.us/sngl/ir"
)

// Builder is sngl:builtin's builder: text that grows in place. It is a value
// in SNGL, so a store copies it (copyOf) and an add writes through to the one
// place that holds it.
type Builder struct{ buf []byte }

// String is what has been built.
func (b *Builder) String() string { return string(b.buf) }

func (b *Builder) clone() *Builder { return &Builder{buf: append([]byte(nil), b.buf...)} }

func init() {
	intrinsics[ir.BuilderAdd] = func(args []any) (any, error) {
		b, err := builderArg(args, 2)
		if err != nil {
			return nil, err
		}
		b.buf = append(b.buf, toString(args[1])...)
		return nil, nil
	}
	intrinsics[ir.BuilderLine] = func(args []any) (any, error) {
		b, err := builderArg(args, 1)
		if err != nil {
			return nil, err
		}
		if len(args) > 1 {
			b.buf = append(b.buf, toString(args[1])...)
		}
		b.buf = append(b.buf, '\n')
		return nil, nil
	}
	intrinsics[ir.BuilderString] = func(args []any) (any, error) {
		b, err := builderArg(args, 1)
		if err != nil {
			return nil, err
		}
		return b.String(), nil
	}
}

func builderArg(args []any, want int) (*Builder, error) {
	if len(args) < want {
		return nil, errors.New("builder: missing argument")
	}
	b, ok := args[0].(*Builder)
	if !ok {
		return nil, errors.New("builder: the receiver is no builder")
	}
	return b, nil
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}
