package forge.identity.org

import forge.identity.http.ApiException
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.delete
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import kotlinx.serialization.Serializable

@Serializable
data class CreateOrgRequest(
    val name: String? = null,
)

@Serializable
data class OrgResponse(
    val id: String,
    val name: String,
    val created_at: String,
)

@Serializable
data class AddOrgMemberRequest(
    val user_id: String? = null,
    val role: String? = null,
)

@Serializable
data class OrgMembershipResponse(
    val org_id: String,
    val user_id: String,
    val role: String,
)

fun Organization.toResponse(): OrgResponse =
    OrgResponse(id = id, name = name, created_at = createdAt.toString())

fun OrgMembership.toResponse(): OrgMembershipResponse =
    OrgMembershipResponse(org_id = orgId, user_id = userId, role = role)

fun Route.orgRoutes(orgs: OrgStore) = orgRoutes { orgs }

fun Route.orgRoutes(orgs: () -> OrgStore) {
    route("/v1/orgs") {
        post {
            val body = call.receive<CreateOrgRequest>()
            val created = orgs().create(
                name = body.name ?: throw ApiException.BadRequest(
                    "name is required",
                    mapOf("field" to "name"),
                ),
            )
            call.respond(HttpStatusCode.Created, created.toResponse())
        }
        get {
            call.respond(orgs().list().map { it.toResponse() })
        }
        get("{orgId}") {
            val orgId = call.parameters["orgId"]
                ?: throw ApiException.BadRequest("orgId is required")
            call.respond(orgs().get(orgId).toResponse())
        }
        get("{orgId}/members") {
            val orgId = call.parameters["orgId"]
                ?: throw ApiException.BadRequest("orgId is required")
            call.respond(orgs().listMembers(orgId).map { it.toResponse() })
        }
        post("{orgId}/members") {
            val orgId = call.parameters["orgId"]
                ?: throw ApiException.BadRequest("orgId is required")
            val body = call.receive<AddOrgMemberRequest>()
            val membership = orgs().addMember(
                orgId = orgId,
                userId = body.user_id ?: throw ApiException.BadRequest(
                    "user_id is required",
                    mapOf("field" to "user_id"),
                ),
                role = body.role ?: throw ApiException.BadRequest(
                    "role is required",
                    mapOf("field" to "role"),
                ),
            )
            call.respond(HttpStatusCode.Created, membership.toResponse())
        }
        delete("{orgId}/members/{userId}") {
            val orgId = call.parameters["orgId"]
                ?: throw ApiException.BadRequest("orgId is required")
            val userId = call.parameters["userId"]
                ?: throw ApiException.BadRequest("userId is required")
            orgs().removeMember(orgId, userId)
            call.respond(HttpStatusCode.NoContent)
        }
    }
}
