// Kotlin runtime for the SNGL i18n stdlib package.
//
// Mirrors pkg/go/i18n/i18n.go and pkg/js/i18n/i18n.js in API and ICU semantics.
// Differences from the Go runtime:
//   - No PluralKey struct — string keys throughout (same as JS).
//   - Locale-aware formatting via android.icu.* (Android platform APIs).
//   - Manifest loaded from assets/i18n.manifest.json via Context.
//
// Plural keys:
//   - Exact matches are encoded as "=N" strings.
//   - CLDR category keywords ("one", "other", etc.) are plain strings.
//   - android.icu.text.PluralRules.select() returns CLDR keyword strings directly.
//
// select() returns the matched case body raw — no ICU formatting applied
// (matches Go and JS behaviour for the direct Translator.select() method).

package us.duckfam.git.jonathan.sngl.i18n

import android.content.Context
import android.icu.text.DateFormat
import android.icu.text.NumberFormat
import android.icu.text.PluralRules
import org.json.JSONObject
import java.util.Date
import java.util.Locale

// Manifest maps ICU template key → locale → translated template.
typealias Manifest = Map<String, Map<String, String>>

class Translator(val manifest: Manifest, val locale: Locale) {
    private val currencyCode: String = localeToCurrency(locale)

    // Lookup `key` in manifest with locale-fallback (e.g. "en-US" → "en"),
    // falling back to inlinedTemplate then key itself.
    private fun lookup(key: String, inlinedTemplate: String): String {
        val entry = manifest[key] ?: return inlinedTemplate.ifEmpty { key }
        var loc = locale.toLanguageTag()
        while (loc.isNotEmpty()) {
            entry[loc]?.let { return it }
            val i = loc.lastIndexOf('-')
            if (i < 0) break
            loc = loc.substring(0, i)
        }
        return inlinedTemplate.ifEmpty { key }
    }

    fun tr(key: String, inlinedTemplate: String, args: Map<String, Any?>): String =
        formatICU(locale, currencyCode, lookup(key, inlinedTemplate), args)

    fun format(template: String, args: Map<String, Any?>): String =
        formatICU(locale, currencyCode, template, args)

    fun numberInt(n: Int, style: String): String =
        formatNumber(locale, currencyCode, n.toDouble(), style)

    fun numberFloat(n: Double, style: String): String =
        formatNumber(locale, currencyCode, n, style)

    fun date(d: Date, style: String): String =
        DateFormat.getDateInstance(dateStyleConst(style), locale).format(d)

    fun time(t: Date, style: String): String =
        DateFormat.getTimeInstance(dateStyleConst(style), locale).format(t)

    fun datetime(dt: Date, dateStyle: String, timeStyle: String): String =
        DateFormat.getDateTimeInstance(
            dateStyleConst(dateStyle), dateStyleConst(timeStyle), locale
        ).format(dt)

    // select returns the matched case body raw — no ICU format applied.
    // Matches Go and JS Translator.select() behaviour.
    fun select(value: String, cases: Map<String, String>): String =
        cases[value] ?: cases["other"] ?: ""

    fun plural(count: Int, forms: Map<String, String>): String =
        pluralImpl(count, forms, false)

    fun selectordinal(count: Int, forms: Map<String, String>): String =
        pluralImpl(count, forms, true)

    private fun pluralImpl(count: Int, forms: Map<String, String>, ordinal: Boolean): String {
        val args = mapOf<String, Any?>("#" to count, "n" to count)
        forms["=$count"]?.let { return formatICU(locale, currencyCode, it, args) }
        val type = if (ordinal) PluralRules.PluralType.ORDINAL else PluralRules.PluralType.CARDINAL
        val cat = PluralRules.forLocale(locale, type).select(count.toDouble())
        forms[cat]?.let { return formatICU(locale, currencyCode, it, args) }
        forms["other"]?.let { return formatICU(locale, currencyCode, it, args) }
        return ""
    }
}

// --- ICU template parser ---

private fun formatICU(locale: Locale, currency: String, tmpl: String, args: Map<String, Any?>): String {
    val out = StringBuilder()
    formatICUInto(out, locale, currency, tmpl, args, "")
    return out.toString()
}

