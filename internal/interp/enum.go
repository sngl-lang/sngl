package interp

import (
	"fmt"

	"duckfam.us/sngl/ir"
)

// EnumValue is a value of an enum whose members carry payloads (ir/enums.go):
// the member it holds, and that member's payload, nil for a member that
// carries none. A value of an enum carrying nothing is its member's name, as
// it always was.
type EnumValue struct {
	Type    *ir.Type
	Member  string
	Payload any
}

func (v EnumValue) String() string {
	if v.Payload == nil {
		return v.Member
	}
	return fmt.Sprintf("%s(%v)", v.Member, v.Payload)
}

func init() {
	intrinsics[ir.EnumIsIntrinsic] = func(args []any) (any, error) {
		v, member, err := enumArgs(args)
		if err != nil {
			return nil, err
		}
		return v.Member == member, nil
	}
	intrinsics[ir.EnumPayloadIntrinsic] = func(args []any) (any, error) {
		v, member, err := enumArgs(args)
		if err != nil {
			return nil, err
		}
		if v.Member != member {
			return nil, fmt.Errorf("enum.payload: the value holds %s, not %s", v.Member, member)
		}
		return v.Payload, nil
	}
}

func enumArgs(args []any) (EnumValue, string, error) {
	if len(args) != 2 {
		return EnumValue{}, "", fmt.Errorf("enum read: want two arguments, got %d", len(args))
	}
	v, ok := args[0].(EnumValue)
	member, isStr := args[1].(string)
	if !ok || !isStr {
		return EnumValue{}, "", fmt.Errorf("enum read: not an enum value: %v", args[0])
	}
	return v, member, nil
}

// enumMemberRead is `v.member` off a value of a payload enum: the payload
// where v holds member, and null where it holds another.
func enumMemberRead(obj any, member string) (any, bool) {
	v, ok := obj.(EnumValue)
	if !ok {
		return nil, false
	}
	if v.Member != member {
		return nil, true
	}
	return v.Payload, true
}
