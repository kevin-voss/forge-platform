package forge.control.reconcile

import java.time.Clock
import java.time.Duration
import java.time.Instant

enum class ReadinessOutcome {
    Ready,
    NotReady,
    TimedOut,
    Unreachable,
}

data class ReadinessCheck(
    val outcome: ReadinessOutcome,
    val status: String? = null,
    val waitedMs: Long = 0,
)

/**
 * Polls Runtime workload health until ready, not-ready, or max wait exceeded.
 * Controller ticks use [checkOnce]; tests may use [awaitReady].
 */
class ReadinessGate(
    private val runtimeClient: RuntimeClient,
    private val pollMs: Long = 1_000,
    private val maxWaitSeconds: Long = 60,
    private val clock: Clock = Clock.systemUTC(),
    private val sleeper: (Long) -> Unit = { ms -> Thread.sleep(ms) },
) {
    fun isReady(runtimeDeploymentId: String): Boolean =
        checkOnce(runtimeDeploymentId).outcome == ReadinessOutcome.Ready

    fun checkOnce(runtimeDeploymentId: String): ReadinessCheck {
        return try {
            val handle = runtimeClient.findWorkload(runtimeDeploymentId)
                ?: return ReadinessCheck(ReadinessOutcome.NotReady, status = null)
            val status = runCatching { ReplicaStatus.parse(handle.status) }.getOrNull()
            when (status) {
                ReplicaStatus.Ready -> ReadinessCheck(ReadinessOutcome.Ready, status = status.wire())
                else -> ReadinessCheck(ReadinessOutcome.NotReady, status = handle.status)
            }
        } catch (_: RuntimeUnreachableException) {
            ReadinessCheck(ReadinessOutcome.Unreachable)
        }
    }

    /**
     * Blocks until ready, max wait, or unreachable. Used by unit tests and
     * optional synchronous callers — the reconcile loop must not block the
     * shared scheduler on this path.
     */
    fun awaitReady(runtimeDeploymentId: String): ReadinessCheck {
        val started = Instant.now(clock)
        val deadline = started.plusSeconds(maxWaitSeconds.coerceAtLeast(0))
        while (true) {
            val check = checkOnce(runtimeDeploymentId)
            val waited = Duration.between(started, Instant.now(clock)).toMillis()
            when (check.outcome) {
                ReadinessOutcome.Ready ->
                    return check.copy(waitedMs = waited)
                ReadinessOutcome.Unreachable ->
                    return check.copy(waitedMs = waited)
                ReadinessOutcome.NotReady, ReadinessOutcome.TimedOut -> {
                    if (!Instant.now(clock).isBefore(deadline)) {
                        return ReadinessCheck(
                            outcome = ReadinessOutcome.TimedOut,
                            status = check.status,
                            waitedMs = waited,
                        )
                    }
                    sleeper(pollMs.coerceAtLeast(1))
                }
            }
        }
    }
}
