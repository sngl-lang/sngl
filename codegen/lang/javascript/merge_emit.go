package javascript

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ir"
)

// EmitMergeFuncs returns JS source for one __merge_<Struct> helper per struct.
// JS objects carry only their set fields; "present" is `!= null` (covers null
// and undefined). A spread copies base, then overwrites each field the
// override actually provides.
func EmitMergeFuncs(structs []*ir.StructDef) string {
	var b strings.Builder
	for _, sd := range structs {
		fmt.Fprintf(&b, "function __merge_%s(base, ov) {\n", sd.Name)
		b.WriteString("  const r = { ...base };\n")
		for _, f := range sd.Fields {
			fmt.Fprintf(&b, "  if (ov.%s != null) r.%s = ov.%s;\n", f.Name, f.Name, f.Name)
		}
		b.WriteString("  return r;\n}\n\n")
	}
	return b.String()
}
