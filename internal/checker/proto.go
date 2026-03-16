package checker

import (
	"fmt"
	"os"

	"git.duckfam.us/jonathan/sngl/ast"
	emproto "github.com/emicklei/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// ProtoResult holds the parsed results from a .proto file.
type ProtoResult struct {
	Structs  []*ast.StructDef
	Enums    []*ast.EnumDef
	FileDesc *descriptorpb.FileDescriptorProto
}

// ParseProtoFile parses a .proto file and returns struct/enum definitions
// and a FileDescriptorProto for CEL type registration.
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

	result := &ProtoResult{
		FileDesc: &descriptorpb.FileDescriptorProto{
			Name:   proto.String(path),
			Syntax: proto.String("proto3"),
		},
	}

	emproto.Walk(def,
		emproto.WithMessage(func(m *emproto.Message) {
			sd := &ast.StructDef{Name: m.Name}
			descMsg := &descriptorpb.DescriptorProto{Name: proto.String(m.Name)}
			fieldNum := int32(1)
			for _, el := range m.Elements {
				if field, ok := el.(*emproto.NormalField); ok {
					sd.Fields = append(sd.Fields, &ast.StructField{
						Name: field.Name,
						Type: protoTypeToHint(field.Type, field.Repeated),
					})
					descMsg.Field = append(descMsg.Field, &descriptorpb.FieldDescriptorProto{
						Name:   proto.String(field.Name),
						Number: proto.Int32(fieldNum),
						Type:   protoTypeToDescType(field.Type),
						Label:  protoLabel(field.Repeated),
					})
					fieldNum++
				}
			}
			result.Structs = append(result.Structs, sd)
			result.FileDesc.MessageType = append(result.FileDesc.MessageType, descMsg)
		}),
		emproto.WithEnum(func(e *emproto.Enum) {
			ed := &ast.EnumDef{Name: e.Name}
			descEnum := &descriptorpb.EnumDescriptorProto{Name: proto.String(e.Name)}
			for _, el := range e.Elements {
				if val, ok := el.(*emproto.EnumField); ok {
					ed.Values = append(ed.Values, val.Name)
					descEnum.Value = append(descEnum.Value, &descriptorpb.EnumValueDescriptorProto{
						Name:   proto.String(val.Name),
						Number: proto.Int32(int32(val.Integer)),
					})
				}
			}
			result.Enums = append(result.Enums, ed)
			result.FileDesc.EnumType = append(result.FileDesc.EnumType, descEnum)
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

func protoTypeToDescType(typ string) *descriptorpb.FieldDescriptorProto_Type {
	var t descriptorpb.FieldDescriptorProto_Type
	switch typ {
	case "string":
		t = descriptorpb.FieldDescriptorProto_TYPE_STRING
	case "bool":
		t = descriptorpb.FieldDescriptorProto_TYPE_BOOL
	case "int32":
		t = descriptorpb.FieldDescriptorProto_TYPE_INT32
	case "int64":
		t = descriptorpb.FieldDescriptorProto_TYPE_INT64
	case "uint32":
		t = descriptorpb.FieldDescriptorProto_TYPE_UINT32
	case "uint64":
		t = descriptorpb.FieldDescriptorProto_TYPE_UINT64
	case "sint32":
		t = descriptorpb.FieldDescriptorProto_TYPE_SINT32
	case "sint64":
		t = descriptorpb.FieldDescriptorProto_TYPE_SINT64
	case "float":
		t = descriptorpb.FieldDescriptorProto_TYPE_FLOAT
	case "double":
		t = descriptorpb.FieldDescriptorProto_TYPE_DOUBLE
	case "bytes":
		t = descriptorpb.FieldDescriptorProto_TYPE_BYTES
	default:
		t = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
	}
	return &t
}

func protoLabel(repeated bool) *descriptorpb.FieldDescriptorProto_Label {
	if repeated {
		l := descriptorpb.FieldDescriptorProto_LABEL_REPEATED
		return &l
	}
	l := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	return &l
}
