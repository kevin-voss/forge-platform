package forge.identity.metrics

import java.util.concurrent.atomic.AtomicLong

/** In-process counters for identity tenancy (OTEL export lands with observe epic). */
object IdentityMetrics {
    val usersTotal = AtomicLong(0)
    val orgsTotal = AtomicLong(0)
    val loginSuccessTotal = AtomicLong(0)
    val loginFailTotal = AtomicLong(0)
    val activeSessions = AtomicLong(0)
    val sessionsCreatedTotal = AtomicLong(0)
    val sessionsRevokedTotal = AtomicLong(0)
    val authzChecksTotal = AtomicLong(0)
    val authzAllowsTotal = AtomicLong(0)
    val authzDeniesTotal = AtomicLong(0)
    val activeTokens = AtomicLong(0)
    val tokensCreatedTotal = AtomicLong(0)
    val tokenRevocationsTotal = AtomicLong(0)
    val introspectTotal = AtomicLong(0)
    val introspectUserTotal = AtomicLong(0)
    val introspectServiceAccountTotal = AtomicLong(0)

    fun recordUserCreated() {
        usersTotal.incrementAndGet()
    }

    fun recordOrgCreated() {
        orgsTotal.incrementAndGet()
    }

    fun recordLogin(success: Boolean) {
        if (success) loginSuccessTotal.incrementAndGet() else loginFailTotal.incrementAndGet()
    }

    fun recordSessionCreated() {
        sessionsCreatedTotal.incrementAndGet()
    }

    fun recordSessionRevoked() {
        sessionsRevokedTotal.incrementAndGet()
    }

    fun setActiveSessions(count: Long) {
        activeSessions.set(count)
    }

    fun recordAuthzCheck() {
        authzChecksTotal.incrementAndGet()
    }

    fun recordAuthzAllow() {
        authzAllowsTotal.incrementAndGet()
    }

    fun recordAuthzDeny(@Suppress("UNUSED_PARAMETER") action: String) {
        authzDeniesTotal.incrementAndGet()
    }

    fun recordTokenCreated() {
        tokensCreatedTotal.incrementAndGet()
    }

    fun recordTokenRevoked() {
        tokenRevocationsTotal.incrementAndGet()
    }

    fun setActiveTokens(count: Long) {
        activeTokens.set(count)
    }

    fun recordIntrospect(principalType: String, @Suppress("UNUSED_PARAMETER") active: Boolean) {
        introspectTotal.incrementAndGet()
        when (principalType) {
            "user" -> introspectUserTotal.incrementAndGet()
            "service_account" -> introspectServiceAccountTotal.incrementAndGet()
        }
    }
}
