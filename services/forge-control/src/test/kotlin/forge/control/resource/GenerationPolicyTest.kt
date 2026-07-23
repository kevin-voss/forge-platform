package forge.control.resource

import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class GenerationPolicyTest {
    @Test
    fun identicalSpecIncludingKeyOrderDifferencesDoesNotBump() {
        val a = buildJsonObject {
            put("size", JsonPrimitive("large"))
            put("color", JsonPrimitive("blue"))
        }
        val b = buildJsonObject {
            put("color", JsonPrimitive("blue"))
            put("size", JsonPrimitive("large"))
        }
        assertFalse(GenerationPolicy.shouldBumpGeneration(a, b))
        assertEquals(GenerationPolicy.canonicalize(a), GenerationPolicy.canonicalize(b))
    }

    @Test
    fun changedSpecBumps() {
        val previous = buildJsonObject { put("size", JsonPrimitive("large")) }
        val next = buildJsonObject { put("size", JsonPrimitive("small")) }
        assertTrue(GenerationPolicy.shouldBumpGeneration(previous, next))
    }

    @Test
    fun nestedKeyOrderDifferencesDoNotBump() {
        val a = buildJsonObject {
            put(
                "resources",
                buildJsonObject {
                    put("cpu", JsonPrimitive("100m"))
                    put("memory", JsonPrimitive("128Mi"))
                },
            )
        }
        val b = buildJsonObject {
            put(
                "resources",
                buildJsonObject {
                    put("memory", JsonPrimitive("128Mi"))
                    put("cpu", JsonPrimitive("100m"))
                },
            )
        }
        assertFalse(GenerationPolicy.shouldBumpGeneration(a, b))
    }
}
