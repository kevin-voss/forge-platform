package forge.identity.user

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
data class CreateUserRequest(
    val email: String? = null,
    val display_name: String? = null,
)

@Serializable
data class UserResponse(
    val id: String,
    val email: String,
    val display_name: String,
    val created_at: String,
)

@Serializable
data class OrgMembershipResponse(
    val org_id: String,
    val org_name: String,
    val role: String,
)

@Serializable
data class ProjectMembershipResponse(
    val project_id: String,
    val project_name: String,
    val org_id: String,
    val role: String,
)

@Serializable
data class UserMembershipsResponse(
    val orgs: List<OrgMembershipResponse>,
    val projects: List<ProjectMembershipResponse>,
)

fun User.toResponse(): UserResponse =
    UserResponse(
        id = id,
        email = email,
        display_name = displayName,
        created_at = createdAt.toString(),
    )

fun UserMemberships.toResponse(): UserMembershipsResponse =
    UserMembershipsResponse(
        orgs = orgs.map {
            OrgMembershipResponse(org_id = it.orgId, org_name = it.orgName, role = it.role)
        },
        projects = projects.map {
            ProjectMembershipResponse(
                project_id = it.projectId,
                project_name = it.projectName,
                org_id = it.orgId,
                role = it.role,
            )
        },
    )

fun Route.userRoutes(users: UserStore) = userRoutes { users }

fun Route.userRoutes(users: () -> UserStore) {
    route("/v1/users") {
        post {
            val body = call.receive<CreateUserRequest>()
            val created = users().create(
                email = body.email ?: throw ApiException.BadRequest(
                    "email is required",
                    mapOf("field" to "email"),
                ),
                displayName = body.display_name ?: throw ApiException.BadRequest(
                    "display_name is required",
                    mapOf("field" to "display_name"),
                ),
            )
            call.respond(HttpStatusCode.Created, created.toResponse())
        }
        get {
            val email = call.request.queryParameters["email"]
            if (email != null) {
                val user = users().findByEmail(email)
                    ?: throw ApiException.NotFound("user not found", mapOf("email" to email))
                call.respond(listOf(user.toResponse()))
                return@get
            }
            call.respond(users().list().map { it.toResponse() })
        }
        get("{userId}") {
            val userId = call.parameters["userId"]
                ?: throw ApiException.BadRequest("userId is required")
            call.respond(users().get(userId).toResponse())
        }
        get("{userId}/memberships") {
            val userId = call.parameters["userId"]
                ?: throw ApiException.BadRequest("userId is required")
            call.respond(users().memberships(userId).toResponse())
        }
    }
}
