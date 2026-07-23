package forge.control.resource

import forge.control.resource.http.ListEnvelope
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import org.junit.jupiter.api.Assumptions.assumeTrue

class ResourceOpenApiContractTest {
    private fun openApiYaml(): String? {
        val root = System.getenv("FORGE_ROOT")?.let { Path.of(it) }
            ?: Path.of("").toAbsolutePath().let { cwd ->
                generateSequence(cwd) { it.parent }.firstOrNull { Files.exists(it.resolve("contracts")) }
            }
            ?: return null
        val path = root.resolve("contracts/openapi/forge-control.openapi.yaml")
        if (!Files.exists(path)) return null
        return Files.readString(path)
    }

    @Test
    fun openApiDeclaresGenericResourcePathsAndSchemas() {
        val yaml = openApiYaml()
        assumeTrue(yaml != null, "contracts/ not available in this build context")
        assertTrue(yaml!!.contains("/v1/projects/{project}/environments/{environment}/{plural}"))
        assertTrue(yaml.contains("/v1/projects/{project}/environments/{environment}/{plural}/{name}"))
        assertTrue(yaml.contains("/v1/projects/{project}/environments/{environment}/{plural}/{name}/status"))
        assertTrue(yaml.contains("ResourceEnvelope:"))
        assertTrue(yaml.contains("Condition:"))
        assertTrue(yaml.contains("resourceVersion"))
        assertTrue(yaml.contains("resource_version_conflict") || yaml.contains("ResourceError"))
        assertTrue(yaml.contains("x-create-resource") || yaml.contains("createResource"))
        assertTrue(yaml.contains("x-list-resources") || yaml.contains("listResources"))
        assertTrue(yaml.contains("x-replace-resource-status") || yaml.contains("replaceResourceStatus"))
        assertTrue(yaml.contains("application/merge-patch+json"))
        assertTrue(yaml.contains("application/json-patch+json"))
        assertTrue(yaml.contains("labelSelector"))
        assertTrue(yaml.contains("namePrefix"))
        assertTrue(yaml.contains("ResourceList:"))
        assertTrue(yaml.contains("nextCursor"))
        assertTrue(yaml.contains("/v1/watch/{plural}"))
        assertTrue(yaml.contains("ResourceWatchEvent:"))
        assertTrue(yaml.contains("resource_version_too_old"))
        assertTrue(yaml.contains("x-watch-resources") || yaml.contains("watchResources"))
        assertTrue(yaml.contains("text/event-stream"))
        assertTrue(yaml.contains("/finalizers"))
        assertTrue(yaml.contains("FinalizerPatch:") || yaml.contains("x-patch-resource-finalizers"))
        assertTrue(yaml.contains("X-Forge-Delete-Confirmation"))
        assertTrue(yaml.contains("delete_confirmation_required") || yaml.contains("owned_resources_exist"))
        assertTrue(yaml.contains("owner_reference_cycle") || yaml.contains("OwnerRef:"))
    }

    @Test
    fun exampleListResponseValidatesAgainstListEnvelope() {
        val example = """
            {
              "apiVersion": "forge.dev/v1",
              "kind": "WidgetList",
              "resourceVersion": "1058",
              "items": [
                {
                  "apiVersion": "forge.dev/v1",
                  "kind": "Widget",
                  "metadata": {
                    "id": "wgt_01J5Z3K9QDJ8XN5V2H9T3RXYA",
                    "name": "sample-1",
                    "organization": "default",
                    "project": "invoice-platform",
                    "environment": "production",
                    "generation": 1,
                    "resourceVersion": "1057",
                    "labels": {"tier": "web"},
                    "annotations": {},
                    "ownerRefs": [],
                    "finalizers": [],
                    "createdAt": "2026-07-23T10:00:00Z",
                    "updatedAt": "2026-07-23T10:00:01Z"
                  },
                  "spec": {"size": "large"},
                  "status": {"phase": "Ready"}
                }
              ],
              "nextCursor": "eyJuYW1lIjoic2FtcGxlLTIiLCJpZCI6IndndF8xIn0"
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true; explicitNulls = false }
            .decodeFromString(ListEnvelope.serializer(), example)
        assertEquals("WidgetList", decoded.kind)
        assertEquals("1058", decoded.resourceVersion)
        assertEquals(1, decoded.items.size)
        assertEquals("sample-1", decoded.items.single().metadata.name)
        assertEquals("eyJuYW1lIjoic2FtcGxlLTIiLCJpZCI6IndndF8xIn0", decoded.nextCursor)
    }

    @Test
    fun exampleConditionDeserializesAgainstConditionSchema() {
        val example = """
            {
              "type": "Available",
              "status": "True",
              "reason": "MinimumReplicasAvailable",
              "message": "3/3 replicas ready",
              "lastTransitionTime": "2026-07-23T10:04:11Z"
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true }
            .decodeFromString(Condition.serializer(), example)
        assertEquals("Available", decoded.type)
        assertEquals("True", decoded.status)
        assertEquals("MinimumReplicasAvailable", decoded.reason)
    }

    @Test
    fun exampleEnvelopeRoundTripsThroughKotlinxSerialization() {
        val example = """
            {
              "apiVersion": "forge.dev/v1",
              "kind": "Widget",
              "metadata": {
                "id": "wgt_01J5Z3K9QDJ8XN5V2H9T3RXYA",
                "name": "sample",
                "organization": "default",
                "project": "invoice-platform",
                "environment": "production",
                "generation": 1,
                "resourceVersion": "1042",
                "labels": {},
                "annotations": {},
                "ownerRefs": [],
                "finalizers": [],
                "createdAt": "2026-07-23T10:00:00Z",
                "updatedAt": "2026-07-23T10:00:01Z"
              },
              "spec": {"size": "large"},
              "status": {}
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true; explicitNulls = false }
            .decodeFromString(ResourceEnvelopeResponse.serializer(), example)
        assertEquals("Widget", decoded.kind)
        assertEquals("1042", decoded.metadata.resourceVersion)
        assertEquals("large", decoded.spec["size"]!!.jsonPrimitiveSafe())
        assertTrue(decoded.metadata.id.startsWith("wgt_"))
    }

    private fun kotlinx.serialization.json.JsonElement.jsonPrimitiveSafe(): String =
        (this as JsonPrimitive).content
}
