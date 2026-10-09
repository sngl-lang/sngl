package ir

// recordArgs names, per intrinsic, the parameter whose argument is a record
// the host reads by field name: sngl:i18n's translation arguments, one field
// per placeholder. The declaration takes it as a type parameter, so a call
// writes `{name=n}` and the checker holds it to a struct; every runtime takes
// a string-keyed map, which lower.RecordArgs makes of it.
var recordArgs = map[string]string{
	"i18n.tr":         "args",
	"i18n.trInline":   "args",
	"i18n.format":     "args",
	"i18n._translate": "args",
	"i18n._format":    "args",
}

// RecordArg returns the index of fn's parameter whose argument is a record
// read by field name, or -1 where fn takes none.
func RecordArg(fn *Func) int {
	if fn == nil {
		return -1
	}
	name, ok := recordArgs[fn.Intrinsic]
	if !ok {
		return -1
	}
	for i, p := range fn.Params {
		if p.Name == name {
			return i
		}
	}
	return -1
}
