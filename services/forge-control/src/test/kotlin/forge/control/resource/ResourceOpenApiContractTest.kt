package forge.control.resource

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
        assertTrue(yaml.contains("ResourceEnvelope:"))
        assertTrue(yaml.contains("resourceVersion"))
        assertTrue(yaml.contains("resource_version_conflict") || yaml.contains("ResourceError"))
        assertTrue(yaml.contains("x-create-resource") || yaml.contains("createResource"))
        assertTrue(yaml.contains("application/merge-patch+json"))
        assertTrue(yaml.contains("application/json-patch+json"))
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
