package forge.control.scheduler

import forge.control.scheduler.api.HeartbeatRequest
import forge.control.scheduler.api.NodeResponse
import forge.control.scheduler.api.RegisterNodeRequest
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertTrue
import kotlinx.serialization.json.Json
import org.junit.jupiter.api.Assumptions.assumeTrue

class NodeOpenApiContractTest {
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
    fun openApiDeclaresNodeFleetPaths() {
        val yaml = openApiYaml()
        assumeTrue(yaml != null, "contracts/ not available in this build context")
        assertTrue(yaml!!.contains("/v1/nodes"))
        assertTrue(yaml.contains("/v1/nodes/register"))
        assertTrue(yaml.contains("/v1/nodes/{nodeId}/heartbeat") || yaml.contains("nodeHeartbeat"))
        assertTrue(yaml.contains("x-register-node") || yaml.contains("registerNode"))
        assertTrue(yaml.contains("x-list-nodes") || yaml.contains("listNodes"))
        assertTrue(yaml.contains("FleetNode"))
        assertTrue(yaml.contains("running_replicas"))
    }

    @Test
    fun examplePayloadsValidateAgainstDtos() {
        val json = Json { ignoreUnknownKeys = true }
        val register = """
            {
              "node_id": "node-a",
              "address": "http://runtime-a:4102",
              "capacity": { "slots": 4, "cpu_millis": 4000, "mem_mb": 4096 }
            }
        """.trimIndent()
        val registerDto = json.decodeFromString(RegisterNodeRequest.serializer(), register)
        assertTrue(registerDto.nodeId == "node-a")
        assertTrue(registerDto.capacity?.slots == 4)

        val heartbeat = """
            {
              "allocated": { "slots": 2 },
              "free": { "slots": 2 },
              "running_replicas": ["dpl_1:0", "dpl_1:1"]
            }
        """.trimIndent()
        val hbDto = json.decodeFromString(HeartbeatRequest.serializer(), heartbeat)
        assertTrue(hbDto.allocated?.slots == 2)
        assertTrue(hbDto.runningReplicas?.size == 2)

        val response = """
            {
              "id": "node-a",
              "address": "http://runtime-a:4102",
              "status": "online",
              "capacity": { "slots": 4 },
              "allocated": { "slots": 2 },
              "free": { "slots": 2 },
              "running_replicas": ["dpl_1:0", "dpl_1:1"],
              "last_heartbeat_at": "2026-07-22T14:00:00Z",
              "registered_at": "2026-07-22T13:00:00Z"
            }
        """.trimIndent()
        val node = json.decodeFromString(NodeResponse.serializer(), response)
        assertTrue(node.id == "node-a")
        assertTrue(node.status == "online")
        assertTrue(node.free.slots == 2)
    }
}