private fun formatICUInto(
    out: StringBuilder,
    locale: Locale,
    currency: String,
    tmpl: String,
    args: Map<String, Any?>,
    current: String,
) {
    var i = 0
    while (i < tmpl.length) {
        val c = tmpl[i]
        if (c == '\'') {
            // Doubled '' → literal '
            if (i + 1 < tmpl.length && tmpl[i + 1] == '\'') {
                out.append('\''); i += 2; continue
            }
            val next = if (i + 1 < tmpl.length) tmpl[i + 1] else ' '
            if (next == '{' || next == '}' || next == '#' || next == '|') {
                // Quoted run — copy interior literally until closing '
                i++ // consume opening '
                while (i < tmpl.length) {
                    if (tmpl[i] == '\'') {
                        if (i + 1 < tmpl.length && tmpl[i + 1] == '\'') {
                            out.append('\''); i += 2; continue
                        }
                        i++; break
                    }
                    out.append(tmpl[i]); i++
                }
                continue
            }
            // Bare ' — literal
            out.append('\''); i++; continue
        }
        if (c == '#' && current.isNotEmpty()) {
            out.append(current); i++; continue
        }
        if (c == '{') {
            val r = splitPlaceholder(tmpl, i)
            if (r == null) {
                out.append(tmpl.substring(i)); return
            }
            renderPlaceholder(out, locale, currency, r.body, args, current)
            i = r.end; continue
        }
        out.append(c); i++
    }
}

private data class PhBody(val end: Int, val body: String)

private fun splitPlaceholder(tmpl: String, start: Int): PhBody? {
    if (start >= tmpl.length || tmpl[start] != '{') return null
    var depth = 0
    var i = start
    while (i < tmpl.length) {
        when (tmpl[i]) {
            '{' -> depth++
            '}' -> {
                depth--
                if (depth == 0) return PhBody(i + 1, tmpl.substring(start + 1, i))
            }
            '\'' -> {
                if (i + 1 < tmpl.length && tmpl[i + 1] == '\'') {
                    // Doubled '' — skip the second quote; outer loop advances past first.
                    i++
                } else {
                    val next = if (i + 1 < tmpl.length) tmpl[i + 1] else ' '
                    if (next == '{' || next == '}' || next == '#' || next == '|') {
                        // Skip ICU-quoted run so inner braces don't affect depth.
                        i++ // skip opening '
                        while (i < tmpl.length) {
                            if (tmpl[i] == '\'') {
                                if (i + 1 < tmpl.length && tmpl[i + 1] == '\'') {
                                    i += 2; continue
                                }
                                break
                            }
                            i++
                        }
                    }
                }
            }
        }
        i++
    }
    return null
}

private fun splitPlaceholderArgs(body: String): List<String> {
    val parts = mutableListOf<String>()
    var depth = 0
    var start = 0
    for (i in body.indices) {
        when (body[i]) {
            '{' -> depth++
            '}' -> depth--
            ',' -> if (depth == 0) {
                parts.add(body.substring(start, i))
                start = i + 1
            }
        }
    }
    parts.add(body.substring(start))
    return parts
}

private data class CasePair(val selector: String, val body: String)

private fun splitCases(raw: String): List<CasePair> {
    val pairs = mutableListOf<CasePair>()
    var i = 0
    while (i < raw.length) {
        while (i < raw.length && raw[i].isWhitespace()) i++
        if (i >= raw.length) break
        val selStart = i
        while (i < raw.length && raw[i] != '{') i++
        if (i >= raw.length) break
        val sel = raw.substring(selStart, i).trim()
        var depth = 0
        val bodyStart = i + 1
        while (i < raw.length) {
            if (raw[i] == '{') depth++
            else if (raw[i] == '}') {
                depth--
                if (depth == 0) {
                    pairs.add(CasePair(sel, raw.substring(bodyStart, i)))
                    i++; break
                }
            }
            i++
        }
    }
    return pairs
}

private fun renderPlaceholder(
    out: StringBuilder,
    locale: Locale,
    currency: String,
    body: String,
    args: Map<String, Any?>,
    current: String,
) {
    val parts = splitPlaceholderArgs(body)
    if (parts.isEmpty()) return
    val name = parts[0].trim()
    val v = args[name]
    if (parts.size == 1) {
        out.append(v?.toString() ?: ""); return
    }
    val typ = parts[1].trim()
    when (typ) {
        "plural", "selectordinal" ->
            renderPlural(out, locale, currency, v, parts.drop(2), typ == "selectordinal", args)
        "select" ->
            renderSelect(out, locale, currency, v, parts.drop(2), args)
        "number" -> {
            val style = if (parts.size >= 3) parts[2].trim() else "decimal"
            out.append(formatNumber(locale, currency, toDouble(v), style))
        }
        "date" -> {
            val style = if (parts.size >= 3) parts[2].trim() else "medium"
            (v as? Date)?.let {
                out.append(DateFormat.getDateInstance(dateStyleConst(style), locale).format(it))
            } ?: out.append(v?.toString() ?: "")
        }
        "time" -> {
            val style = if (parts.size >= 3) parts[2].trim() else "medium"
            (v as? Date)?.let {
                out.append(DateFormat.getTimeInstance(dateStyleConst(style), locale).format(it))
            } ?: out.append(v?.toString() ?: "")
        }
        "dateTime" -> {
            val style = if (parts.size >= 3) parts[2].trim() else "medium"
            (v as? Date)?.let {
                out.append(
                    DateFormat.getDateTimeInstance(
                        dateStyleConst(style), dateStyleConst(style), locale
                    ).format(it)
                )
            } ?: out.append(v?.toString() ?: "")
        }
        else -> out.append(v?.toString() ?: "")
    }
}

