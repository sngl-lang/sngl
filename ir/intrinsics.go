package ir

// IntrinsicDef describes a function that must be natively implemented by
// codegen backends. The IR only tracks signatures — implementations are
// registered by each language/platform via the codegen registry.
type IntrinsicDef struct {
	Name   string   // PascalCase identifier, e.g. "string.indexOf"
	Params []*Param // parameter signatures
	Return *Type    // return type
	// TypeParams names the type variables Params and Return are written
	// against, in the order a caller binds them. A collection intrinsic is
	// generic in its element type exactly as its lib declaration is
	// (`func list<T>.push(item T) list<T>`); spelling that here as dyn made
	// this a less-typed second record of the same signature, and put
	// `list<dyn>` into the IR of every pass that synthesizes a call from it.
	// Instantiate binds them.
	TypeParams []string
	// Purity, if non-zero, overrides the default PurityPure assumption.
	// Use this for intrinsics whose result depends on host state
	// (env vars, filesystem, time, etc.) or that have side effects.
	Purity Purity
	// MutatesReceiver marks an intrinsic whose first parameter (the receiver)
	// is mutated in place by the native implementation, even though the SNGL
	// signature returns a value. Reactivity treats a statement-level call as a
	// write to the receiver var, and backends emit an in-place mutation.
	MutatesReceiver bool
	// Pkg is the library package the declaration lives in, which is how a
	// caller asks about a group of intrinsics without matching id spellings.
	Pkg string
}

// Instantiate binds d's type parameters, in TypeParams order, and returns the
// resulting params and return type. Extra arguments are ignored; a parameter
// left unbound stays a type variable, which is what a caller that only needs
// the arity gets. Safe on a non-generic def: it returns Params and Return
// unchanged.
func (d IntrinsicDef) Instantiate(args ...*Type) ([]*Param, *Type) {
	if len(d.TypeParams) == 0 || len(args) == 0 {
		return d.Params, d.Return
	}
	bindings := make(map[string]*Type, len(d.TypeParams))
	for i, name := range d.TypeParams {
		if i < len(args) && args[i] != nil {
			bindings[name] = args[i]
		}
	}
	sig := (&FuncSig{Params: d.Params, Return: d.Return}).Substitute(bindings)
	return sig.Params, sig.Return
}

// The node operations: the protocol between the lowering passes that flatten a
// visual tree into imperative statements and the platforms that rebuild it.
//
// They are ids stamped on a synthesized Func rather than declarations in a
// library package, because no program calls them. Nothing in SNGL can name
// them, no language registers an emitter for them, and the only consumer is
// codegen.WalkLowered, which matches on the id and dispatches to a method of
// IntrinsicTranslator — a platform interface.
const (
	NodeOpCreateNode      = "CreateNode"
	NodeOpCreateComponent = "CreateComponent"
	NodeOpAppendChild     = "AppendChild"
	NodeOpRemoveChild     = "RemoveChild"
	NodeOpAttachHandler   = "AttachHandler"
)

// NodeOps is every node operation, for the passes that build a call per op.
var NodeOps = []string{
	NodeOpCreateNode,
	NodeOpCreateComponent,
	NodeOpAppendChild,
	NodeOpRemoveChild,
	NodeOpAttachHandler,
}

// IsI18nCall reports whether c targets an i18n stdlib entry point. The mark on
// the declaration answers for both shapes — the wrapper a program writes and
// the primitive left after NoContext and InlinePure collapse it — so "package
// uses i18n" is correct whichever passes have run, and stays correct when the
// program imports the package under an alias.
func IsI18nCall(c *Call) bool {
	if c == nil || c.Func == nil {
		return false
	}
	return IsI18nIntrinsic(c.Func.Intrinsic)
}

// IsI18nIntrinsic reports whether an intrinsic id is one sngl:i18n declares.
// Empty name returns false (most user funcs carry no Intrinsic tag).
//
// The declaring package, not the id's spelling: the ids all happen to start
// with "i18n." and matching that would be the compiler identifying a built-in
// by its name again. Pkg comes off the declaration.
func IsI18nIntrinsic(name string) bool {
	def := LookupIntrinsic(name)
	return def != nil && def.Pkg == I18nPkg
}

// I18nPkg is the package whose intrinsics IsI18nIntrinsic answers for.
const I18nPkg = "sngl:i18n"

// IsI18nPluralKey reports whether sel reads one of i18n's predeclared
// PluralKey constants — a CLDR category (zero, one, two, few, many, other).
// Plural-map literals carry these as bare Selects that survive lowering, so
// both usage detection and the JS/Kotlin backends have to recognize them.
//
// The select's *type* is what identifies it. Matching the operand's spelling
// instead read `i18n.one` and missed `t.one` under `import t "sngl:i18n"`,
// which then emitted the alias as a bare identifier.
func IsI18nPluralKey(sel *Select) bool {
	if sel == nil {
		return false
	}
	switch sel.Field {
	case "zero", "one", "two", "few", "many", "other":
	default:
		return false
	}
	return isPluralKeyType(sel.Type)
}

func isPluralKeyType(t *Type) bool {
	if t == nil || t.Kind != TypeStruct {
		return false
	}
	sd, ok := t.Decl.(*StructDef)
	return ok && sd.Name == pluralKeyTypeName
}

// The one name left: PluralKey is a lib/i18n declaration, not something a
// program spells, so it does not vary with how the import is written.
const pluralKeyTypeName = "PluralKey"
