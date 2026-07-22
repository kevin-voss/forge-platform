package forge.control.auth

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import io.ktor.server.application.Application
import io.ktor.server.application.ApplicationCallPipeline
import io.ktor.server.application.call
import io.ktor.server.request.httpMethod
import io.ktor.server.request.path
import io.ktor.util.AttributeKey
import java.util.concurrent.atomic.AtomicLong

data class AuthPrincipal(
    val type: String,
    val id: String,
    val role: String? = null,
    val projectId: String? = null,
)

val AuthPrincipalKey = AttributeKey<AuthPrincipal>("forge.auth.principal")

data class AuthMetrics(
    val ok: AtomicLong = AtomicLong(0),
    val unauthorized: AtomicLong = AtomicLong(0),
    val forbidden: AtomicLong = AtomicLong(0),
    val unavailable: AtomicLong = AtomicLong(0),
)

class AuthMiddleware(
    private val authMode: String,
    private val identity: IdentityClient,
    private val routes: RouteActionMap,
    private val log: JsonLog? = null,
    val metrics: AuthMetrics = AuthMetrics(),
) {
    val isDevBypass: Boolean = authMode.equals("dev", ignoreCase = true)

    fun enforce(method: String, path: String, authorizationHeader: String?): AuthPrincipal? {
        if (isDevBypass) {
            return AuthPrincipal(type = "dev", id = "dev")
        }

        val target = routes.resolve(method, path)
        if (target is AuthTarget.Skip) {
            return null
        }

        val token = parseBearer(authorizationHeader)
        if (token == null) {
            metrics.unauthorized.incrementAndGet()
            throw unauthenticated("missing Authorization bearer token")
        }

        val introspect = try {
            identity.introspect(token)
        } catch (e: IdentityUnreachableException) {
            metrics.unavailable.incrementAndGet()
            log?.warn(
                "identity unreachable during introspect",
                "path" to path,
                "method" to method,
                "error" to (e.message ?: "unreachable"),
            )
            throw ApiException.ServiceUnavailable(
                "identity unavailable",
                details = mapOf("reason" to "identity_unreachable"),
            )
        }

        if (!introspect.active) {
            metrics.unauthorized.incrementAndGet()
            throw unauthenticated("inactive or unknown token")
        }

        val principalType = introspect.principal_type?.trim().orEmpty()
        val principalId = introspect.principal_id?.trim().orEmpty()
        if (principalType.isEmpty() || principalId.isEmpty()) {
            metrics.unauthorized.incrementAndGet()
            throw unauthenticated("token missing principal")
        }

        val principal = AuthPrincipal(
            type = principalType,
            id = principalId,
            role = introspect.role,
            projectId = introspect.project_id,
        )

        when (target) {
            AuthTarget.Skip -> Unit
            AuthTarget.AuthenticateOnly -> {
                metrics.ok.incrementAndGet()
            }
            is AuthTarget.Authorize -> {
                val decision = try {
                    identity.checkAuthz(
                        principalType = principalType,
                        principalId = principalId,
                        projectId = target.projectId,
                        action = target.action,
                    )
                } catch (e: IdentityUnreachableException) {
                    metrics.unavailable.incrementAndGet()
                    log?.warn(
                        "identity unreachable during authz",
                        "principal" to principalId,
                        "action" to target.action,
                        "project" to target.projectId,
                        "error" to (e.message ?: "unreachable"),
                    )
                    throw ApiException.ServiceUnavailable(
                        "identity unavailable",
                        details = mapOf("reason" to "identity_unreachable"),
                    )
                }

                if (!decision.allow) {
                    metrics.forbidden.incrementAndGet()
                    log?.info(
                        "authz denied",
                        "principal" to principalId,
                        "action" to target.action,
                        "project" to target.projectId,
                        "decision" to "deny",
                        "role" to decision.role,
                        "reason" to decision.reason,
                    )
                    throw ApiException.Forbidden(
                        "forbidden",
                        details = mapOf(
                            "required_action" to target.action,
                            "role" to decision.role,
                        ),
                    )
                }
                metrics.ok.incrementAndGet()
            }
        }

        return principal
    }

    companion object {
        fun unauthenticated(message: String): ApiException.Unauthorized =
            ApiException.Unauthorized(message = message, code = "unauthenticated")

        fun parseBearer(header: String?): String? {
            if (header.isNullOrBlank()) return null
            val parts = header.trim().split(Regex("\\s+"), limit = 2)
            if (parts.size != 2) return null
            if (!parts[0].equals("Bearer", ignoreCase = true)) return null
            return parts[1].trim().ifEmpty { null }
        }
    }
}

fun Application.installAuthMiddleware(middleware: AuthMiddleware) {
    // Call phase runs after StatusPages is wired so ApiException maps to the envelope.
    intercept(ApplicationCallPipeline.Call) {
        val method = call.request.httpMethod.value
        val path = call.request.path()
        val principal = middleware.enforce(
            method = method,
            path = path,
            authorizationHeader = call.request.headers["Authorization"],
        )
        if (principal != null) {
            call.attributes.put(AuthPrincipalKey, principal)
        }
        proceed()
    }
}
