package forge.control.scheduler

import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.withConnection
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList
import javax.sql.DataSource

data class PreemptionEvent(
    val id: String,
    val victimPlacementId: String,
    val preemptorPlacementId: String,
    val victimPriority: Int,
    val preemptorPriority: Int,
    val nodeId: String,
    val reason: String,
    val createdAt: Instant,
    val victimDeploymentId: UUID? = null,
)

interface PreemptionAuditor {
    fun record(event: PreemptionEvent): PreemptionEvent

    fun list(
        deploymentId: UUID? = null,
        limit: Int = 100,
    ): List<PreemptionEvent>
}

class JdbcPreemptionAuditor(
    private val dataSource: DataSource,
    private val idFactory: () -> String = {
        "pev_${UUID.randomUUID().toString().replace("-", "").take(12)}"
    },
) : PreemptionAuditor {
    override fun record(event: PreemptionEvent): PreemptionEvent = runSql {
        val id = event.id.ifBlank { idFactory() }
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO preemption_events(
                    id, victim_placement_id, preemptor_placement_id,
                    victim_priority, preemptor_priority, node_id, reason, created_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.setString(2, event.victimPlacementId)
                ps.setString(3, event.preemptorPlacementId)
                ps.setInt(4, event.victimPriority)
                ps.setInt(5, event.preemptorPriority)
                ps.setString(6, event.nodeId)
                ps.setString(7, event.reason)
                ps.setTimestamp(8, Timestamp.from(event.createdAt))
                ps.executeUpdate()
            }
            event.copy(id = id)
        }
    }

    override fun list(deploymentId: UUID?, limit: Int): List<PreemptionEvent> = runSql {
        dataSource.withConnection { conn ->
            val sql = if (deploymentId != null) {
                """
                SELECT e.id, e.victim_placement_id, e.preemptor_placement_id,
                       e.victim_priority, e.preemptor_priority, e.node_id, e.reason, e.created_at,
                       v.deployment_id AS victim_deployment_id
                FROM preemption_events e
                LEFT JOIN placements v ON v.id = e.victim_placement_id
                WHERE v.deployment_id = ?
                   OR EXISTS (
                       SELECT 1 FROM placements p
                       WHERE p.id = e.preemptor_placement_id
                         AND p.deployment_id = ?
                   )
                ORDER BY e.created_at DESC
                LIMIT ?
                """.trimIndent()
            } else {
                """
                SELECT e.id, e.victim_placement_id, e.preemptor_placement_id,
                       e.victim_priority, e.preemptor_priority, e.node_id, e.reason, e.created_at,
                       v.deployment_id AS victim_deployment_id
                FROM preemption_events e
                LEFT JOIN placements v ON v.id = e.victim_placement_id
                ORDER BY e.created_at DESC
                LIMIT ?
                """.trimIndent()
            }
            conn.prepareStatement(sql).use { ps ->
                if (deploymentId != null) {
                    ps.setObject(1, deploymentId)
                    ps.setObject(2, deploymentId)
                    ps.setInt(3, limit.coerceAtLeast(1))
                } else {
                    ps.setInt(1, limit.coerceAtLeast(1))
                }
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) {
                            add(
                                PreemptionEvent(
                                    id = rs.getString("id"),
                                    victimPlacementId = rs.getString("victim_placement_id"),
                                    preemptorPlacementId = rs.getString("preemptor_placement_id"),
                                    victimPriority = rs.getInt("victim_priority"),
                                    preemptorPriority = rs.getInt("preemptor_priority"),
                                    nodeId = rs.getString("node_id"),
                                    reason = rs.getString("reason"),
                                    createdAt = rs.instant("created_at"),
                                    victimDeploymentId = runCatching {
                                        rs.getObject("victim_deployment_id") as? UUID
                                    }.getOrNull(),
                                ),
                            )
                        }
                    }
                }
            }
        }
    }
}

class InMemoryPreemptionAuditor(
    private val idFactory: () -> String = {
        "pev_${UUID.randomUUID().toString().replace("-", "").take(12)}"
    },
) : PreemptionAuditor {
    private val events = CopyOnWriteArrayList<PreemptionEvent>()
    private val byId = ConcurrentHashMap<String, PreemptionEvent>()

    override fun record(event: PreemptionEvent): PreemptionEvent {
        val id = event.id.ifBlank { idFactory() }
        val stored = event.copy(id = id)
        byId[id] = stored
        events += stored
        return stored
    }

    override fun list(deploymentId: UUID?, limit: Int): List<PreemptionEvent> =
        events
            .asReversed()
            .filter {
                deploymentId == null ||
                    it.victimDeploymentId == deploymentId
            }
            .take(limit.coerceAtLeast(1))
}
