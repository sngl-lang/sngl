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
)

// baselineImports are imported ahead of everything the body names, so each
// keeps its simple name: what `chain` and `args` write is Kotlin as text --
// `fillMaxSize()`, `MaterialTheme.shapes.medium` -- which no Name call can
// spell, and it reaches only what the file imports under the name it wrote.
var baselineImports = []string{
	"androidx.compose.foundation.background",
	"androidx.compose.foundation.clickable",
	"androidx.compose.foundation.rememberScrollState",
	"androidx.compose.foundation.verticalScroll",
	ktFillMaxHeight,
	ktFillMaxSize,
	ktFillMaxWidth,
	ktMaterialTheme,
	// Every screen is a composable, and a slot's type spells the annotation
	// where no context is at hand.
	ktComposable,
	"androidx.compose.material3.pulltorefresh.PullToRefreshBox",
	"androidx.compose.ui.Alignment",
	"androidx.compose.ui.Modifier",
	"androidx.compose.ui.platform.testTag",
	"androidx.compose.ui.draw.alpha",
	"androidx.compose.ui.draw.clip",
	// Aliased to ComposeColor so the SNGL stdlib `color` struct (emitted as
	// a `Color` data class for Canvas2D) doesn't collide with the framework
	// graphics Color. All framework-color uses say ComposeColor.
	"androidx.compose.ui.graphics.Color as ComposeColor",
	"androidx.compose.ui.text.TextStyle",
	"androidx.compose.ui.text.font.FontWeight",
	"androidx.compose.ui.text.style.TextAlign",
	"androidx.compose.ui.unit.dp",
	"androidx.compose.ui.unit.sp",
	"androidx.compose.ui.window.Dialog",
}

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
