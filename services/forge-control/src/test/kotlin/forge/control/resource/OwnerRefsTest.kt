package forge.control.resource

import forge.control.http.ApiException
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class OwnerRefsTest {
    private val now = Instant.parse("2026-07-23T12:00:00Z")

    @Test
    fun sameOrWiderScopeAcceptsClusterOwnerForEnvironmentSubject() {
        val owner = row(id = "own_1", project = null, environment = null)
        val subject = row(id = "sub_1", project = "p", environment = "e")
        assertTrue(OwnerRefs.isSameOrWiderScope(owner, subject))
    }

    @Test
    fun sameOrWiderScopeRejectsEnvironmentOwnerForDifferentEnvironment() {
        val owner = row(id = "own_1", project = "p", environment = "prod")
        val subject = row(id = "sub_1", project = "p", environment = "dev")
        assertFalse(OwnerRefs.isSameOrWiderScope(owner, subject))
    }

    @Test
    fun cycleThroughOwnerChainIsRejected() {
        val a = row(id = "a", ownerRefs = refs("b", "Widget"))
        val b = row(id = "b", ownerRefs = refs("a", "Widget"))
        val byId = mapOf("a" to a, "b" to b)
        val ex = assertFailsWith<ApiException.BadRequest> {
            OwnerRefs.validate(b, refs("a", "Widget")) { byId[it] }
        }
        assertEquals("owner_reference_cycle", ex.code)
    }

    @Test
    fun selfOwnerReferenceIsRejected() {
        val subject = row(id = "self")
        val ex = assertFailsWith<ApiException.BadRequest> {
            OwnerRefs.validate(subject, refs("self", "Widget")) { null }
        }
        assertEquals("owner_reference_cycle", ex.code)
    }

    @Test
    fun missingOwnerIsRejected() {
        val subject = row(id = "child")
        val ex = assertFailsWith<ApiException.BadRequest> {
            OwnerRefs.validate(subject, refs("missing", "Widget")) { null }
        }
        assertEquals("invalid_owner_reference", ex.code)
    }

    @Test
    fun validOwnerPasses() {
        val owner = row(id = "parent", project = "p", environment = "e")
        val subject = row(id = "child", project = "p", environment = "e")
        OwnerRefs.validate(subject, refs("parent", "Widget")) { id ->
            if (id == "parent") owner else null
        }
    }

    private fun refs(id: String, kind: String): JsonArray =
        buildJsonArray {
            add(
                buildJsonObject {
                    put("kind", JsonPrimitive(kind))
                    put("id", JsonPrimitive(id))
                    put("controller", JsonPrimitive(true))
                },
            )
        }

    private fun row(
        id: String,
        project: String? = "invoice-platform",
        environment: String? = "production",
        ownerRefs: JsonArray = JsonArray(emptyList()),
    ): ResourceRow =
        ResourceRow(
            id = id,
            kind = "Widget",
            apiVersion = "forge.dev/v1",
            organization = "default",
            project = project,
            environment = environment,
            name = id,
            generation = 1,
            resourceVersion = 1,
            labels = kotlinx.serialization.json.JsonObject(emptyMap()),
            annotations = kotlinx.serialization.json.JsonObject(emptyMap()),
            spec = kotlinx.serialization.json.JsonObject(emptyMap()),
            status = kotlinx.serialization.json.JsonObject(emptyMap()),
            ownerRefs = ownerRefs,
            finalizers = JsonArray(emptyList()),
            createdAt = now,
            updatedAt = now,
        )
}
