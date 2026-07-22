package forge.identity.project

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
data class CreateProjectRequest(
    val id: String? = null,
    val org_id: String? = null,
    val name: String? = null,
)

@Serializable
data class ProjectResponse(
    val id: String,
    val org_id: String,
    val name: String,
)

@Serializable
data class AddProjectMemberRequest(
    val user_id: String? = null,
    val role: String? = null,
)

@Serializable
data class ProjectMembershipResponse(
    val project_id: String,
    val user_id: String,
    val role: String,
)

fun IdentityProject.toResponse(): ProjectResponse =
    ProjectResponse(id = id, org_id = orgId, name = name)

fun ProjectMembership.toResponse(): ProjectMembershipResponse =
    ProjectMembershipResponse(project_id = projectId, user_id = userId, role = role)

fun Route.projectRoutes(projects: ProjectMembershipStore) = projectRoutes { projects }

fun Route.projectRoutes(projects: () -> ProjectMembershipStore) {
    route("/v1/projects") {
        post {
            val body = call.receive<CreateProjectRequest>()
            val created = projects().createProject(
                id = body.id ?: throw ApiException.BadRequest(
                    "id is required",
                    mapOf("field" to "id"),
                ),
                orgId = body.org_id ?: throw ApiException.BadRequest(
                    "org_id is required",
                    mapOf("field" to "org_id"),
                ),
                name = body.name ?: throw ApiException.BadRequest(
                    "name is required",
                    mapOf("field" to "name"),
                ),
            )
            call.respond(HttpStatusCode.Created, created.toResponse())
        }
        get {
            val orgId = call.request.queryParameters["org_id"]
            if (orgId != null) {
                call.respond(projects().listProjects(orgId).map { it.toResponse() })
            } else {
                call.respond(projects().listProjects().map { it.toResponse() })
            }
        }
        get("{projectId}") {
            val projectId = call.parameters["projectId"]
                ?: throw ApiException.BadRequest("projectId is required")
            call.respond(projects().getProject(projectId).toResponse())
        }
        post("{projectId}/members") {
            val projectId = call.parameters["projectId"]
                ?: throw ApiException.BadRequest("projectId is required")
            val body = call.receive<AddProjectMemberRequest>()
            val membership = projects().addMember(
                projectId = projectId,
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
        delete("{projectId}/members/{userId}") {
            val projectId = call.parameters["projectId"]
                ?: throw ApiException.BadRequest("projectId is required")
            val userId = call.parameters["userId"]
                ?: throw ApiException.BadRequest("userId is required")
            projects().removeMember(projectId, userId)
            call.respond(HttpStatusCode.NoContent)
        }
    }
}
