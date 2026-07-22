package forge.identity.token

import forge.identity.auth.IntrospectResponse
import forge.identity.metrics.IdentityMetrics
import java.time.Instant

/**
 * Resolves opaque API tokens to principal + project + role (09.05).
 * Session tokens continue to be handled by [forge.identity.auth.AuthService].
 */
class TokenIntrospector(
    private val tokens: TokenStore,
) {
    /**
     * Introspect an API token. Returns null when [token] is not an API token
     * (so callers can fall through to session lookup).
     */
    fun introspect(token: String, now: Instant = Instant.now()): IntrospectResponse? {
        if (!TokenStore.looksLikeApiToken(token)) return null
        val record = tokens.findByToken(token) ?: return IntrospectResponse(active = false)
        if (!record.isActive(now)) {
            IdentityMetrics.recordIntrospect(principalType = record.ownerType, active = false)
            return IntrospectResponse(active = false)
        }
        IdentityMetrics.recordIntrospect(principalType = record.ownerType, active = true)
        return IntrospectResponse(
            active = true,
            principal_type = record.ownerType,
            principal_id = record.ownerId,
            user_id = record.ownerId.takeIf { record.ownerType == "user" },
            project_id = record.projectId,
            role = record.role,
        )
    }
}
