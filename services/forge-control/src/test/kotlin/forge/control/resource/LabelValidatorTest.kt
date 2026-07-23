package forge.control.resource

import forge.control.http.ApiException
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class LabelValidatorTest {
    @Test
    fun acceptsValidKeysAndValues() {
        LabelValidator.validate(
            labels = buildJsonObject {
                put("tier", JsonPrimitive("web"))
                put("app.kubernetes.io/name", JsonPrimitive("invoice"))
                put("env", JsonPrimitive(""))
            },
            annotations = buildJsonObject {
                put("forge.dev/note", JsonPrimitive("ok"))
            },
        )
    }

    @Test
    fun rejectsInvalidKeyNameSegment() {
        val err = assertFailsWith<ApiException.BadRequest> {
            LabelValidator.validateKey("Tier")
        }
        assertEquals("invalid_label", err.code)
    }

    @Test
    fun rejectsKeyEndingWithDash() {
        assertFailsWith<ApiException.BadRequest> {
            LabelValidator.validateKey("tier-")
        }
    }

    @Test
    fun rejectsOversizedPrefix() {
        val prefix = "a".repeat(254)
        val err = assertFailsWith<ApiException.BadRequest> {
            LabelValidator.validateKey("$prefix/name")
        }
        assertTrue(err.message!!.contains("prefix"))
    }

    @Test
    fun rejectsOversizedValue() {
        val err = assertFailsWith<ApiException.BadRequest> {
            LabelValidator.validateValue("v".repeat(64))
        }
        assertEquals("invalid_label", err.code)
    }

    @Test
    fun rejectsInvalidValueChars() {
        assertFailsWith<ApiException.BadRequest> {
            LabelValidator.validateValue("Bad Value")
        }
    }

    @Test
    fun acceptsSingleCharKeyAndValue() {
        LabelValidator.validateKey("a")
        LabelValidator.validateValue("b")
    }
}
