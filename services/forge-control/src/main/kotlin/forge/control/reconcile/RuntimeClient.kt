package forge.control.reconcile

import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import java.util.UUID
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/** Read actual replica state from forge-runtime. No mutate methods in 07.01. */
interface RuntimeClient {
    fun loadActual(deploymentId: UUID): ActualState
}

class RuntimeUnreachableException(
    message: String,
    cause: Throwable? = null,
) : Exception(message, cause)

/**
 * HTTP client for `GET {runtimeUrl}/v1/node/state` (04.07).
 * Maps Runtime workload statuses into the reconcile replica vocabulary.
 */
class HttpRuntimeClient(
    private val runtimeUrl: String,
    private val httpClient: HttpClient = HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(2))
        .build(),
    private val json: Json = Json { ignoreUnknownKeys = true },
) : RuntimeClient {
    override fun loadActual(deploymentId: UUID): ActualState {
        val base = runtimeUrl.trimEnd('/')
        val request = HttpRequest.newBuilder()
            .uri(URI.create("$base/v1/node/state"))
            .timeout(Duration.ofSeconds(3))
            .GET()
            .build()
        val response = try {
            httpClient.send(request, HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            throw RuntimeUnreachableException("runtime unreachable at $base: ${e.message}", e)
        }
        if (response.statusCode() !in 200..299) {
            throw RuntimeUnreachableException(
                "runtime node state HTTP ${response.statusCode()} from $base",
            )
        }
        val body = try {
            json.decodeFromString(NodeStateResponse.serializer(), response.body())
        } catch (e: Exception) {
            throw RuntimeUnreachableException("runtime node state decode failed: ${e.message}", e)
        }
        val id = deploymentId.toString()
        val replicas = body.workloads
            .filter { it.deploymentId == id }
            .mapIndexed { index, workload ->
                ReplicaObservation(
                    replicaId = replicaIdFor(workload, index),
                    status = ReplicaStatus.parse(workload.status).wire(),
                )
            }
        return ActualState(replicas)
    }

    private fun replicaIdFor(workload: NodeWorkloadState, index: Int): String {
        val port = workload.hostPort
        return if (port > 0) {
            "${workload.deploymentId}:$port"
        } else {
            "${workload.deploymentId}:$index"
        }
    }
}

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
)
