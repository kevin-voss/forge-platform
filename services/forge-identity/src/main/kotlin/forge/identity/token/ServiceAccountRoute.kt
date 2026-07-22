package forge.identity.token

import forge.identity.http.ApiException
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import kotlinx.serialization.Serializable

@Serializable
data class CreateServiceAccountRequest(
    val project_id: String? = null,
    val name: String? = null,
    val role: String? = null,
)

@Serializable
data class ServiceAccountResponse(
    val id: String,
    val project_id: String,
    val name: String,
    val role: String,
    val created_at: String,
)

fun ServiceAccount.toResponse(): ServiceAccountResponse =
    ServiceAccountResponse(
        id = id,
        project_id = projectId,
        name = name,
        role = role,
        created_at = createdAt.toString(),
    )

fun Route.serviceAccountRoutes(accounts: ServiceAccountStore) = serviceAccountRoutes { accounts }

fun Route.serviceAccountRoutes(accounts: () -> ServiceAccountStore) {
    route("/v1/service-accounts") {
        post {
            val body = call.receive<CreateServiceAccountRequest>()
            val created = accounts().create(
                projectId = body.project_id ?: throw ApiException.BadRequest(
                    "project_id is required",
                    mapOf("field" to "project_id"),
                ),
                name = body.name ?: throw ApiException.BadRequest(
                    "name is required",
                    mapOf("field" to "name"),
                ),
                role = body.role ?: throw ApiException.BadRequest(
                    "role is required",
                    mapOf("field" to "role"),
                ),
            )
            call.respond(HttpStatusCode.Created, created.toResponse())
        }
    }
}
