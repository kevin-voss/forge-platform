package forge.identity.auth

import forge.identity.config.AuthConfig
import forge.identity.db.StoreException
import forge.identity.db.runSql
import forge.identity.db.withConnection
import forge.identity.http.ApiException
import forge.identity.logging.JsonLog
import forge.identity.metrics.IdentityMetrics
import forge.identity.user.UserMemberships
import forge.identity.user.UserStore
import forge.identity.user.toResponse
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.header
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import kotlinx.serialization.Serializable
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource

@Serializable
data class RegisterRequest(
    val email: String? = null,
    val password: String? = null,
    val display_name: String? = null,
)

@Serializable
data class RegisterResponse(
    val user_id: String,
)

@Serializable
data class LoginRequest(
    val email: String? = null,
    val password: String? = null,
)

@Serializable
data class LoginResponse(
    val session_token: String,
    val expires_at: String,
)

@Serializable
data class IntrospectRequest(
    val token: String? = null,
)

@Serializable
data class IntrospectMemberships(
    val orgs: List<forge.identity.user.OrgMembershipResponse>,
    val projects: List<forge.identity.user.ProjectMembershipResponse>,
)

@Serializable
data class IntrospectResponse(
    val active: Boolean,
    val principal_type: String? = null,
    val user_id: String? = null,
    val memberships: IntrospectMemberships? = null,
)

class AuthService(
    private val dataSource: DataSource,
    private val users: UserStore,
    private val credentials: CredentialStore,
    private val sessions: SessionStore,
    private val hasher: PasswordHasher,
    private val authConfig: AuthConfig,
    private val log: JsonLog? = null,
) {
    /** Precomputed Argon2id hash used to equalize timing when the user is unknown. */
    private val dummyHash: String by lazy { hasher.hash("forge-identity-timing-dummy") }

    fun register(email: String, password: String, displayName: String): String {
        val normalizedEmail = email.trim()
        val normalizedName = displayName.trim()
        validatePassword(password)
        if (normalizedEmail.isEmpty()) {
            throw ApiException.BadRequest("email must not be blank", mapOf("field" to "email"))
        }
        if (!normalizedEmail.contains('@')) {
            throw ApiException.BadRequest("email must be a valid address", mapOf("field" to "email"))
        }
        if (normalizedName.isEmpty()) {
            throw ApiException.BadRequest(
                "display_name must not be blank",
                mapOf("field" to "display_name"),
            )
        }

        val passwordHash = hasher.hash(password)
        val userId = UUID.randomUUID().toString()
        val now = Instant.now()

        try {
            runSql {
                dataSource.withConnection { conn ->
                    conn.autoCommit = false
                    try {
                        conn.prepareStatement(
                            """
                            INSERT INTO users (id, email, display_name, created_at)
                            VALUES (?, ?, ?, ?)
                            """.trimIndent(),
                        ).use { ps ->
                            ps.setString(1, userId)
                            ps.setString(2, normalizedEmail)
                            ps.setString(3, normalizedName)
                            ps.setTimestamp(4, Timestamp.from(now))
                            ps.executeUpdate()
                        }
                        conn.prepareStatement(
                            """
                            INSERT INTO credentials (user_id, hash, updated_at)
                            VALUES (?, ?, ?)
                            """.trimIndent(),
                        ).use { ps ->
                            ps.setString(1, userId)
                            ps.setString(2, passwordHash)
                            ps.setTimestamp(3, Timestamp.from(now))
                            ps.executeUpdate()
                        }
                        conn.commit()
                    } catch (e: Exception) {
                        conn.rollback()
                        throw e
                    } finally {
                        conn.autoCommit = true
                    }
                }
            }
        } catch (_: StoreException.Conflict) {
            throw ApiException.Conflict(
                "email already registered",
                mapOf("email" to normalizedEmail),
            )
        }

        IdentityMetrics.recordUserCreated()
        log?.info(
            "auth event",
            "event" to "register",
            "user_id" to userId,
            "success" to true,
        )
        return userId
    }

    fun login(email: String, password: String): LoginResponse {
        val normalizedEmail = email.trim()
        if (normalizedEmail.isEmpty() || password.isEmpty()) {
            throw unauthorized()
        }

        if (credentials.isLockedOut(
                normalizedEmail,
                authConfig.loginMaxFails,
                authConfig.loginLockoutWindowSeconds,
            )
        ) {
            log?.info(
                "auth event",
                "event" to "login",
                "success" to false,
                "reason" to "lockout",
            )
            throw ApiException.TooManyRequests(
                "too many failed login attempts; try again later",
                mapOf("email" to normalizedEmail),
            )
        }

        val user = users.findByEmail(normalizedEmail)
        val storedHash = user?.let { credentials.findByUserId(it.id)?.hash }
        val hashToVerify = storedHash ?: dummyHash
        val verified = try {
            hasher.verify(hashToVerify, password)
        } catch (e: Exception) {
            log?.warn(
                "auth anomaly",
                "event" to "login",
                "error" to (e.message ?: e.javaClass.simpleName),
            )
            false
        }
        val passwordOk = verified && storedHash != null && user != null

        if (!passwordOk || user == null) {
            credentials.recordLoginAttempt(normalizedEmail, success = false)
            IdentityMetrics.recordLogin(success = false)
            log?.info(
                "auth event",
                "event" to "login",
                "user_id" to (user?.id ?: ""),
                "success" to false,
            )
            throw unauthorized()
        }

        credentials.recordLoginAttempt(normalizedEmail, success = true)
        val created = sessions.create(user.id)
        IdentityMetrics.recordLogin(success = true)
        IdentityMetrics.setActiveSessions(sessions.countActive())
        log?.info(
            "auth event",
            "event" to "login",
            "user_id" to user.id,
            "session_id" to created.session.id,
            "success" to true,
        )
        return LoginResponse(
            session_token = created.token,
            expires_at = created.session.expiresAt.toString(),
        )
    }

    fun introspect(token: String): IntrospectResponse {
        val session = sessions.findByToken(token)
        if (session == null || !session.isActive()) {
            return IntrospectResponse(active = false)
        }
        val memberships = users.memberships(session.userId)
        return IntrospectResponse(
            active = true,
            principal_type = "user",
            user_id = session.userId,
            memberships = memberships.toIntrospect(),
        )
    }

    fun logout(bearerToken: String) {
        val sessionId = sessions.revokeByToken(bearerToken)
        IdentityMetrics.setActiveSessions(sessions.countActive())
        log?.info(
            "auth event",
            "event" to "logout",
            "session_id" to (sessionId ?: "unknown"),
            "success" to true,
        )
    }

    private fun validatePassword(password: String) {
        if (password.length < 8) {
            throw ApiException.BadRequest(
                "password must be at least 8 characters",
                mapOf("field" to "password"),
            )
        }
    }

    private fun unauthorized(): ApiException.Unauthorized =
        ApiException.Unauthorized("invalid credentials")

    private fun UserMemberships.toIntrospect(): IntrospectMemberships {
        val response = toResponse()
        return IntrospectMemberships(orgs = response.orgs, projects = response.projects)
    }
}

