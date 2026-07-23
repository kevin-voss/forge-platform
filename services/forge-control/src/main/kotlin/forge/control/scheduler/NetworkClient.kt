package forge.control.scheduler

import java.net.URI
import java.net.URLEncoder
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

data class NodeNetworkLease(
    val nodeId: String,
    val cidr: String,
    val gateway: String,
)

sealed class NetworkLeaseResult {
    data class Ok(val lease: NodeNetworkLease) : NetworkLeaseResult()
    data class NoAddress(val detail: String) : NetworkLeaseResult()
    data class Unavailable(val detail: String) : NetworkLeaseResult()
    data class Failed(val detail: String) : NetworkLeaseResult()
}

/** Control → forge-network node-block lease client (22.01). */
interface NetworkClient {
    fun allocateNodeLease(networkName: String, nodeId: String): NetworkLeaseResult
}

/** Used when FORGE_NETWORK_URL is unset — join handshake cannot allocate addresses. */
object NoOpNetworkClient : NetworkClient {
    override fun allocateNodeLease(networkName: String, nodeId: String): NetworkLeaseResult =
        NetworkLeaseResult.Unavailable("forge-network URL not configured")
}

class HttpNetworkClient(
    private val networkUrl: String,
    private val httpClient: HttpClient = HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(2))
        .build(),
    private val json: Json = Json { ignoreUnknownKeys = true },
) : NetworkClient {
    override fun allocateNodeLease(networkName: String, nodeId: String): NetworkLeaseResult {
        val name = networkName.trim()
        val id = nodeId.trim()
        if (name.isEmpty() || id.isEmpty()) {
            return NetworkLeaseResult.Failed("network name and node_id are required")
        }
        val base = networkUrl.trimEnd('/')
        val path = "/v1/networks/${enc(name)}/node-leases"
        val body = json.encodeToString(NodeLeaseRequest.serializer(), NodeLeaseRequest(id))
        val builder = HttpRequest.newBuilder()
            .uri(URI.create("$base$path"))
            .timeout(Duration.ofSeconds(10))
            .header("content-type", "application/json")
            .POST(HttpRequest.BodyPublishers.ofString(body))
        forge.control.observability.Otel.inject(builder)
        val response = try {
            httpClient.send(builder.build(), HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            return NetworkLeaseResult.Unavailable(
                "forge-network unreachable at $base: ${e.message ?: e.javaClass.simpleName}",
            )
        }
        return when (response.statusCode()) {
            in 200..299 -> {
                val parsed = try {
                    json.decodeFromString(NodeLeaseResponse.serializer(), response.body())
                } catch (e: Exception) {
                    return NetworkLeaseResult.Failed(
                        "forge-network decode failed: ${e.message ?: e.javaClass.simpleName}",
                    )
                }
                NetworkLeaseResult.Ok(
                    NodeNetworkLease(
                        nodeId = parsed.nodeId,
                        cidr = parsed.cidr,
                        gateway = parsed.gateway,
                    ),
                )
            }
            409 -> {
                val code = extractErrorCode(response.body())
                if (code in setOf("NoAddressSpaceAvailable", "NodeBlockExhausted", "NetworkNotReady")) {
                    NetworkLeaseResult.NoAddress(response.body().take(200))
                } else {
                    NetworkLeaseResult.Failed("forge-network HTTP 409: ${response.body().take(200)}")
                }
            }
            in 500..599 -> NetworkLeaseResult.Unavailable("forge-network HTTP ${response.statusCode()}")
            404 -> NetworkLeaseResult.Failed("forge-network network not found: $name")
            else -> NetworkLeaseResult.Failed(
                "forge-network HTTP ${response.statusCode()}: ${response.body().take(200)}",
            )
        }
    }

    private fun enc(value: String): String =
        URLEncoder.encode(value, Charsets.UTF_8).replace("+", "%20")

    private fun extractErrorCode(body: String): String =
        try {
            json.decodeFromString(ErrorBody.serializer(), body).error?.code.orEmpty()
        } catch (_: Exception) {
            ""
        }

    @Serializable
    private data class NodeLeaseRequest(
        @SerialName("node_id") val nodeId: String,
    )

    @Serializable
    private data class NodeLeaseResponse(
        @SerialName("node_id") val nodeId: String,
        val cidr: String,
        val gateway: String,
    )

    @Serializable
    private data class ErrorBody(
        val error: ErrorCode? = null,
    )

    @Serializable
    private data class ErrorCode(
        val code: String? = null,
    )
}
