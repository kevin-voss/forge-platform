package forge.control.reconcile

import forge.control.observability.Otel
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

data class WorkloadEnsureRequest(
    val deploymentId: UUID,
    val serviceSlug: String,
    val serviceId: String,
    val replicaIndex: Int,
    val image: String,
    val port: Int,
    /** Extra env (secrets/config); merged with platform keys. Never logged. */
    val environment: Map<String, String> = emptyMap(),
    /** Secrets version fingerprint for redeploy detection (hash only). */
    val secretsFingerprint: String = "",
)

enum class EnsureOutcome {
    Created,
    Adopted,
    Recreated,
}

data class WorkloadHandle(
    val runtimeDeploymentId: String,
    val status: String,
    val hostPort: Int = 0,
    val image: String? = null,
    val secretsFingerprint: String? = null,
)

/** Runtime lifecycle + observation seam for the reconciliation controller. */
interface RuntimeClient {
    fun loadActual(deploymentId: UUID): ActualState

    fun observe(deploymentId: UUID): ActualState = loadActual(deploymentId)

    fun findWorkload(runtimeDeploymentId: String): WorkloadHandle?

    fun ensureWorkload(request: WorkloadEnsureRequest): EnsureOutcome

    fun stopWorkload(runtimeDeploymentId: String)

    /**
     * Mark a workload drained/unready without removing it so Gateway can
     * drop the upstream before StopReplica. Default no-op for test fakes.
     */
    fun drainWorkload(runtimeDeploymentId: String) {}

    /** All workloads on the node (for startup orphan GC). */
    fun listWorkloads(): List<WorkloadHandle> = emptyList()
}

class RuntimeUnreachableException(
    message: String,
    cause: Throwable? = null,
) : Exception(message, cause)

class RuntimeApiException(
    message: String,
    cause: Throwable? = null,
) : Exception(message, cause)

/**
 * HTTP client for forge-runtime workload APIs (04.03/04.06/04.07).
 *
 * Create: `POST /v1/workloads` with deterministic per-replica `deployment_id`.
 * Stop: `DELETE /v1/workloads/{deployment_id}` (idempotent).
 * Observe: `GET /v1/node/state` filtered by deployment short id (label-equivalent).
 */
