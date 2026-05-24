// Composite-build entry point for the SNGL Kotlin testagent runtime.
// Required by gradle's `includeBuild(...)` mechanism so the generated
// android test project can pull this module in as a project dependency
// rather than from a published maven repository.

rootProject.name = "testagent"