private fun renderPlural(
    out: StringBuilder,
    locale: Locale,
    currency: String,
    v: Any?,
    cases: List<String>,
    ordinal: Boolean,
    args: Map<String, Any?>,
) {
    val raw = cases.joinToString(",").trim()
    val pairs = splitCases(raw)
    val n = toInt(v)
    // Try =N exact match first.
    pairs.firstOrNull { it.selector.startsWith("=") && it.selector.substring(1) == n.toString() }
        ?.let { formatICUInto(out, locale, currency, it.body, args, n.toString()); return }
    // CLDR keyword.
    val type = if (ordinal) PluralRules.PluralType.ORDINAL else PluralRules.PluralType.CARDINAL
    val cat = PluralRules.forLocale(locale, type).select(n.toDouble())
    pairs.firstOrNull { it.selector == cat }
        ?.let { formatICUInto(out, locale, currency, it.body, args, n.toString()); return }
    // Fallback "other".
    pairs.firstOrNull { it.selector == "other" }
        ?.let { formatICUInto(out, locale, currency, it.body, args, n.toString()); return }
}

private fun renderSelect(
    out: StringBuilder,
    locale: Locale,
    currency: String,
    v: Any?,
    cases: List<String>,
    args: Map<String, Any?>,
) {
    val raw = cases.joinToString(",").trim()
    val pairs = splitCases(raw)
    val sel = v?.toString() ?: ""
    pairs.firstOrNull { it.selector == sel }
        ?.let { formatICUInto(out, locale, currency, it.body, args, ""); return }
    pairs.firstOrNull { it.selector == "other" }
        ?.let { formatICUInto(out, locale, currency, it.body, args, ""); return }
}

// --- Direct formatters ---

private fun formatNumber(locale: Locale, currency: String, n: Double, style: String): String {
    val nf = when (style) {
        "percent" -> NumberFormat.getPercentInstance(locale)
        "currency" -> NumberFormat.getCurrencyInstance(locale).also {
            it.currency = android.icu.util.Currency.getInstance(currency)
        }
        "scientific" -> NumberFormat.getScientificInstance(locale)
        else -> NumberFormat.getInstance(locale)
    }
    return nf.format(n)
}

private fun dateStyleConst(style: String): Int = when (style) {
    "short" -> DateFormat.SHORT
    "long"  -> DateFormat.LONG
    "full"  -> DateFormat.FULL
    else    -> DateFormat.MEDIUM
}

private fun localeToCurrency(locale: Locale): String {
    return try {
        java.util.Currency.getInstance(locale).currencyCode
    } catch (_: Exception) {
        "USD"
    }
}

private fun toInt(v: Any?): Int = when (v) {
    is Int    -> v
    is Long   -> v.toInt()
    is Double -> v.toInt()
    is Float  -> v.toInt()
    is String -> v.toIntOrNull() ?: 0
    else      -> 0
}

private fun toDouble(v: Any?): Double = when (v) {
    is Int    -> v.toDouble()
    is Long   -> v.toDouble()
    is Double -> v
    is Float  -> v.toDouble()
    is String -> v.toDoubleOrNull() ?: 0.0
    else      -> 0.0
}

// --- Module-level singleton ---

object I18n {
    @Volatile private var instance: Translator? = null

    @JvmStatic
    fun getTranslator(): Translator {
        instance?.let { return it }
        synchronized(this) {
            instance?.let { return it }
            val t = Translator(emptyMap(), Locale.getDefault())
            instance = t
            return t
        }
    }

    /**
     * Called from generated Application.onCreate to load assets/i18n.manifest.json.
     * Replaces any previously created translator with one backed by the manifest.
     */
    @JvmStatic
    fun init(ctx: Context) {
        val manifest = loadManifest(ctx)
        synchronized(this) {
            instance = Translator(manifest, Locale.getDefault())
        }
    }
}

private fun loadManifest(ctx: Context): Manifest {
    return try {
        val json = ctx.assets.open("i18n.manifest.json").bufferedReader().use { it.readText() }
        val root = JSONObject(json)
        val out = mutableMapOf<String, Map<String, String>>()
        for (key in root.keys()) {
            val entry = root.getJSONObject(key)
            val translations = entry.optJSONObject("translations") ?: continue
            val per = mutableMapOf<String, String>()
            for (loc in translations.keys()) per[loc] = translations.getString(loc)
            out[key] = per
        }
        out
    } catch (_: Exception) {
        emptyMap()
    }
}
