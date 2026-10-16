package android

import (
	"duckfam.us/sngl/codegen/lang/kotlin"
	"duckfam.us/sngl/ir"
)

// The Compose names this emitter writes, fully qualified. Each reaches the
// file through kotlin.KtIRContext.Name, which imports it explicitly and
// answers the spelling to write -- the simple name, or an alias where another
// import already brought that name into scope. Nothing reaches the file
// through a star import, so which declaration a name means does not move when
// a library adds one.
const (
	ktLayout = "androidx.compose.foundation.layout."
	ktM3     = "androidx.compose.material3."
	ktRT     = "androidx.compose.runtime."
	ktUI     = "androidx.compose.ui."
	ktNav    = "androidx.navigation."

	ktBackground = "androidx.compose.foundation.background"
	ktCanvas     = "androidx.compose.foundation.Canvas"
	ktCornerRect = "androidx.compose.foundation.shape.RoundedCornerShape"

	ktBox                = ktLayout + "Box"
	ktColumn             = ktLayout + "Column"
	ktAspectRatio        = ktLayout + "aspectRatio"
	ktFillMaxHeight      = ktLayout + "fillMaxHeight"
	ktFillMaxSize        = ktLayout + "fillMaxSize"
	ktFillMaxWidth       = ktLayout + "fillMaxWidth"
	ktHeight             = ktLayout + "height"
	ktPadding            = ktLayout + "padding"
	ktSafeDrawingPadding = ktLayout + "safeDrawingPadding"
	ktSize               = ktLayout + "size"
	ktWidth              = ktLayout + "width"

	ktButton                   = ktM3 + "Button"
	ktButtonDefaults           = ktM3 + "ButtonDefaults"
	ktDropdownMenu             = ktM3 + "DropdownMenu"
	ktDropdownMenuItem         = ktM3 + "DropdownMenuItem"
	ktExperimentalMaterial3Api = ktM3 + "ExperimentalMaterial3Api"
	ktMaterialTheme            = ktM3 + "MaterialTheme"
	ktOutlinedButton           = ktM3 + "OutlinedButton"
	ktText                     = ktM3 + "Text"

	ktComposable         = ktRT + "Composable"
	ktDerivedStateOf     = ktRT + "derivedStateOf"
	ktDisposableEffect   = ktRT + "DisposableEffect"
	ktLaunchedEffect     = ktRT + "LaunchedEffect"
	ktMutableStateListOf = ktRT + "mutableStateListOf"
	ktMutableStateOf     = ktRT + "mutableStateOf"
	ktRemember           = ktRT + "remember"
	ktToMutableStateList = ktRT + "toMutableStateList"

	ktModifier      = ktUI + "Modifier"
	ktAlpha         = ktUI + "draw.alpha"
	ktColor         = ktUI + "graphics.Color"
	ktTestTag       = ktUI + "platform.testTag"
	ktSemantics     = ktUI + "semantics.semantics"
	ktSemanticsKey  = ktUI + "semantics.SemanticsPropertyKey"
	ktLocalContext  = ktUI + "platform.LocalContext"
	ktTextStyle     = ktUI + "text.TextStyle"
	ktFontWeight    = ktUI + "text.font.FontWeight"
	ktFontStyle     = ktUI + "text.font.FontStyle"
	ktFontFamily    = ktUI + "text.font.FontFamily"
	ktTextAlign     = ktUI + "text.style.TextAlign"
	ktTextUnit      = ktUI + "unit.TextUnit"
	ktDp            = ktUI + "unit.dp"
	ktSp            = ktUI + "unit.sp"
	ktOffset        = ktUI + "geometry.Offset"
	ktGeomSize      = ktUI + "geometry.Size"
	ktPath          = ktUI + "graphics.Path"
	ktStroke        = ktUI + "graphics.drawscope.Stroke"
	ktDrawScope     = ktUI + "graphics.drawscope.DrawScope"
	ktDrawScale     = ktUI + "graphics.drawscope.scale"
	ktNativeCanvas  = ktUI + "graphics.nativeCanvas"
	ktAsImageBitmap = ktUI + "graphics.asImageBitmap"
	ktToArgb        = ktUI + "graphics.toArgb"
	ktIntOffset     = ktUI + "unit.IntOffset"
	ktIntSize       = ktUI + "unit.IntSize"

	ktSpanStyle      = ktUI + "text.SpanStyle"
	ktWithStyle      = ktUI + "text.withStyle"
	ktWithLink       = ktUI + "text.withLink"
	ktLinkAnnotation = ktUI + "text.LinkAnnotation"
	ktBuildAnnotated = ktUI + "text.buildAnnotatedString"
	ktTextDecoration = ktUI + "text.style.TextDecoration"
)

// composeColor is what the file calls Compose's Color: ComposeColor, beside
// the Color data class SNGL's own color type becomes, or an alias of that
// where a program's declaration holds the name.
func composeColor(kc *kotlin.KtIRContext) string {
	return kc.NameAs(ktColor, "ComposeColor")
}

// dp and sp are a number in Compose's units, through the extension property
// the file imports under whatever name it was given.
func dp(kc *kotlin.KtIRContext, v string) string { return v + "." + kc.Name(ktDp) }
func sp(kc *kotlin.KtIRContext, v string) string { return v + "." + kc.Name(ktSp) }

// stateDelegate imports what a `by` delegate to a State needs. getValue and
// setValue are operators Kotlin finds by name, so they are imported as they
// are rather than through Name, which may alias.
func stateDelegate(kc *kotlin.KtIRContext) {
	kc.RequireImport(ktRT + "getValue")
	kc.RequireImport(ktRT + "setValue")
}

// reserveDeclared tells the import set which names the program's own
// declarations take at the file's top level -- a component is a composable
// function, a struct a data class, an enum an enum class -- so that a Compose
// name the emitter imports for itself is aliased around them rather than
// capturing their references.
func reserveDeclared(kc *kotlin.KtIRContext, pkg *ir.Package) {
	for _, c := range pkg.Components {
		kc.Reserve(exportName(c.Name))
	}
	for _, sd := range pkg.Structs {
		kc.Reserve(exportName(sd.Name))
	}
	for _, ed := range pkg.Enums {
		kc.Reserve(kotlin.EnumName(ed))
	}
	for _, fn := range pkg.Funcs {
		if !fn.IsMethod() {
			kc.Reserve(fn.Name)
		}
	}
}
