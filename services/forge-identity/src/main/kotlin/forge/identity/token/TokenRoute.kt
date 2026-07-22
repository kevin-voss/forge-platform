package forge.identity.token

import forge.identity.http.ApiException
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import kotlinx.serialization.Serializable

@Serializable
data class TokenOwnerRef(
    val type: String? = null,
    val id: String? = null,
)

@Serializable
data class CreateTokenRequest(
    val owner: TokenOwnerRef? = null,
    val project_id: String? = null,
    val role: String? = null,
    val expires_in_s: Long? = null,
)

@Serializable
data class CreateTokenResponse(
    val token_id: String,
    val token: String,
    val prefix: String,
    val expires_at: String? = null,
)

@Serializable
data class TokenMetadataResponse(
    val token_id: String,
    val prefix: String,
    val owner_type: String,
    val owner_id: String,
    val project_id: String,
    val role: String,
    val created_at: String,
    val expires_at: String? = null,
    val revoked_at: String? = null,
)

fun ApiToken.toMetadataResponse(): TokenMetadataResponse =
    TokenMetadataResponse(
        token_id = id,
        prefix = prefix,
        owner_type = ownerType,
        owner_id = ownerId,
        project_id = projectId,
        role = role,
        created_at = createdAt.toString(),
        expires_at = expiresAt?.toString(),
        revoked_at = revokedAt?.toString(),
    )

fun Route.tokenRoutes(tokens: TokenStore) = tokenRoutes { tokens }

fun Route.tokenRoutes(tokens: () -> TokenStore) {
    route("/v1/tokens") {
        post {
            val body = call.receive<CreateTokenRequest>()
            val owner = body.owner
                ?: throw ApiException.BadRequest("owner is required", mapOf("field" to "owner"))
            val created = tokens().create(
                ownerType = owner.type ?: throw ApiException.BadRequest(
                    "owner.type is required",
                    mapOf("field" to "owner.type"),
                ),
                ownerId = owner.id ?: throw ApiException.BadRequest(
                    "owner.id is required",
                    mapOf("field" to "owner.id"),
                ),
                projectId = body.project_id ?: throw ApiException.BadRequest(
                    "project_id is required",
                    mapOf("field" to "project_id"),
                ),
                role = body.role ?: throw ApiException.BadRequest(
                    "role is required",
                    mapOf("field" to "role"),
                ),
                expiresInSeconds = body.expires_in_s,
            )
            call.respond(
                HttpStatusCode.Created,
                CreateTokenResponse(
                    token_id = created.token.id,
                    token = created.plaintext,
                    prefix = created.token.prefix,
                    expires_at = created.token.expiresAt?.toString(),
                ),
            )
        }
        get {
            val owner = call.request.queryParameters["owner"]
                ?: throw ApiException.BadRequest(
                    "owner query parameter is required",
                    mapOf("field" to "owner"),
                )
            val ownerType = call.request.queryParameters["owner_type"]
            val list = tokens().listByOwner(owner, ownerType).map { it.toMetadataResponse() }
            call.respond(HttpStatusCode.OK, list)
        }
        post("{tokenId}/revoke") {
            val tokenId = call.parameters["tokenId"]
                ?: throw ApiException.BadRequest("tokenId is required")
            tokens().revoke(tokenId)
            call.respond(HttpStatusCode.NoContent)
        }
    }
}
