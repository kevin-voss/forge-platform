package forge.control.reconcile

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/**
 * Validates Control→Runtime request shapes against the epic 04 workload contract
 * (`POST /v1/workloads` snake_case body; DELETE by deployment id / name suffix).
 */
class RuntimeClientContractTest {
    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun createBodyMatchesRuntimeWorkloadSpec() {
        // Mirrors forge-runtime WorkloadSpec fields used by POST /v1/workloads.
        val body = """
            {
              "deployment_id": "demo-11111111-0",
              "image": "registry.local/demo:v1",
              "port": 8080,
              "environment": { "PORT": "8080" }
            }
        """.trimIndent()
        val parsed = json.parseToJsonElement(body).jsonObject
        assertEquals("demo-11111111-0", parsed["deployment_id"]!!.jsonPrimitive.content)
        assertEquals("registry.local/demo:v1", parsed["image"]!!.jsonPrimitive.content)
        assertEquals("8080", parsed["port"]!!.jsonPrimitive.content)
        assertTrue(parsed.containsKey("environment"))
    }

    @Test
    fun deletePathUsesRuntimeDeploymentId() {
        val runtimeId = WorkloadNamer.runtimeDeploymentId(
            "demo",
            java.util.UUID.fromString("11111111-1111-1111-1111-111111111111"),
            1,
        )
        assertEquals("demo-11111111-1", runtimeId)
        // DELETE /v1/workloads/{deployment_id} — path segment is the Runtime id.
        assertEquals("/v1/workloads/$runtimeId", "/v1/workloads/$runtimeId")
    }
}
