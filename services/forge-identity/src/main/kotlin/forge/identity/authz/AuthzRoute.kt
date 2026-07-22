package forge.identity.authz

import forge.identity.http.ApiException
import forge.identity.logging.JsonLog
import forge.identity.metrics.IdentityMetrics
import forge.identity.org.OrgStore
import forge.identity.project.ProjectMembershipStore
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import kotlinx.serialization.Serializable

@Serializable
data class AuthzPrincipal(
    val type: String? = null,
    val id: String? = null,
)

@Serializable
data class AuthzCheckRequest(
    val principal: AuthzPrincipal? = null,
    val project_id: String? = null,
    val action: String? = null,
)

@Serializable
data class AuthzCheckResponse(
    val allow: Boolean,
    val role: String,
    val reason: String,
)

@Serializable
data class AuthzMatrixResponse(
    val version: String,
    val matrix: Map<String, List<String>>,
)

class AuthzService(
    private val resolver: RoleResolver,
    private val matrix: PermissionMatrix,
    private val log: JsonLog? = null,
) {
    companion object {
        fun create(
            projects: ProjectMembershipStore,
            orgs: OrgStore,
            matrix: PermissionMatrix,
            log: JsonLog? = null,
        ): AuthzService =
            AuthzService(
                resolver = RoleResolver(projects, orgs),
                matrix = matrix,
                log = log,
            )
    }

    fun check(principal: PrincipalRef, projectId: String, action: String): AuthzCheckResponse {
        val normalizedAction = action.trim()
        if (normalizedAction.isEmpty()) {
            throw ApiException.BadRequest("action must not be blank", mapOf("field" to "action"))
        }

        val role = resolver.resolve(principal, projectId)
        IdentityMetrics.recordAuthzCheck()

        if (!matrix.isKnownAction(normalizedAction)) {
            IdentityMetrics.recordAuthzDeny(normalizedAction)
            logDenial(principal, projectId, normalizedAction, role, "unknown action")
            return AuthzCheckResponse(
                allow = false,
                role = role.wire,
                reason = "unknown action",
            )
        }

        if (role == Role.NONE) {
            IdentityMetrics.recordAuthzDeny(normalizedAction)
            logDenial(principal, projectId, normalizedAction, role, "no membership")
            return AuthzCheckResponse(
                allow = false,
                role = Role.NONE.wire,
                reason = "no membership in project",
            )
        }

        val allow = matrix.allows(normalizedAction, role)
        if (allow) {
            IdentityMetrics.recordAuthzAllow()
            return AuthzCheckResponse(
                allow = true,
                role = role.wire,
                reason = "${role.wire} may $normalizedAction",
            )
        }

        IdentityMetrics.recordAuthzDeny(normalizedAction)
        val reason = "${role.wire} may not $normalizedAction"
        logDenial(principal, projectId, normalizedAction, role, reason)
        return AuthzCheckResponse(
            allow = false,
            role = role.wire,
            reason = reason,
        )
    }

    fun publishedMatrix(): AuthzMatrixResponse =
        AuthzMatrixResponse(
            version = matrix.version,
            matrix = matrix.toWireMap(),
        )

    private fun logDenial(
        principal: PrincipalRef,
        projectId: String,
        action: String,
        role: Role,
        reason: String,
    ) {
        log?.info(
            "authz denial",
            "principal_type" to principal.type,
            "principal_id" to principal.id,
            "project_id" to projectId,
            "action" to action,
            "role" to role.wire,
            "reason" to reason,
        )
    }
}

fun Route.authzRoutes(authz: AuthzService) = authzRoutes { authz }

fun Route.authzRoutes(authz: () -> AuthzService) {
    route("/v1/authz") {
        post("check") {
            val body = call.receive<AuthzCheckRequest>()
            val principalBody = body.principal
                ?: throw ApiException.BadRequest("principal is required", mapOf("field" to "principal"))
            val type = principalBody.type?.trim().orEmpty()
            if (type.isEmpty()) {
                throw ApiException.BadRequest(
                    "principal.type is required",
                    mapOf("field" to "principal.type"),
                )
            }
            val id = principalBody.id?.trim().orEmpty()
            if (id.isEmpty()) {
                throw ApiException.BadRequest(
                    "principal.id is required",
                    mapOf("field" to "principal.id"),
                )
            }
            val projectId = body.project_id?.trim().orEmpty()
            if (projectId.isEmpty()) {
                throw ApiException.BadRequest(
                    "project_id is required",
                    mapOf("field" to "project_id"),
                )
            }
            val action = body.action?.trim().orEmpty()
            if (action.isEmpty()) {
                throw ApiException.BadRequest("action is required", mapOf("field" to "action"))
            }
            val decision = authz().check(
                principal = PrincipalRef(type = type, id = id),
                projectId = projectId,
                action = action,
            )
            call.respond(HttpStatusCode.OK, decision)
        }
        get("matrix") {
            call.respond(HttpStatusCode.OK, authz().publishedMatrix())
        }
    }
}
