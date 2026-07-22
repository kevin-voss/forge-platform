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
                "image": "registry.local/demo:v2",
                "replicas": 2,
                "rollout": { "batchSize": 1, "timeoutSeconds": 120 }
              },
              "actual": {
                "replicas": [
                  { "replicaId": "0", "status": "ready", "image": "registry.local/demo:v1" },
                  { "replicaId": "2", "status": "ready", "image": "registry.local/demo:v2" }
                ]
              },
              "plan": [ { "action": "ShiftTraffic", "reason": "rolling shift", "replicaId": "2" } ],
              "lastRunAt": "2026-07-22T14:00:00Z",
              "controllerHealthy": true,
              "phase": "rolling",
              "updatedReplicas": "1/2",
              "currentImage": "registry.local/demo:v1",
              "targetImage": "registry.local/demo:v2",
              "status": "deploying",
              "lastHealthyImage": "registry.local/demo:v1"
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true }
            .decodeFromString(ReconcileStatusResponse.serializer(), example)
        assertTrue(decoded.controllerHealthy)
        assertTrue(decoded.plan.any { it.action == "ShiftTraffic" })
        assertTrue(decoded.desired.replicas == 2)
        assertTrue(decoded.phase == "rolling")
        assertTrue(decoded.updatedReplicas == "1/2")
        assertTrue(decoded.status == "deploying")
        assertTrue(decoded.lastHealthyImage == "registry.local/demo:v1")
    }

    @Test
    fun openApiDeclaresRollingActionsAndPhase() {
        val yaml = Files.readString(openApiPath)
        assertTrue(yaml.contains("WaitReady"))
        assertTrue(yaml.contains("ShiftTraffic"))
        assertTrue(yaml.contains("DrainReplica"))
        assertTrue(yaml.contains("updatedReplicas"))
        assertTrue(yaml.contains("currentImage"))
        assertTrue(yaml.contains("targetImage"))
        assertTrue(yaml.contains("lastHealthyImage"))
        assertTrue(yaml.contains("rolling_back"))
        assertTrue(yaml.contains("rolled_back"))
    }

    @Test
    fun openApiDeclaresDeploymentHistory() {
        val yaml = Files.readString(openApiPath)
        assertTrue(yaml.contains("/v1/deployments/{deploymentId}/history"))
        assertTrue(yaml.contains("x-get-deployment-history"))
        assertTrue(yaml.contains("DeploymentHistory:"))
        assertTrue(yaml.contains("DeploymentEvent:"))
        assertTrue(yaml.contains("operationId: getDeploymentHistory") || yaml.contains("getDeploymentHistory"))
    }
}
