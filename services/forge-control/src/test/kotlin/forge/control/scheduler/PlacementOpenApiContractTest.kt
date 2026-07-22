package forge.control.scheduler

import forge.control.scheduler.api.PlacementResponse
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertTrue
import kotlinx.serialization.json.Json
import org.junit.jupiter.api.Assumptions.assumeTrue

class PlacementOpenApiContractTest {
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
    fun openApiDeclaresPlacementPaths() {
        val yaml = openApiYaml()
        assumeTrue(yaml != null, "contracts/ not available in this build context")
        assertTrue(yaml!!.contains("/v1/placements"))
        assertTrue(yaml.contains("x-create-placement") || yaml.contains("createPlacement"))
        assertTrue(yaml.contains("x-list-placements") || yaml.contains("listPlacements"))
        assertTrue(yaml.contains("Placement:"))
        assertTrue(yaml.contains("placement_id") || yaml.contains("placementId"))
        assertTrue(yaml.contains("single-node"))
    }

    @Test
    fun exampleResponseMatchesDtoShape() {
        val example = """
            {
              "placement_id": "plc_1",
              "deployment_id": "11111111-1111-1111-1111-111111111111",
              "replica_index": 0,
              "node_id": "node-local",
              "strategy": "single-node",
              "reason": "only node available"
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true }
            .decodeFromString(PlacementResponse.serializer(), example)
        assertTrue(decoded.placementId == "plc_1")
        assertTrue(decoded.nodeId == "node-local")
        assertTrue(decoded.strategy == "single-node")
        assertTrue(decoded.replicaIndex == 0)
    }
}
