package checker

import (
	"fmt"
	"os"

	"git.duckfam.us/jonathan/sngl/ast"
	emproto "github.com/emicklei/proto"
)

// ProtoResult holds the parsed results from a .proto file.
type ProtoResult struct {
	Structs []*ast.StructDef
	Enums   []*ast.EnumDef
}

// ParseProtoFile parses a .proto file and returns struct/enum definitions.
func ParseProtoFile(path string) (*ProtoResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	parser := emproto.NewParser(f)
	def, err := parser.Parse()
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	result := &ProtoResult{}

	emproto.Walk(def,
		emproto.WithMessage(func(m *emproto.Message) {
			sd := &ast.StructDef{Name: m.Name}
			for _, el := range m.Elements {
				if field, ok := el.(*emproto.NormalField); ok {
					sd.Fields = append(sd.Fields, &ast.StructField{
						Name: field.Name,
						Type: protoTypeToHint(field.Type, field.Repeated),
					})
				}
			}
			result.Structs = append(result.Structs, sd)
		}),
		emproto.WithEnum(func(e *emproto.Enum) {
			ed := &ast.EnumDef{Name: e.Name}
			for _, el := range e.Elements {
				if val, ok := el.(*emproto.EnumField); ok {
					ed.Values = append(ed.Values, val.Name)
				}
			}
			result.Enums = append(result.Enums, ed)
		}),
	)

	return result, nil
}

func protoTypeToHint(typ string, repeated bool) string {
	hint := protoScalarToHint(typ)
	if repeated {
		return "list:" + hint
	}
	return hint
}

func protoScalarToHint(typ string) string {
	switch typ {
	case "string":
		return "string"
	case "bool":
		return "bool"
	case "int32", "int64", "uint32", "uint64", "sint32", "sint64",
		"fixed32", "fixed64", "sfixed32", "sfixed64":
		return "int"
	case "float", "double":
		return "float"
	case "bytes":
		return "string"
	default:
		return typ
	}
}
