package golang

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// A test reads a member of an instance built at run time through the Model,
// which holds each instance a test reads for that: the record under its id,
// or one copy's under the key the copy stands in (codegen.InstanceReads).

// InstanceField is the Model field holding the record of the instance id.
func InstanceField(id string) string { return "__inst_" + id }

// InstancesField is the Model field holding the record of each copy of the
// instance id, by key.
func InstancesField(id string) string { return "__insts_" + id }

// InstanceRegistry is the Model method putting a copy's record under its key.
func InstanceRegistry(id string) string { return "__listInst_" + id }

// InstanceRecordType is the Go type of comp's record.
func InstanceRecordType(comp *ir.Component) string { return "*" + ComponentInstanceType(comp.Name) }

// InstanceHolds is each Model field the reads need, by name, with its Go type.
func InstanceHolds(reads []codegen.InstanceRead) [][2]string {
	var out [][2]string
	seen := map[string]bool{}
	for _, r := range reads {
		name, goType := InstanceField(r.ID), InstanceRecordType(r.Comp)
		if r.Keyed {
			name, goType = InstancesField(r.ID), "map[string]"+goType
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, [2]string{name, goType})
		}
	}
	return out
}

// InstanceRead is the read gc's build makes through the instance id, by
// which the Model holds it: through one copy, or the instance whole.
func (gc *GoIRContext) InstanceRead(id string) (codegen.InstanceRead, bool) {
	for _, r := range gc.InstanceReads {
		if r.ID == id {
			return r, true
		}
	}
	return codegen.InstanceRead{}, false
}

// HoldInstance is the statement binding the record inst of the instance id on
// the Model where its root is bound: the copy key's entry where key is set.
func HoldInstance(model *ir.Ident, id string, comp *ir.Component, inst, key ir.Expr) ir.Stmt {
	if key != nil {
		return &ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: model,
			Func:     &ir.Func{Name: InstanceRegistry(id)},
			Args:     []ir.CallArg{{Value: key}, {Value: inst}},
		}}
	}
	return &ir.Assign{
		Target: codegen.RecvFieldRef(model, InstanceField(id), ir.NativeGoNamed(InstanceRecordType(comp))),
		Op:     ast.AssignSet,
		Value:  inst,
	}
}

// InstanceRelease is the Destroy lines of comp's record, recv, letting go of
// what the Model holds of it.
func InstanceRelease(reads []codegen.InstanceRead, comp *ir.Component, recv string) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, r := range reads {
		if r.Comp != comp {
			continue
		}
		if r.Keyed {
			field := recv + "." + InstanceModelField + "." + InstancesField(r.ID)
			if !seen[field] {
				seen[field] = true
				fmt.Fprintf(&b, "\tfor k, held := range %s {\n\t\tif held == %s {\n\t\t\tdelete(%s, k)\n\t\t}\n\t}\n", field, recv, field)
			}
			continue
		}
		field := recv + "." + InstanceModelField + "." + InstanceField(r.ID)
		if !seen[field] {
			seen[field] = true
			fmt.Fprintf(&b, "\tif %s == %s {\n\t\t%s = nil\n\t}\n", field, recv, field)
		}
	}
	return b.String()
}

// EmitInstanceReads writes each keyed InstanceRegistry and each
// codegen.InstanceReadMethod on the Model, for tests: a read of an instance
// the Model holds none of -- it is not rendered -- is the member's zero.
// method is the name a func carries on the record, and varType a var's type
// there.
func EmitInstanceReads(b *strings.Builder, reads []codegen.InstanceRead, method func(*ir.Func) string, varType func(*ir.Var) string) {
	seen := map[string]bool{}
	for _, r := range reads {
		if !r.Keyed || seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		field, rec := InstancesField(r.ID), InstanceRecordType(r.Comp)
		fmt.Fprintf(b, "func (m *Model) %s(key string, c %s) {\n", InstanceRegistry(r.ID), rec)
		fmt.Fprintf(b, "\tfor k, h := range m.%s {\n\t\tif h == c {\n\t\t\tdelete(m.%s, k)\n\t\t}\n\t}\n", field, field)
		fmt.Fprintf(b, "\tif m.%s == nil {\n\t\tm.%s = map[string]%s{}\n\t}\n\tm.%s[key] = c\n}\n\n", field, field, rec, field)
	}
	for _, r := range reads {
		name := codegen.InstanceReadMethod(r)
		var params, args []string
		lookup := "m." + InstanceField(r.ID)
		if r.Keyed {
			params = append(params, "key string")
			lookup = "m." + InstancesField(r.ID) + "[key]"
		}
		result, body := "", ""
		switch {
		case r.Func != nil:
			for _, p := range r.Func.Params {
				if p.Receiver {
					continue
				}
				params = append(params, p.Name+" "+IRTypeToGo(p.Type))
				args = append(args, p.Name)
			}
			call := "c." + method(r.Func) + "(" + strings.Join(args, ", ") + ")"
			if ret := r.Func.Return; ret != nil && ret.Kind != ir.TypeVoid {
				result = " " + IRTypeToGo(ret)
				body = "\t\treturn " + call + "\n"
			} else {
				body = "\t\t" + call + "\n"
			}
		default:
			result = " " + varType(r.Var)
			body = "\t\treturn c." + r.Var.Name + "\n"
		}
		fmt.Fprintf(b, "// %s answers a test's %s of #%s; for tests.\n", name, verbOf(r), r.ID)
		fmt.Fprintf(b, "func (m *Model) %s(%s)%s {\n\tif c := %s; c != nil {\n%s\t}\n", name, strings.Join(params, ", "), result, lookup, body)
		if result != "" {
			fmt.Fprintf(b, "\tvar zero%s\n\treturn zero\n", result)
		}
		b.WriteString("}\n\n")
	}
}

func verbOf(r codegen.InstanceRead) string {
	if r.Func != nil {
		return "call of " + r.Member
	}
	return "read of " + r.Member
}
