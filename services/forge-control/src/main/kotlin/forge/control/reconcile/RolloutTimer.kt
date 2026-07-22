package forge.control.reconcile

import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.concurrent.ConcurrentHashMap

/**
 * Tracks elapsed rollout time vs [timeoutSeconds] with an injectable [clock]
 * so tests can advance time deterministically.
 */
class RolloutTimer(
    private val clock: Clock = Clock.systemUTC(),
) {
    private val startedAt = ConcurrentHashMap<String, Instant>()

    fun start(deploymentId: String, at: Instant = Instant.now(clock)) {
        startedAt.putIfAbsent(deploymentId, at)
    }

    fun markStarted(deploymentId: String, at: Instant) {
        startedAt[deploymentId] = at
    }

    fun startedAt(deploymentId: String): Instant? = startedAt[deploymentId]

    fun elapsed(deploymentId: String): Duration {
        val start = startedAt[deploymentId] ?: return Duration.ZERO
        return Duration.between(start, Instant.now(clock)).coerceAtLeast(Duration.ZERO)
    }

    fun isTimedOut(deploymentId: String, timeoutSeconds: Int): Boolean {
        if (timeoutSeconds < 1) return false
        if (startedAt[deploymentId] == null) return false
        return elapsed(deploymentId).seconds >= timeoutSeconds.toLong()
    }

    fun clear(deploymentId: String) {
        startedAt.remove(deploymentId)
    }
}