fun Route.authRoutes(auth: AuthService) = authRoutes { auth }

fun Route.authRoutes(auth: () -> AuthService) {
    route("/v1/auth") {
        post("register") {
            val body = call.receive<RegisterRequest>()
            val userId = auth().register(
                email = body.email ?: throw ApiException.BadRequest(
                    "email is required",
                    mapOf("field" to "email"),
                ),
                password = body.password ?: throw ApiException.BadRequest(
                    "password is required",
                    mapOf("field" to "password"),
                ),
                displayName = body.display_name ?: throw ApiException.BadRequest(
                    "display_name is required",
                    mapOf("field" to "display_name"),
                ),
            )
            call.respond(HttpStatusCode.Created, RegisterResponse(user_id = userId))
        }
        post("login") {
            val body = call.receive<LoginRequest>()
            val result = auth().login(
                email = body.email ?: throw ApiException.BadRequest(
                    "email is required",
                    mapOf("field" to "email"),
                ),
                password = body.password ?: throw ApiException.BadRequest(
                    "password is required",
                    mapOf("field" to "password"),
                ),
            )
            call.respond(HttpStatusCode.OK, result)
        }
        post("introspect") {
            val body = call.receive<IntrospectRequest>()
            val token = body.token ?: throw ApiException.BadRequest(
                "token is required",
                mapOf("field" to "token"),
            )
            call.respond(HttpStatusCode.OK, auth().introspect(token))
        }
        post("logout") {
            val header = call.request.header("Authorization")
                ?: throw ApiException.Unauthorized("missing Authorization bearer token")
            val token = parseBearer(header)
                ?: throw ApiException.Unauthorized("missing Authorization bearer token")
            auth().logout(token)
            call.respond(HttpStatusCode.NoContent)
        }
    }
}

internal fun parseBearer(header: String): String? {
    val parts = header.trim().split(Regex("\\s+"), limit = 2)
    if (parts.size != 2) return null
    if (!parts[0].equals("Bearer", ignoreCase = true)) return null
    val token = parts[1].trim()
    return token.ifEmpty { null }
}
