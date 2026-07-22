package forge.control.reconcile

import forge.control.http.dto.ReconcileStatusResponse
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertTrue
import kotlinx.serialization.json.Json

class ReconcileOpenApiContractTest {
    private val openApiPath = Path.of(
        System.getenv("FORGE_ROOT")
            ?: Path.of("").toAbsolutePath().let { cwd ->
                generateSequence(cwd) { it.parent }.first { Files.exists(it.resolve("contracts")) }
            }.toString(),
        "contracts/openapi/forge-control.openapi.yaml",
    )

    @Test
    fun openApiDeclaresReconcilePathAndSchema() {
        assertTrue(Files.exists(openApiPath), "missing OpenAPI at $openApiPath")
        val yaml = Files.readString(openApiPath)
        assertTrue(yaml.contains("/v1/deployments/{deploymentId}/reconcile"))
        assertTrue(yaml.contains("x-get-reconcile-status"))
        assertTrue(yaml.contains("ReconcileStatus:"))
        assertTrue(yaml.contains("operationId: getReconcileStatus") || yaml.contains("getReconcileStatus"))
    }

    @Test
    fun exampleResponseMatchesDtoShape() {
        val example = """
            {
              "deploymentId": "11111111-1111-1111-1111-111111111111",
              "desired": {
                "image": "registry.local/demo:v1",
                "replicas": 2,
                "rollout": { "batchSize": 1, "timeoutSeconds": 120 }
              },
              "actual": {
                "replicas": [ { "replicaId": "r1", "status": "running" } ]
              },
              "plan": [ { "action": "StartReplica", "reason": "desired=2 actual=1" } ],
              "lastRunAt": "2026-07-22T14:00:00Z",
              "controllerHealthy": true
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true }
            .decodeFromString(ReconcileStatusResponse.serializer(), example)
        assertTrue(decoded.controllerHealthy)
        assertTrue(decoded.plan.any { it.action == "StartReplica" })
        assertTrue(decoded.desired.replicas == 2)
    }
}
