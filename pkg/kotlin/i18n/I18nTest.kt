// JUnit tests for the SNGL i18n Kotlin runtime.
//
// These tests require Robolectric so that android.icu.* APIs are available
// without a running Android emulator. They are NOT executed by `go tool verify`
// (no Gradle/Robolectric setup exists in this repo yet). They will run when
// Phase 6 wires up the android example project with a Gradle test task.
//
// To run manually once Gradle is available:
//   ./gradlew :app:testDebugUnitTest --tests "*.I18nTest"
//
// Or with Robolectric on the classpath:
//   kotlinc -cp robolectric.jar:android-all.jar I18n.kt I18nTest.kt -d out/
//   java -cp out/:robolectric.jar:android-all.jar org.junit.runner.JUnitCore I18nTest

package us.duckfam.git.jonathan.sngl.i18n

import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.Locale

@RunWith(RobolectricTestRunner::class)
class I18nTest {

    // --- Apostrophe quoting ---

    @Test fun `apostrophe doubled is literal`() {
        val t = Translator(emptyMap(), Locale.US)
        assertEquals("Don't do that", t.format("Don''t do that", emptyMap(), ""))
    }

    @Test fun `apostrophe quotes open brace`() {
        val t = Translator(emptyMap(), Locale.US)
        // '{ starts a quoted run that ends at the next unescaped '
        assertEquals("Use {literal}", t.format("Use '{'literal'}'", emptyMap(), ""))
    }

    @Test fun `apostrophe quotes hash`() {
        val t = Translator(emptyMap(), Locale.US)
        // '#' inside a plural body should be treated as a literal '#' when quoted
        assertEquals("100% done", t.format("'#'00% done", emptyMap(), ""))
    }

    // --- Simple substitution ---

    @Test fun `simple substitution`() {
        val t = Translator(emptyMap(), Locale.US)
        assertEquals("Hello, Alice!", t.format("Hello, {name}!", mapOf("name" to "Alice"), ""))
    }

    @Test fun `missing arg renders empty`() {
        val t = Translator(emptyMap(), Locale.US)
        assertEquals("Hello, !", t.format("Hello, {name}!", emptyMap(), ""))
    }

    // --- Plural (en cardinal) ---

    @Test fun `plural en cardinal exact zero`() {
        val t = Translator(emptyMap(), Locale.US)
        val tmpl = "{n, plural, =0{none} one{# item} other{# items}}"
        assertEquals("none", t.format(tmpl, mapOf("n" to 0), ""))
    }

    @Test fun `plural en cardinal one`() {
        val t = Translator(emptyMap(), Locale.US)
        val tmpl = "{n, plural, =0{none} one{# item} other{# items}}"
        assertEquals("1 item", t.format(tmpl, mapOf("n" to 1), ""))
    }

    @Test fun `plural en cardinal other`() {
        val t = Translator(emptyMap(), Locale.US)
        val tmpl = "{n, plural, =0{none} one{# item} other{# items}}"
        assertEquals("5 items", t.format(tmpl, mapOf("n" to 5), ""))
    }

    // --- Select ---

    @Test fun `select dispatch by value`() {
        val t = Translator(emptyMap(), Locale.US)
        val tmpl = "{g, select, female{She} male{He} other{They}}"
        assertEquals("She", t.format(tmpl, mapOf("g" to "female"), ""))
        assertEquals("He",  t.format(tmpl, mapOf("g" to "male"), ""))
    }

    @Test fun `select falls back to other`() {
        val t = Translator(emptyMap(), Locale.US)
        val tmpl = "{g, select, female{She} male{He} other{They}}"
        assertEquals("They", t.format(tmpl, mapOf("g" to "x"), ""))
    }

    // --- Direct Translator.select() — returns raw body, no ICU formatting ---

    @Test fun `direct select returns raw case`() {
        val t = Translator(emptyMap(), Locale.US)
        assertEquals("Y", t.select("yes", mapOf("yes" to "Y", "other" to "?"), ""))
        assertEquals("?", t.select("nope", mapOf("yes" to "Y", "other" to "?"), ""))
    }

    @Test fun `direct select returns empty when no match and no other`() {
        val t = Translator(emptyMap(), Locale.US)
        assertEquals("", t.select("missing", mapOf("yes" to "Y"), ""))
    }

    // --- Direct Translator.plural() with string keys ---

    @Test fun `direct plural string keys exact zero`() {
        val t = Translator(emptyMap(), Locale.US)
        val forms = mapOf("=0" to "none", "one" to "{n} item", "other" to "{n} items")
        assertEquals("none", t.plural(0, forms, ""))
    }

    @Test fun `direct plural string keys one`() {
        val t = Translator(emptyMap(), Locale.US)
        val forms = mapOf("=0" to "none", "one" to "{n} item", "other" to "{n} items")
        assertEquals("1 item", t.plural(1, forms, ""))
    }

    @Test fun `direct plural string keys other`() {
        val t = Translator(emptyMap(), Locale.US)
        val forms = mapOf("=0" to "none", "one" to "{n} item", "other" to "{n} items")
        assertEquals("7 items", t.plural(7, forms, ""))
    }

    // --- getTranslator singleton ---

    @Test fun `getTranslator returns same instance`() {
        val a = I18n.getTranslator()
        val b = I18n.getTranslator()
        assert(a === b) { "getTranslator should return the same instance" }
    }
}
