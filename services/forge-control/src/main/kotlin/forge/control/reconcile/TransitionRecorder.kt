package forge.control.reconcile

import forge.control.logging.JsonLog
import forge.control.repo.runSql
import forge.control.repo.withConnection
import forge.control.telemetry.Telemetry
import java.time.Clock
import java.time.Instant
import java.util.UUID
import java.util.concurrent.atomic.AtomicBoolean
import javax.sql.DataSource

/**
 * Single choke point for status changes + history append (07.05).
 * Status update and history row happen in one DB transaction when JDBC-backed.
 */
interface TransitionRecorder {
    fun transition(
        deploymentId: UUID,
        to: DeploymentLifecycle,
        from: DeploymentLifecycle? = null,
        image: String? = null,
        desiredReplicas: Int? = null,
        actualReplicas: Int? = null,
        reason: String,
    ): DeploymentEvent?
}

class JdbcTransitionRecorder(
    private val dataSource: DataSource,
    private val history: DeploymentHistory,
    private val log: JsonLog,
    private val enabled: Boolean = true,
    private val clock: Clock = Clock.systemUTC(),
    private val telemetry: Telemetry = Telemetry.current(),
) : TransitionRecorder {
    override fun transition(
        deploymentId: UUID,
        to: DeploymentLifecycle,
        from: DeploymentLifecycle?,
        image: String?,
        desiredReplicas: Int?,
        actualReplicas: Int?,
        reason: String,
    ): DeploymentEvent? = runSql {
        dataSource.withConnection { conn ->
            val previousAutoCommit = conn.autoCommit
            conn.autoCommit = false
            try {
                val currentStatus = conn.prepareStatement(
                    "SELECT status, image, desired_replicas FROM deployments WHERE id = ?",
                ).use { ps ->
                    ps.setObject(1, deploymentId)
                    ps.executeQuery().use { rs ->
                        if (!rs.next()) return@use null
                        Triple(
                            rs.getString("status"),
                            rs.getString("image"),
                            rs.getInt("desired_replicas"),
                        )
                    }
                } ?: return@withConnection null

                val fromLifecycle = from ?: DeploymentLifecycle.parse(currentStatus.first)
                if (fromLifecycle == to) {
                    conn.commit()
                    return@withConnection null
                }

                val resolvedImage = image ?: currentStatus.second
                val resolvedDesired = desiredReplicas ?: currentStatus.third
                val at = Instant.now(clock)

                conn.prepareStatement(
                    "UPDATE deployments SET status = ?, updated_at = ? WHERE id = ?",
                ).use { ps ->
                    ps.setString(1, to.wire())
                    ps.setTimestamp(2, java.sql.Timestamp.from(at))
                    ps.setObject(3, deploymentId)
                    if (ps.executeUpdate() == 0) {
                        conn.rollback()
                        return@withConnection null
                    }
                }

                var event: DeploymentEvent? = null
                if (enabled) {
                    conn.prepareStatement(
                        """
                        INSERT INTO deployment_events (
                            deployment_id, at, from_status, to_status, image,
                            desired_replicas, actual_replicas, reason
                        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                        RETURNING id
                        """.trimIndent(),
                    ).use { ps ->
                        ps.setObject(1, deploymentId)
                        ps.setTimestamp(2, java.sql.Timestamp.from(at))
                        ps.setString(3, fromLifecycle.wire())
                        ps.setString(4, to.wire())
                        ps.setString(5, resolvedImage)
                        ps.setInt(6, resolvedDesired)
                        if (actualReplicas != null) ps.setInt(7, actualReplicas) else ps.setObject(7, null)
                        ps.setString(8, reason)
                        ps.executeQuery().use { rs ->
                            require(rs.next()) { "deployment_events insert returned no id" }
                            event = DeploymentEvent(
                                id = rs.getLong("id"),
                                deploymentId = deploymentId,
                                at = at,
                                fromStatus = fromLifecycle.wire(),
                                toStatus = to.wire(),
                                image = resolvedImage,
                                desiredReplicas = resolvedDesired,
                                actualReplicas = actualReplicas,
                                reason = reason,
                            )
                        }
                    }
                }

                conn.commit()
                val recorded = event
                if (recorded != null) {
                    log.info(
                        "deployment transition",
                        "deployment_id" to deploymentId.toString(),
                        "event_id" to recorded.id,
                        "from" to recorded.fromStatus,
                        "to" to recorded.toStatus,
                        "image" to (recorded.image ?: ""),
                        "reason" to reason,
                    )
                    telemetry.recordDeploymentTransition(recorded.toStatus)
                }
                recorded
            } catch (e: Exception) {
                conn.rollback()
                throw e
            } finally {
                conn.autoCommit = previousAutoCommit
            }
        }
    }
}

/**
 * In-memory recorder for unit/integration tests.
 * Optionally fails after status write to prove rollback of both sides.
 */
class InMemoryTransitionRecorder(
    private val deploymentStore: DeploymentStore,
    private val history: DeploymentHistory,
    private val log: JsonLog? = null,
    private val enabled: Boolean = true,
    private val clock: Clock = Clock.systemUTC(),
    private val telemetry: Telemetry = Telemetry.current(),
) : TransitionRecorder {
    private val failNext = AtomicBoolean(false)

    fun failNextTransition() {
        failNext.set(true)
    }

    override fun transition(
        deploymentId: UUID,
        to: DeploymentLifecycle,
        from: DeploymentLifecycle?,
        image: String?,
        desiredReplicas: Int?,
        actualReplicas: Int?,
        reason: String,
    ): DeploymentEvent? {
        val current = deploymentStore.getStatus(deploymentId)
        val fromLifecycle = from ?: DeploymentLifecycle.parse(current)
        if (fromLifecycle == to) return null

        val desired = deploymentStore.findDesired(deploymentId)
        val at = Instant.now(clock)
        val previousStatus = current

        // Stage status first — on failure, restore previous so both sides roll back.
        deploymentStore.setStatus(deploymentId, to.wire())
        if (failNext.getAndSet(false)) {
            if (previousStatus != null) {
                deploymentStore.setStatus(deploymentId, previousStatus)
            }
            throw IllegalStateException("forced transition failure")
        }

        if (!enabled) return null

        val event = history.append(
            DeploymentEvent(
                id = 0,
                deploymentId = deploymentId,
                at = at,
                fromStatus = fromLifecycle.wire(),
                toStatus = to.wire(),
                image = image ?: desired?.image,
                desiredReplicas = desiredReplicas ?: desired?.replicas,
                actualReplicas = actualReplicas,
                reason = reason,
            ),
        )
        log?.info(
            "deployment transition",
            "deployment_id" to deploymentId.toString(),
            "event_id" to event.id,
            "from" to event.fromStatus,
            "to" to event.toStatus,
            "image" to (event.image ?: ""),
            "reason" to reason,
        )
        telemetry.recordDeploymentTransition(event.toStatus)
        return event
    }
}

/** No-op recorder used when history wiring is absent (keeps status-only path). */
class StatusOnlyTransitionRecorder(
    private val deploymentStore: DeploymentStore,
) : TransitionRecorder {
    override fun transition(
        deploymentId: UUID,
        to: DeploymentLifecycle,
        from: DeploymentLifecycle?,
        image: String?,
        desiredReplicas: Int?,
        actualReplicas: Int?,
        reason: String,
    ): DeploymentEvent? {
        val current = DeploymentLifecycle.parse(deploymentStore.getStatus(deploymentId))
        if (current == to) return null
        deploymentStore.setStatus(deploymentId, to.wire())
        return null
    }
}
