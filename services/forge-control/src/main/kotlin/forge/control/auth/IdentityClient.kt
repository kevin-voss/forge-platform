package forge.control.auth

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import java.util.concurrent.atomic.AtomicLong

@Serializable
data class IntrospectRequest(
    val token: String,
)

@Serializable
data class IntrospectMembershipOrg(
    val org_id: String? = null,
    val role: String? = null,
)

@Serializable
data class IntrospectMembershipProject(
    val project_id: String? = null,
    val role: String? = null,
)

@Serializable
data class IntrospectMemberships(
    val orgs: List<IntrospectMembershipOrg> = emptyList(),
    val projects: List<IntrospectMembershipProject> = emptyList(),
)

@Serializable
data class IntrospectResult(
    val active: Boolean,
    val principal_type: String? = null,
    val principal_id: String? = null,
    val user_id: String? = null,
    val project_id: String? = null,
    val role: String? = null,
    val memberships: IntrospectMemberships? = null,
)

@Serializable
data class AuthzPrincipal(
    val type: String,
    val id: String,
)

@Serializable
data class AuthzCheckRequest(
    val principal: AuthzPrincipal,
    val project_id: String,
    val action: String,
)

@Serializable
data class AuthzDecision(
    val allow: Boolean,
    val role: String,
    val reason: String,
)

/** Fail-closed signal when Identity cannot be reached. */
class IdentityUnreachableException(
    message: String,
    cause: Throwable? = null,
) : Exception(message, cause)

interface IdentityClient {
    fun introspect(token: String): IntrospectResult

    fun checkAuthz(principalType: String, principalId: String, projectId: String, action: String): AuthzDecision

    fun invalidateIntrospection(token: String) {}
}

/**
 * HTTP Identity client with short-TTL introspection + authz caches (09.06).
 */
class HttpIdentityClient(
    private val identityUrl: String,
    private val introspectCache: IntrospectionCache,
    private val authzCache: AuthzCache,
    private val httpClient: HttpClient = HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(2))
        .build(),
    private val json: Json = Json { ignoreUnknownKeys = true },
    private val requestTimeout: Duration = Duration.ofSeconds(3),
) : IdentityClient {
    private val base = identityUrl.trimEnd('/')
    private val authzCacheHits = AtomicLong(0)
    private val introspectCalls = AtomicLong(0)

    fun authzCacheHits(): Long = authzCacheHits.get()

    fun introspectCalls(): Long = introspectCalls.get()

    override fun introspect(token: String): IntrospectResult {
        introspectCache.get(token)?.let { return it }
        introspectCalls.incrementAndGet()
        val body = json.encodeToString(IntrospectRequest.serializer(), IntrospectRequest(token))
        val response = postJson("$base/v1/auth/introspect", body)
        val result = try {
            json.decodeFromString(IntrospectResult.serializer(), response)
        } catch (e: Exception) {
            throw IdentityUnreachableException("identity introspect decode failed: ${e.message}", e)
        }
        introspectCache.put(token, result)
        return result
    }

    override fun checkAuthz(
        principalType: String,
        principalId: String,
        projectId: String,
        action: String,
    ): AuthzDecision {
        val cacheKey = AuthzCache.key(principalType, principalId, projectId, action)
        authzCache.get(cacheKey)?.let {
            authzCacheHits.incrementAndGet()
            return it
        }
        val body = json.encodeToString(
            AuthzCheckRequest.serializer(),
            AuthzCheckRequest(
                principal = AuthzPrincipal(type = principalType, id = principalId),
                project_id = projectId,
                action = action,
            ),
        )
        val response = postJson("$base/v1/authz/check", body)
        val decision = try {
            json.decodeFromString(AuthzDecision.serializer(), response)
        } catch (e: Exception) {
            throw IdentityUnreachableException("identity authz decode failed: ${e.message}", e)
        }
        authzCache.put(cacheKey, decision)
        return decision
    }

    override fun invalidateIntrospection(token: String) {
        introspectCache.invalidate(token)
    }

    private fun postJson(url: String, body: String): String {
        val request = HttpRequest.newBuilder()
            .uri(URI.create(url))
            .timeout(requestTimeout)
            .header("content-type", "application/json")
            .header("accept", "application/json")
            .POST(HttpRequest.BodyPublishers.ofString(body))
            .build()
        val response = try {
            httpClient.send(request, HttpResponse.BodyHandlers.ofString())
        } catch (e: Exception) {
            throw IdentityUnreachableException("identity unreachable: ${e.message}", e)
        }
        if (response.statusCode() !in 200..299) {
            throw IdentityUnreachableException(
                "identity HTTP ${response.statusCode()}: ${response.body().take(200)}",
            )
        }
        return response.body()
    }
}

/** Deterministic fake for unit tests. */
class FakeIdentityClient(
    private val introspectByToken: MutableMap<String, IntrospectResult> = mutableMapOf(),
    private val decisions: MutableMap<String, AuthzDecision> = mutableMapOf(),
    var unreachable: Boolean = false,
) : IdentityClient {
    var introspectCalls: Int = 0
        private set
    var authzCalls: Int = 0
        private set

    fun stubIntrospect(token: String, result: IntrospectResult) {
        introspectByToken[token] = result
    }

    fun stubAuthz(
        principalType: String,
        principalId: String,
        projectId: String,
        action: String,
        decision: AuthzDecision,
    ) {
        decisions[AuthzCache.key(principalType, principalId, projectId, action)] = decision
    }

    override fun introspect(token: String): IntrospectResult {
        if (unreachable) throw IdentityUnreachableException("identity unreachable")
        introspectCalls++
        return introspectByToken[token] ?: IntrospectResult(active = false)
    }

    override fun checkAuthz(
        principalType: String,
        principalId: String,
        projectId: String,
        action: String,
    ): AuthzDecision {
        if (unreachable) throw IdentityUnreachableException("identity unreachable")
        authzCalls++
        return decisions[AuthzCache.key(principalType, principalId, projectId, action)]
            ?: AuthzDecision(allow = false, role = "none", reason = "no stub")
    }
}
