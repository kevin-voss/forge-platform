package forge.control.scheduler.api

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.scheduler.BootstrapTokenStore
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.delete
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import java.time.Instant
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class IssueBootstrapTokenRequest(
    val organization: String? = null,
    @SerialName("node_pool") val nodePool: String? = null,
    @SerialName("ttl_seconds") val ttlSeconds: Long? = null,
)

@Serializable
data class BootstrapTokenScope(
    val organization: String,
    @SerialName("node_pool") val nodePool: String? = null,
)

@Serializable
data class IssueBootstrapTokenResponse(
    val token: String,
    @SerialName("expires_at") val expiresAt: String,
    val scope: BootstrapTokenScope,
    val id: String,
)

fun Route.bootstrapTokenRoutes(
    store: BootstrapTokenStore,
    log: JsonLog,
    defaultTtlSeconds: Long = 900,
    clock: () -> Instant = { Instant.now() },
) {
    route("/v1/nodes/bootstrap-tokens") {
        post {
            val body = call.receive<IssueBootstrapTokenRequest>()
            val org = body.organization?.trim().orEmpty()
            if (org.isEmpty()) {
                throw ApiException.BadRequest(
                    "organization is required",
                    mapOf("field" to "organization"),
                )
            }
            val ttl = body.ttlSeconds ?: defaultTtlSeconds
            val issued = store.issue(
                organization = org,
                nodePool = body.nodePool,
                ttlSeconds = ttl,
                now = clock(),
            )
            log.info(
                "bootstrap token issued",
                "token_id" to issued.record.id,
                "organization" to issued.record.organization,
                "node_pool" to (issued.record.nodePool ?: ""),
                "expires_at" to issued.record.expiresAt.toString(),
            )
            call.respond(
                HttpStatusCode.Created,
                IssueBootstrapTokenResponse(
                    token = issued.plaintext,
                    expiresAt = issued.record.expiresAt.toString(),
                    scope = BootstrapTokenScope(
                        organization = issued.record.organization,
                        nodePool = issued.record.nodePool,
                    ),
                    id = issued.record.id,
                ),
            )
        }

        delete("/{tokenId}") {
            val tokenId = call.parameters["tokenId"]?.trim().orEmpty()
            if (tokenId.isEmpty()) {
                throw ApiException.BadRequest(
                    "token id is required",
                    mapOf("field" to "tokenId"),
                )
            }
            val revoked = store.revoke(tokenId, clock())
                ?: throw ApiException.NotFound(
                    "bootstrap token not found",
                    mapOf("token_id" to tokenId),
                )
            log.info(
                "bootstrap token revoked",
                "token_id" to revoked.id,
                "organization" to revoked.organization,
            )
            call.respond(HttpStatusCode.NoContent)
        }
    }
}
