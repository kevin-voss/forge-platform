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
        assertTrue(yaml.contains("first-fit"))
        assertTrue(yaml.contains("least-allocated"))
        assertTrue(yaml.contains("single-node"))
        assertTrue(yaml.contains("'202'") || yaml.contains("202:"))
        assertTrue(yaml.contains("status: pending") || yaml.contains("pending"))
        assertTrue(yaml.contains("lost"))
        assertTrue(yaml.contains("rescheduled_from_node") || yaml.contains("rescheduledFromNode"))
        assertTrue(yaml.contains("anti_affinity") || yaml.contains("anti-affinity"))
        assertTrue(yaml.contains("requests") || yaml.contains("ResourceBundle"))
        assertTrue(yaml.contains("limits"))
        assertTrue(yaml.contains("trace") || yaml.contains("PlacementTrace"))
        assertTrue(yaml.contains("unschedulable_reasons") || yaml.contains("UnschedulableReason"))
        assertTrue(yaml.contains("/v1/placements/{placementId}") || yaml.contains("getPlacement"))
        assertTrue(yaml.contains("allocatable"))
        assertTrue(yaml.contains("\"slots\": 1") || yaml.contains("slots: 1") || yaml.contains("legacySlots"))
        assertTrue(yaml.contains("nodeSelector") || yaml.contains("PlacementConstraints"))
        assertTrue(yaml.contains("tolerations") || yaml.contains("Toleration"))
        assertTrue(yaml.contains("platform") || yaml.contains("PlatformSpec"))
        assertTrue(yaml.contains("topologySpreadConstraints") || yaml.contains("TopologySpreadConstraint"))
        assertTrue(yaml.contains("affinity") || yaml.contains("PlacementAffinity"))
        assertTrue(yaml.contains("trace") && (yaml.contains("scores") || yaml.contains("PlacementTrace")))
        assertTrue(yaml.contains("zone") && yaml.contains("region") && yaml.contains("provider"))
    }

    @Test
    fun exampleResponseMatchesDtoShape() {
        val example = """
            {
              "placement_id": "plc_1",
              "deployment_id": "11111111-1111-1111-1111-111111111111",
              "replica_index": 0,
              "node_id": "node-b",
              "strategy": "least-allocated",
              "reason": "least-allocated: node-b free=4",
              "status": "placed",
              "anti_affinity": "soft"
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true }
            .decodeFromString(PlacementResponse.serializer(), example)
        assertTrue(decoded.placementId == "plc_1")
        assertTrue(decoded.nodeId == "node-b")
        assertTrue(decoded.strategy == "least-allocated")
        assertTrue(decoded.replicaIndex == 0)
        assertTrue(decoded.status == "placed")
    }

    @Test
    fun pendingResponseMatchesDtoShape() {
        val example = """
            {
              "placement_id": "plc_2",
              "deployment_id": "11111111-1111-1111-1111-111111111111",
              "replica_index": 4,
              "status": "pending",
              "reason": "no node with 1 free slot",
              "anti_affinity": "soft",
              "strategy": "pending"
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true }
            .decodeFromString(PlacementResponse.serializer(), example)
        assertTrue(decoded.status == "pending")
        assertTrue(decoded.nodeId == null)
        assertTrue(decoded.reason!!.contains("no node"))
    }

    @Test
    fun lostAndReplacementResponseMatchDtoShape() {
        val lost = """
            {
              "placement_id": "plc_old",
              "deployment_id": "11111111-1111-1111-1111-111111111111",
              "replica_index": 1,
              "node_id": "node-b",
              "status": "lost",
              "strategy": "least-allocated",
              "anti_affinity": "soft"
            }
        """.trimIndent()
        val replacement = """
            {
              "placement_id": "plc_new",
              "deployment_id": "11111111-1111-1111-1111-111111111111",
              "replica_index": 1,
              "node_id": "node-a",
              "status": "placed",
              "strategy": "least-allocated",
              "anti_affinity": "soft",
              "rescheduled_from_node": "node-b"
            }
        """.trimIndent()
        val json = Json { ignoreUnknownKeys = true }
        assertTrue(json.decodeFromString(PlacementResponse.serializer(), lost).status == "lost")
        val decoded = json.decodeFromString(PlacementResponse.serializer(), replacement)
        assertTrue(decoded.status == "placed")
        assertTrue(decoded.rescheduledFromNode == "node-b")
    }
}
