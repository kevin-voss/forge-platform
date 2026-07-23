package forge.control.resource

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class FinalizersTest {
    @Test
    fun applyPatchAddsAndRemovesPreservingOrderUniqueness() {
        val current = JsonArray(
            listOf(JsonPrimitive("a.forge.dev/f1"), JsonPrimitive("a.forge.dev/f2")),
        )
        val patched = Finalizers.applyPatch(
            current,
            FinalizerPatchRequest(
                add = listOf("a.forge.dev/f3", "a.forge.dev/f1"),
                remove = listOf("a.forge.dev/f2"),
            ),
        )
        assertEquals(
            listOf("a.forge.dev/f1", "a.forge.dev/f3"),
            Finalizers.asStrings(patched),
        )
    }

    @Test
    fun emptyDetection() {
        assertTrue(Finalizers.isEmpty(JsonArray(emptyList())))
        assertTrue(!Finalizers.isEmpty(JsonArray(listOf(JsonPrimitive("x")))))
    }
}
