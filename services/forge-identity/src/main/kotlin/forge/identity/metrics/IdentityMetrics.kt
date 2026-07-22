package forge.identity.metrics

import java.util.concurrent.atomic.AtomicLong

/** In-process counters for identity tenancy (OTEL export lands with observe epic). */
object IdentityMetrics {
    val usersTotal = AtomicLong(0)
    val orgsTotal = AtomicLong(0)

    fun recordUserCreated() {
        usersTotal.incrementAndGet()
    }

    fun recordOrgCreated() {
        orgsTotal.incrementAndGet()
    }
}
