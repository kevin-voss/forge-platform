package forge.control.reconcile

import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.uuid
import forge.control.repo.withConnection
import java.time.Instant
import java.util.UUID
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicLong
import javax.sql.DataSource

/** One append-only status transition for a deployment. */
data class DeploymentEvent(
    val id: Long,
    val deploymentId: UUID,
    val at: Instant,
    val fromStatus: String,
    val toStatus: String,
    val image: String? = null,
    val desiredReplicas: Int? = null,
    val actualReplicas: Int? = null,
    val reason: String? = null,
)

/** Read/append seam for deployment history (07.05). */
interface DeploymentHistory {
    fun append(event: DeploymentEvent): DeploymentEvent
    fun listByDeploymentId(deploymentId: UUID): List<DeploymentEvent>
    fun latest(deploymentId: UUID): DeploymentEvent?
}

class JdbcDeploymentHistory(
    private val dataSource: DataSource,
) : DeploymentHistory {
    override fun append(event: DeploymentEvent): DeploymentEvent = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO deployment_events (
                    deployment_id, at, from_status, to_status, image,
                    desired_replicas, actual_replicas, reason
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                RETURNING id
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, event.deploymentId)
                ps.setTimestamp(2, java.sql.Timestamp.from(event.at))
                ps.setString(3, event.fromStatus)
                ps.setString(4, event.toStatus)
                ps.setString(5, event.image)
                if (event.desiredReplicas != null) ps.setInt(6, event.desiredReplicas) else ps.setObject(6, null)
                if (event.actualReplicas != null) ps.setInt(7, event.actualReplicas) else ps.setObject(7, null)
                ps.setString(8, event.reason)
                ps.executeQuery().use { rs ->
                    require(rs.next()) { "deployment_events insert returned no id" }
                    event.copy(id = rs.getLong("id"))
                }
            }
        }
    }

    override fun listByDeploymentId(deploymentId: UUID): List<DeploymentEvent> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, deployment_id, at, from_status, to_status, image,
                       desired_replicas, actual_replicas, reason
                FROM deployment_events
                WHERE deployment_id = ?
                ORDER BY at ASC, id ASC
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, deploymentId)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(mapRow(rs))
                    }
                }
            }
        }
    }

    override fun latest(deploymentId: UUID): DeploymentEvent? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, deployment_id, at, from_status, to_status, image,
                       desired_replicas, actual_replicas, reason
                FROM deployment_events
                WHERE deployment_id = ?
                ORDER BY at DESC, id DESC
                LIMIT 1
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, deploymentId)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapRow(rs) else null
                }
            }
        }
    }

    private fun mapRow(rs: java.sql.ResultSet): DeploymentEvent =
        DeploymentEvent(
            id = rs.getLong("id"),
            deploymentId = rs.uuid("deployment_id"),
            at = rs.instant("at"),
            fromStatus = rs.getString("from_status"),
            toStatus = rs.getString("to_status"),
            image = rs.getString("image"),
            desiredReplicas = rs.getObject("desired_replicas") as? Int,
            actualReplicas = rs.getObject("actual_replicas") as? Int,
            reason = rs.getString("reason"),
        )
}

/** In-memory history for unit tests. */
class InMemoryDeploymentHistory : DeploymentHistory {
    private val seq = AtomicLong(0)
    private val events = CopyOnWriteArrayList<DeploymentEvent>()

    override fun append(event: DeploymentEvent): DeploymentEvent {
        val stored = event.copy(id = if (event.id > 0) event.id else seq.incrementAndGet())
        events.add(stored)
        return stored
    }

    override fun listByDeploymentId(deploymentId: UUID): List<DeploymentEvent> =
        events.filter { it.deploymentId == deploymentId }.sortedWith(compareBy({ it.at }, { it.id }))

    override fun latest(deploymentId: UUID): DeploymentEvent? =
        listByDeploymentId(deploymentId).lastOrNull()

    fun all(): List<DeploymentEvent> = events.toList()

    fun clear() = events.clear()
}
