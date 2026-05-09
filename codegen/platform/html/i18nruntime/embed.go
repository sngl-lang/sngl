// Package i18nruntime embeds the JS i18n runtime files for inlining into
// generated HTML bundles. The files are copies of pkg/js/i18n/{i18n,locale_currency}.js
// placed here so that //go:embed can reach them (embed paths must be
// within the package tree — ".." is not permitted).
package i18nruntime

import _ "embed"

//go:embed i18n.js
var I18nJS string

//go:embed locale_currency.js
var LocaleCurrencyJS string
