package forge.control.reconcile

import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration

enum class ShiftOutcome {
    Shifted,
    Drained,
    GatewayUnreachable,
    Failed,
}

data class ShiftResult(
    val outcome: ShiftOutcome,
    val detail: String? = null,
)

/** Gateway admin seam used to force route sync / drain confirmation. */
interface GatewayClient {
    fun refreshRoutes(): ShiftResult
}

class GatewayUnreachableException(
    message: String,
    cause: Throwable? = null,
) : Exception(message, cause)

/**
 * Ensures Gateway upstreams reflect the ready replica set and drains old
 * replicas before stop. Fail-closed: unreachable Gateway blocks stops.
 */
class TrafficShifter(
    private val gatewayClient: GatewayClient,
) {
    fun shiftToReady(replicaId: String): ShiftResult {
        val refreshed = gatewayClient.refreshRoutes()
        if (refreshed.outcome == ShiftOutcome.GatewayUnreachable) {
            return refreshed
        }
        if (refreshed.outcome == ShiftOutcome.Failed) {
            return refreshed
        }
        return ShiftResult(
            outcome = ShiftOutcome.Shifted,
            detail = "shifted replica=$replicaId",
        )
    }

    fun drain(replicaId: String): ShiftResult {
        val refreshed = gatewayClient.refreshRoutes()
        if (refreshed.outcome == ShiftOutcome.GatewayUnreachable) {
            return refreshed
        }
        if (refreshed.outcome == ShiftOutcome.Failed) {
            return refreshed
        }
        return ShiftResult(
            outcome = ShiftOutcome.Drained,
            detail = "drained replica=$replicaId",
        )
    }
}

/** HTTP client for forge-gateway admin refresh (`POST /admin/routes/refresh`). */
class HttpGatewayClient(
    private val gatewayUrl: String,
    private val httpClient: HttpClient = HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(2))
        .build(),
) : GatewayClient {
    override fun refreshRoutes(): ShiftResult {
        val base = gatewayUrl.trimEnd('/')
        val request = HttpRequest.newBuilder()
            .uri(URI.create("$base/admin/routes/refresh"))
            .timeout(Duration.ofSeconds(5))
            .POST(HttpRequest.BodyPublishers.noBody())
            .build()
        val response = try {
            httpClient.send(request, HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            return ShiftResult(
                outcome = ShiftOutcome.GatewayUnreachable,
                detail = e.message ?: e.javaClass.simpleName,
            )
        }
        return if (response.statusCode() in 200..299) {
            ShiftResult(outcome = ShiftOutcome.Shifted, detail = "refresh ok")
        } else {
            ShiftResult(
                outcome = ShiftOutcome.Failed,
                detail = "gateway refresh HTTP ${response.statusCode()}",
            )
        }
    }
}

/** No-op gateway for unit tests / when gateway URL is unset. */
class NoOpGatewayClient : GatewayClient {
    override fun refreshRoutes(): ShiftResult =
        ShiftResult(outcome = ShiftOutcome.Shifted, detail = "noop")
}
