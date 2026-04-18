package ir

// IntrinsicDef describes a function that must be natively implemented by every
// codegen backend. The canonical list lives in Intrinsics.
type IntrinsicDef struct {
	Name   string       // PascalCase identifier, e.g. "StrIndexOf"
	Params []*Param     // parameter signatures
	Return *Type        // return type
	Native []NativeImpl // per-language native implementations
}

// Intrinsics is the canonical list of all intrinsic functions.
// Every codegen backend must provide a native implementation for each entry.
var Intrinsics = []IntrinsicDef{
	// --- string ---
	{
		Name:   "StrLength",
		Params: []*Param{{Name: "s", Type: TypString}},
		Return: TypInt,
		Native: []NativeImpl{
			{Lang: "js", Ident: ".length"},
			{Lang: "go", ImportPath: "", Ident: "len"},
			{Lang: "kotlin", Ident: ".length"},
		},
	},
	{
		Name:   "StrIndexOf",
		Params: []*Param{{Name: "s", Type: TypString}, {Name: "sub", Type: TypString}},
		Return: TypInt,
		Native: []NativeImpl{
			{Lang: "js", Ident: ".indexOf"},
			{Lang: "go", ImportPath: "strings", Ident: "strings.Index"},
			{Lang: "kotlin", Ident: ".indexOf"},
		},
	},
	{
		Name:   "StrSubstring",
		Params: []*Param{{Name: "s", Type: TypString}, {Name: "start", Type: TypInt}, {Name: "end", Type: TypInt}},
		Return: TypString,
		Native: []NativeImpl{
			{Lang: "js", Ident: ".substring"},
			{Lang: "go", Ident: "slice"},
			{Lang: "kotlin", Ident: ".substring"},
		},
	},
	{
		Name:   "StrUpper",
		Params: []*Param{{Name: "s", Type: TypString}},
		Return: TypString,
		Native: []NativeImpl{
			{Lang: "js", Ident: ".toUpperCase"},
			{Lang: "go", ImportPath: "strings", Ident: "strings.ToUpper"},
			{Lang: "kotlin", Ident: ".uppercase"},
		},
	},
	{
		Name:   "StrLower",
		Params: []*Param{{Name: "s", Type: TypString}},
		Return: TypString,
		Native: []NativeImpl{
			{Lang: "js", Ident: ".toLowerCase"},
			{Lang: "go", ImportPath: "strings", Ident: "strings.ToLower"},
			{Lang: "kotlin", Ident: ".lowercase"},
		},
	},
	{
		Name:   "StrTrim",
		Params: []*Param{{Name: "s", Type: TypString}},
		Return: TypString,
		Native: []NativeImpl{
			{Lang: "js", Ident: ".trim"},
			{Lang: "go", ImportPath: "strings", Ident: "strings.TrimSpace"},
			{Lang: "kotlin", Ident: ".trim"},
		},
	},
	{
		Name:   "StrReplace",
		Params: []*Param{{Name: "s", Type: TypString}, {Name: "old", Type: TypString}, {Name: "new", Type: TypString}},
		Return: TypString,
		Native: []NativeImpl{
			{Lang: "js", Ident: ".replaceAll"},
			{Lang: "go", ImportPath: "strings", Ident: "strings.ReplaceAll"},
			{Lang: "kotlin", Ident: ".replace"},
		},
	},
	{
		Name:   "StrSplit",
		Params: []*Param{{Name: "s", Type: TypString}, {Name: "sep", Type: TypString}},
		Return: ListOf(TypString),
		Native: []NativeImpl{
			{Lang: "js", Ident: ".split"},
			{Lang: "go", ImportPath: "strings", Ident: "strings.Split"},
			{Lang: "kotlin", Ident: ".split"},
		},
	},

	// --- float math ---
	{
		Name:   "MathFloor",
		Params: []*Param{{Name: "x", Type: TypFloat}},
		Return: TypInt,
		Native: []NativeImpl{
			{Lang: "js", Ident: "Math.floor"},
			{Lang: "go", ImportPath: "math", Ident: "math.Floor"},
			{Lang: "kotlin", ImportPath: "kotlin.math", Ident: "kotlin.math.floor"},
		},
	},
	{
		Name:   "MathCeil",
		Params: []*Param{{Name: "x", Type: TypFloat}},
		Return: TypInt,
		Native: []NativeImpl{
			{Lang: "js", Ident: "Math.ceil"},
			{Lang: "go", ImportPath: "math", Ident: "math.Ceil"},
			{Lang: "kotlin", ImportPath: "kotlin.math", Ident: "kotlin.math.ceil"},
		},
	},
	{
		Name:   "MathRound",
		Params: []*Param{{Name: "x", Type: TypFloat}},
		Return: TypInt,
		Native: []NativeImpl{
			{Lang: "js", Ident: "Math.round"},
			{Lang: "go", ImportPath: "math", Ident: "math.Round"},
			{Lang: "kotlin", ImportPath: "kotlin.math", Ident: "kotlin.math.round"},
		},
	},
	{
		Name:   "MathPow",
		Params: []*Param{{Name: "base", Type: TypFloat}, {Name: "exp", Type: TypFloat}},
		Return: TypFloat,
		Native: []NativeImpl{
			{Lang: "js", Ident: "Math.pow"},
			{Lang: "go", ImportPath: "math", Ident: "math.Pow"},
			{Lang: "kotlin", Ident: ".pow"},
		},
	},
	{
		Name:   "MathSqrt",
		Params: []*Param{{Name: "x", Type: TypFloat}},
		Return: TypFloat,
		Native: []NativeImpl{
			{Lang: "js", Ident: "Math.sqrt"},
			{Lang: "go", ImportPath: "math", Ident: "math.Sqrt"},
			{Lang: "kotlin", ImportPath: "kotlin.math", Ident: "kotlin.math.sqrt"},
		},
	},
	{
		Name:   "MathSin",
		Params: []*Param{{Name: "x", Type: TypFloat}},
		Return: TypFloat,
		Native: []NativeImpl{
			{Lang: "js", Ident: "Math.sin"},
			{Lang: "go", ImportPath: "math", Ident: "math.Sin"},
			{Lang: "kotlin", ImportPath: "kotlin.math", Ident: "kotlin.math.sin"},
		},
	},
	{
		Name:   "MathCos",
		Params: []*Param{{Name: "x", Type: TypFloat}},
		Return: TypFloat,
		Native: []NativeImpl{
			{Lang: "js", Ident: "Math.cos"},
			{Lang: "go", ImportPath: "math", Ident: "math.Cos"},
			{Lang: "kotlin", ImportPath: "kotlin.math", Ident: "kotlin.math.cos"},
		},
	},
	{
		Name:   "MathTan",
		Params: []*Param{{Name: "x", Type: TypFloat}},
		Return: TypFloat,
		Native: []NativeImpl{
			{Lang: "js", Ident: "Math.tan"},
			{Lang: "go", ImportPath: "math", Ident: "math.Tan"},
			{Lang: "kotlin", ImportPath: "kotlin.math", Ident: "kotlin.math.tan"},
		},
	},
	{
		Name:   "MathAsin",
		Params: []*Param{{Name: "x", Type: TypFloat}},
		Return: TypFloat,
		Native: []NativeImpl{
			{Lang: "js", Ident: "Math.asin"},
			{Lang: "go", ImportPath: "math", Ident: "math.Asin"},
			{Lang: "kotlin", ImportPath: "kotlin.math", Ident: "kotlin.math.asin"},
		},
	},
	{
		Name:   "MathAcos",
		Params: []*Param{{Name: "x", Type: TypFloat}},
		Return: TypFloat,
		Native: []NativeImpl{
			{Lang: "js", Ident: "Math.acos"},
			{Lang: "go", ImportPath: "math", Ident: "math.Acos"},
			{Lang: "kotlin", ImportPath: "kotlin.math", Ident: "kotlin.math.acos"},
		},
	},
	{
		Name:   "MathAtan",
		Params: []*Param{{Name: "x", Type: TypFloat}},
		Return: TypFloat,
		Native: []NativeImpl{
			{Lang: "js", Ident: "Math.atan"},
			{Lang: "go", ImportPath: "math", Ident: "math.Atan"},
			{Lang: "kotlin", ImportPath: "kotlin.math", Ident: "kotlin.math.atan"},
		},
	},
	{
		Name:   "MathAtan2",
		Params: []*Param{{Name: "y", Type: TypFloat}, {Name: "x", Type: TypFloat}},
		Return: TypFloat,
		Native: []NativeImpl{
			{Lang: "js", Ident: "Math.atan2"},
			{Lang: "go", ImportPath: "math", Ident: "math.Atan2"},
			{Lang: "kotlin", ImportPath: "kotlin.math", Ident: "kotlin.math.atan2"},
		},
	},

	// --- list ---
	{
		Name:   "ListLength",
		Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}},
		Return: TypInt,
		Native: []NativeImpl{
			{Lang: "js", Ident: ".length"},
			{Lang: "go", Ident: "len"},
			{Lang: "kotlin", Ident: ".size"},
		},
	},
	{
		Name:   "ListPush",
		Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "item", Type: TypDyn}},
		Return: ListOf(TypDyn),
		Native: []NativeImpl{
			{Lang: "js", Ident: ".concat"},
			{Lang: "go", Ident: "append"},
			{Lang: "kotlin", Ident: ".plus"},
		},
	},
	{
		Name:   "ListRemove",
		Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "index", Type: TypInt}},
		Return: ListOf(TypDyn),
		Native: []NativeImpl{
			{Lang: "js", Ident: ".splice"},
			{Lang: "go", Ident: "slices.Delete"},
			{Lang: "kotlin", Ident: ".removeAt"},
		},
	},
	{
		Name:   "ListIndexOf",
		Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "item", Type: TypDyn}},
		Return: TypInt,
		Native: []NativeImpl{
			{Lang: "js", Ident: ".indexOf"},
			{Lang: "go", Ident: "slices.Index"},
			{Lang: "kotlin", Ident: ".indexOf"},
		},
	},
	{
		Name:   "ListJoin",
		Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "sep", Type: TypString}},
		Return: TypString,
		Native: []NativeImpl{
			{Lang: "js", Ident: ".join"},
			{Lang: "go", ImportPath: "strings", Ident: "strings.Join"},
			{Lang: "kotlin", Ident: ".joinToString"},
		},
	},
	{
		Name:   "ListReverse",
		Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}},
		Return: ListOf(TypDyn),
		Native: []NativeImpl{
			{Lang: "js", Ident: ".reverse"},
			{Lang: "go", ImportPath: "slices", Ident: "slices.Reverse"},
			{Lang: "kotlin", Ident: ".reversed"},
		},
	},
	{
		Name:   "ListSlice",
		Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "start", Type: TypInt}, {Name: "end", Type: TypInt}},
		Return: ListOf(TypDyn),
		Native: []NativeImpl{
			{Lang: "js", Ident: ".slice"},
			{Lang: "go", Ident: "slice"},
			{Lang: "kotlin", Ident: ".subList"},
		},
	},
	{
		Name:   "ListFilter",
		Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "pred", Type: TypDyn}},
		Return: ListOf(TypDyn),
		Native: []NativeImpl{
			{Lang: "js", Ident: ".filter"},
			{Lang: "go", Ident: "filter"},
			{Lang: "kotlin", Ident: ".filter"},
		},
	},
	{
		Name:   "ListMap",
		Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "fn", Type: TypDyn}},
		Return: ListOf(TypDyn),
		Native: []NativeImpl{
			{Lang: "js", Ident: ".map"},
			{Lang: "go", Ident: "mapSlice"},
			{Lang: "kotlin", Ident: ".map"},
		},
	},

	// --- color ---
	{
		Name:   "ColorHex",
		Params: []*Param{{Name: "c", Type: TypDyn}}, // Color struct, use Dyn since struct is in stdlib
		Return: TypString,
		Native: []NativeImpl{
			{Lang: "js", Ident: "colorHex"},
			{Lang: "go", ImportPath: "fmt", Ident: "fmt.Sprintf"},
			{Lang: "kotlin", Ident: "String.format"},
		},
	},

	// --- Alert ---
	{
		Name:   "AlertToast",
		Params: []*Param{{Name: "message", Type: TypString}, {Name: "variant", Type: TypString}},
		Return: TypInt,
		Native: []NativeImpl{
			{Lang: "js", Ident: "alert.toast"},
			{Lang: "go", ImportPath: "fmt", Ident: "fmt.Println"},
			{Lang: "kotlin", Ident: "Toast.makeText"},
		},
	},
	{
		Name:   "AlertInfo",
		Params: []*Param{{Name: "message", Type: TypString}},
		Return: TypInt,
		Native: []NativeImpl{
			{Lang: "js", Ident: "alert"},
			{Lang: "go", ImportPath: "fmt", Ident: "fmt.Println"},
			{Lang: "kotlin", Ident: "AlertDialog"},
		},
	},
	{
		Name:   "AlertWarn",
		Params: []*Param{{Name: "message", Type: TypString}},
		Return: TypInt,
		Native: []NativeImpl{
			{Lang: "js", Ident: "alert"},
			{Lang: "go", ImportPath: "fmt", Ident: "fmt.Println"},
			{Lang: "kotlin", Ident: "AlertDialog"},
		},
	},
	{
		Name:   "AlertError",
		Params: []*Param{{Name: "message", Type: TypString}},
		Return: TypInt,
		Native: []NativeImpl{
			{Lang: "js", Ident: "alert"},
			{Lang: "go", ImportPath: "fmt", Ident: "fmt.Println"},
			{Lang: "kotlin", Ident: "AlertDialog"},
		},
	},
	{
		Name:   "AlertConfirm",
		Params: []*Param{{Name: "message", Type: TypString}},
		Return: TypBool,
		Native: []NativeImpl{
			{Lang: "js", Ident: "confirm"},
			{Lang: "go", Ident: "true"},
			{Lang: "kotlin", Ident: "true"},
		},
	},

	// --- File ---
	{
		Name:   "FilePick",
		Params: []*Param{},
		Return: TypString,
		Native: []NativeImpl{
			{Lang: "js", Ident: "prompt"},
			{Lang: "go", Ident: "\"\""},
			{Lang: "kotlin", Ident: "\"\""},
		},
	},
	{
		Name:   "FilePickFolder",
		Params: []*Param{},
		Return: TypString,
		Native: []NativeImpl{
			{Lang: "js", Ident: "prompt"},
			{Lang: "go", Ident: "\"\""},
			{Lang: "kotlin", Ident: "\"\""},
		},
	},

	// --- regex ---
	{
		Name:   "RegexMatches",
		Params: []*Param{{Name: "re", Type: TypRegex}, {Name: "s", Type: TypString}},
		Return: TypBool,
		Native: []NativeImpl{
			{Lang: "js", Ident: ".test"},
			{Lang: "go", Ident: ".MatchString"},
			{Lang: "kotlin", Ident: ".containsMatchIn"},
		},
	},
	{
		Name:   "RegexFind",
		Params: []*Param{{Name: "re", Type: TypRegex}, {Name: "s", Type: TypString}},
		Return: TypString,
		Native: []NativeImpl{
			{Lang: "js", Ident: ".exec"},
			{Lang: "go", Ident: ".FindString"},
			{Lang: "kotlin", Ident: ".find"},
		},
	},
}

// LookupIntrinsic returns the intrinsic definition for the given name, or nil.
func LookupIntrinsic(name string) *IntrinsicDef {
	for i := range Intrinsics {
		if Intrinsics[i].Name == name {
			return &Intrinsics[i]
		}
	}
	return nil
}

// IntrinsicNames returns the names of all registered intrinsics.
func IntrinsicNames() []string {
	names := make([]string, len(Intrinsics))
	for i, d := range Intrinsics {
		names[i] = d.Name
	}
	return names
}
