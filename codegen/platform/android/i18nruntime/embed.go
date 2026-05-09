// Package i18nruntime embeds the Kotlin i18n runtime file for injection into
// generated Android projects. The file is a copy of pkg/kotlin/i18n/I18n.kt
// placed here so that //go:embed can reach it (embed paths must be within the
// package tree — ".." is not permitted).
package i18nruntime

import _ "embed"

//go:embed I18n.kt
var I18nKt string