class HttpRuntimeClient(
    private val runtimeUrl: String,
    private val httpClient: HttpClient = HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(2))
        .build(),
    private val json: Json = Json { ignoreUnknownKeys = true },
    private val restartCounts: MutableMap<String, Int> = ConcurrentHashMap(),
) : RuntimeClient {
    override fun loadActual(deploymentId: UUID): ActualState = observe(deploymentId)

    override fun observe(deploymentId: UUID): ActualState {
        val body = getNodeState()
        val short = WorkloadNamer.deploymentShort(deploymentId)
        val replicas = body.workloads
            .filter { WorkloadNamer.matchesDeployment(it.deploymentId, deploymentId) }
            .map { workload ->
                val index = WorkloadNamer.parseReplicaIndex(workload.deploymentId)
                val status = ReplicaStatus.parse(workload.status).wire()
                val restart = restartCounts.getOrDefault(workload.deploymentId, 0)
                ReplicaObservation(
                    replicaId = index?.toString() ?: "${short}:${workload.deploymentId}",
                    status = status,
                    replicaIndex = index,
                    restartCount = restart,
                    workloadName = "forge-${workload.deploymentId}",
                    image = workload.image?.takeIf { it.isNotBlank() },
                    secretsFingerprint = workload.secretsFingerprint?.takeIf { it.isNotBlank() },
                )
            }
            .sortedBy { it.replicaIndex ?: Int.MAX_VALUE }
        return ActualState(replicas)
    }

    override fun findWorkload(runtimeDeploymentId: String): WorkloadHandle? {
        val base = runtimeUrl.trimEnd('/')
        val builder = HttpRequest.newBuilder()
            .uri(URI.create("$base/v1/workloads/${encodePath(runtimeDeploymentId)}"))
            .timeout(Duration.ofSeconds(3))
            .GET()
        Otel.inject(builder)
        val response = try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            throw RuntimeUnreachableException("runtime unreachable at $base: ${e.message}", e)
        }
        return when (response.statusCode()) {
            404 -> null
            in 200..299 -> {
                val view = decodeWorkload(response.body())
                WorkloadHandle(
                    runtimeDeploymentId = view.deploymentId,
                    status = view.state,
                    hostPort = view.hostPort,
                    image = view.image,
                    secretsFingerprint = view.secretsFingerprint,
                )
            }
            else -> throw RuntimeApiException(
                "runtime get workload HTTP ${response.statusCode()} for $runtimeDeploymentId",
            )
        }
    }

    override fun ensureWorkload(request: WorkloadEnsureRequest): EnsureOutcome {
        val runtimeId = WorkloadNamer.runtimeDeploymentId(
            request.serviceSlug,
            request.deploymentId,
            request.replicaIndex,
        )
        val existing = findWorkload(runtimeId)
        if (existing != null) {
            val status = runCatching { ReplicaStatus.parse(existing.status) }.getOrNull()
            val imageMatches = existing.image.isNullOrBlank() || existing.image == request.image
            val fingerprintMatches = request.secretsFingerprint.isBlank() ||
                existing.secretsFingerprint == request.secretsFingerprint
            if (imageMatches &&
                fingerprintMatches &&
                (status == ReplicaStatus.Running || status == ReplicaStatus.Ready || status == ReplicaStatus.Pending)
            ) {
                // Idempotent: healthy workload already present with desired image/fingerprint.
                return EnsureOutcome.Adopted
            }
            // Crashed/stopped, image mismatch, or secrets fingerprint drift: recreate.
            stopWorkload(runtimeId)
            restartCounts.merge(runtimeId, 1, Int::plus)
            createWorkload(runtimeId, request)
            return EnsureOutcome.Recreated
        }
        createWorkload(runtimeId, request)
        return EnsureOutcome.Created
    }

    override fun drainWorkload(runtimeDeploymentId: String) {
        val base = runtimeUrl.trimEnd('/')
        val builder = HttpRequest.newBuilder()
            .uri(URI.create("$base/v1/workloads/${encodePath(runtimeDeploymentId)}/drain"))
            .timeout(Duration.ofSeconds(5))
            .POST(HttpRequest.BodyPublishers.noBody())
        Otel.inject(builder)
        val response = try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            throw RuntimeUnreachableException("runtime unreachable at $base: ${e.message}", e)
        }
        // 200 drained; 404 already gone — both OK for rolling drain.
        if (response.statusCode() !in setOf(200, 404) && response.statusCode() !in 200..299) {
            throw RuntimeApiException(
                "runtime drain workload HTTP ${response.statusCode()} for $runtimeDeploymentId",
            )
        }
    }

    override fun stopWorkload(runtimeDeploymentId: String) {
        val base = runtimeUrl.trimEnd('/')
        val builder = HttpRequest.newBuilder()
            .uri(URI.create("$base/v1/workloads/${encodePath(runtimeDeploymentId)}"))
            .timeout(Duration.ofSeconds(30))
            .DELETE()
        Otel.inject(builder)
        val response = try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            throw RuntimeUnreachableException("runtime unreachable at $base: ${e.message}", e)
        }
        // 204 success; 404 already gone — both idempotent success.
        if (response.statusCode() !in setOf(204, 404) && response.statusCode() !in 200..299) {
            throw RuntimeApiException(
                "runtime delete workload HTTP ${response.statusCode()} for $runtimeDeploymentId",
            )
        }
        restartCounts.remove(runtimeDeploymentId)
    }

    override fun listWorkloads(): List<WorkloadHandle> {
        val body = getNodeState()
        return body.workloads.map { workload ->
            WorkloadHandle(
                runtimeDeploymentId = workload.deploymentId,
                status = workload.status,
                hostPort = workload.hostPort,
                image = workload.image,
                secretsFingerprint = workload.secretsFingerprint,
            )
        }
    }

    private fun createWorkload(runtimeId: String, request: WorkloadEnsureRequest) {
        val base = runtimeUrl.trimEnd('/')
        val environment = linkedMapOf(
            "PORT" to request.port.toString(),
            "FORGE_DEPLOYMENT_ID" to request.deploymentId.toString(),
            "FORGE_SERVICE_ID" to request.serviceId,
            "FORGE_REPLICA_INDEX" to request.replicaIndex.toString(),
        )
        // Merge injected secrets/config; platform keys win on collision.
        for ((k, v) in request.environment) {
            environment.putIfAbsent(k, v)
        }
        if (request.secretsFingerprint.isNotBlank()) {
            environment["FORGE_SECRETS_FINGERPRINT"] = request.secretsFingerprint
        }
        val body = json.encodeToString(
            WorkloadCreateBody.serializer(),
            WorkloadCreateBody(
                deployment_id = runtimeId,
                image = request.image,
                port = request.port,
                environment = environment,
                secrets_fingerprint = request.secretsFingerprint.takeIf { it.isNotBlank() },
            ),
        )
        val builder = HttpRequest.newBuilder()
            .uri(URI.create("$base/v1/workloads"))
            .timeout(Duration.ofSeconds(120))
            .header("content-type", "application/json")
            .POST(HttpRequest.BodyPublishers.ofString(body))
        Otel.inject(builder)
        val response = try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            throw RuntimeUnreachableException("runtime unreachable at $base: ${e.message}", e)
        }
        when (response.statusCode()) {
            200, 201 -> Unit
            409 -> Unit // conflict / already exists — treat as idempotent success
            else -> throw RuntimeApiException(
                "runtime create workload HTTP ${response.statusCode()}: ${response.body()}",
            )
        }
    }

    private fun getNodeState(): NodeStateResponse {
        val base = runtimeUrl.trimEnd('/')
        val builder = HttpRequest.newBuilder()
            .uri(URI.create("$base/v1/node/state"))
            .timeout(Duration.ofSeconds(3))
            .GET()
        Otel.inject(builder)
        val response = try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            throw RuntimeUnreachableException("runtime unreachable at $base: ${e.message}", e)
        }
        if (response.statusCode() !in 200..299) {
            throw RuntimeUnreachableException(
                "runtime node state HTTP ${response.statusCode()} from $base",
            )
        }
        return try {
            json.decodeFromString(NodeStateResponse.serializer(), response.body())
        } catch (e: Exception) {
            throw RuntimeUnreachableException("runtime node state decode failed: ${e.message}", e)
        }
    }

    private fun decodeWorkload(body: String): WorkloadViewResponse =
        try {
            json.decodeFromString(WorkloadViewResponse.serializer(), body)
        } catch (e: Exception) {
            throw RuntimeApiException("runtime workload decode failed: ${e.message}", e)
        }

    private fun encodePath(value: String): String =
        java.net.URLEncoder.encode(value, Charsets.UTF_8).replace("+", "%20")
}

@Serializable
private data class WorkloadCreateBody(
    val deployment_id: String,
    val image: String,
    val port: Int,
    val environment: Map<String, String> = emptyMap(),
    val secrets_fingerprint: String? = null,
)

@Serializable
private data class WorkloadViewResponse(
    val deploymentId: String,
    val containerId: String = "",
    val hostPort: Int = 0,
    val state: String = "pending",
    val image: String? = null,
    val secretsFingerprint: String? = null,
)

@Serializable
private data class NodeStateResponse(
    val nodeId: String? = null,
    val workloads: List<NodeWorkloadState> = emptyList(),
)

@Serializable
private data class NodeWorkloadState(
    val deploymentId: String,
    val status: String,
    val hostPort: Int = 0,
    val image: String? = null,
    val secretsFingerprint: String? = null,
)
