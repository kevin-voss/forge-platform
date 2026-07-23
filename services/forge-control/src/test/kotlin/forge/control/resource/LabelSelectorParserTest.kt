package forge.control.resource

import forge.control.http.ApiException
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertTrue

class LabelSelectorParserTest {
    @Test
    fun parsesEquality() {
        val sel = LabelSelectorParser.parse("tier=web")
        assertEquals(1, sel.terms.size)
        val term = assertIs<LabelSelectorTerm.Equals>(sel.terms.single())
        assertEquals("tier", term.key)
        assertEquals("web", term.value)
    }

    @Test
    fun parsesInequality() {
        val term = assertIs<LabelSelectorTerm.NotEquals>(
            LabelSelectorParser.parse("env!=staging").terms.single(),
        )
        assertEquals("env", term.key)
        assertEquals("staging", term.value)
    }

    @Test
    fun parsesInAndNotIn() {
        val inn = assertIs<LabelSelectorTerm.In>(
            LabelSelectorParser.parse("tier in (web,api)").terms.single(),
        )
        assertEquals(listOf("web", "api"), inn.values)

        val notIn = assertIs<LabelSelectorTerm.NotIn>(
            LabelSelectorParser.parse("env notin (staging,dev)").terms.single(),
        )
        assertEquals(listOf("staging", "dev"), notIn.values)
    }

    @Test
    fun parsesExistenceAndNegatedExistence() {
        assertIs<LabelSelectorTerm.Exists>(LabelSelectorParser.parse("canary").terms.single())
        val missing = assertIs<LabelSelectorTerm.DoesNotExist>(
            LabelSelectorParser.parse("!canary").terms.single(),
        )
        assertEquals("canary", missing.key)
    }

    @Test
    fun parsesCombinedAndTerms() {
        val sel = LabelSelectorParser.parse("tier=web,env!=staging,region in (eu,us),!cache")
        assertEquals(4, sel.terms.size)
        assertIs<LabelSelectorTerm.Equals>(sel.terms[0])
        assertIs<LabelSelectorTerm.NotEquals>(sel.terms[1])
        assertIs<LabelSelectorTerm.In>(sel.terms[2])
        assertIs<LabelSelectorTerm.DoesNotExist>(sel.terms[3])
    }

    @Test
    fun emptyOrBlankYieldsNoTerms() {
        assertTrue(LabelSelectorParser.parse(null).terms.isEmpty())
        assertTrue(LabelSelectorParser.parse("  ").terms.isEmpty())
    }

    @Test
    fun malformedRaisesWithOffendingTerm() {
        val err = assertFailsWith<ApiException.BadRequest> {
            LabelSelectorParser.parse("tier=web,!!!")
        }
        assertEquals("invalid_label_selector", err.code)
        assertEquals("!!!", err.details?.get("term"))
    }

    @Test
    fun malformedSetRaises() {
        val err = assertFailsWith<ApiException.BadRequest> {
            LabelSelectorParser.parse("tier in web,api")
        }
        assertEquals("invalid_label_selector", err.code)
    }
}
