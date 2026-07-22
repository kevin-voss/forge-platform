package forge.control.scheduler

import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.uuid
import forge.control.repo.withConnection
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import javax.sql.DataSource

data class Placement(
    val id: String,
    val deploymentId: UUID,
    val replicaIndex: Int,
    val nodeId: String?,
    val strategy: String,
    val reason: String?,
    val createdAt: Instant,
    val status: String = PendingQueue.STATUS_PLACED,
    val antiAffinity: String = "soft",
    val slots: Int = 1,
    val serviceId: String? = null,
)

interface PlacementStore {
    /** Idempotent upsert on (deployment_id, replica_index); returns existing row on conflict. */
    fun upsert(placement: Placement): Placement

    fun find(deploymentId: UUID, replicaIndex: Int): Placement?

    fun listByDeployment(deploymentId: UUID, status: String? = null): List<Placement>

    /**
     * Delete a placement row (capacity release is caller's responsibility via
     * [CapacityReservation]). Returns the deleted row, or null if absent.
     */
    fun delete(deploymentId: UUID, replicaIndex: Int): Placement?

    /** Node ids that already host a placed replica of [serviceId]. */
    fun nodeIdsWithPlacedService(serviceId: String): Set<String>

    fun listPendingFifo(limit: Int = 1000): List<Placement>

    fun countPending(): Int

    /** Promote a pending row to placed after a successful scheduler decision. */
    fun markPlaced(
        deploymentId: UUID,
        replicaIndex: Int,
        nodeId: String,
        strategy: String,
        reason: String?,
    ): Placement?
}

class JdbcPlacementStore(
    private val dataSource: DataSource,
) : PlacementStore {
    override fun upsert(placement: Placement): Placement = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO placements (
                    id, deployment_id, replica_index, node_id, strategy, reason, created_at,
                    status, anti_affinity, slots, service_id
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT (deployment_id, replica_index) DO NOTHING
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, placement.id)
                ps.setObject(2, placement.deploymentId)
                ps.setInt(3, placement.replicaIndex)
                ps.setString(4, placement.nodeId)
                ps.setString(5, placement.strategy)
                ps.setString(6, placement.reason)
                ps.setTimestamp(7, java.sql.Timestamp.from(placement.createdAt))
                ps.setString(8, placement.status)
                ps.setString(9, placement.antiAffinity)
                ps.setInt(10, placement.slots)
                ps.setString(11, placement.serviceId)
                ps.executeUpdate()
            }
            find(conn, placement.deploymentId, placement.replicaIndex)
                ?: error("placement missing after upsert")
        }
    }

    override fun find(deploymentId: UUID, replicaIndex: Int): Placement? = runSql {
        dataSource.withConnection { conn -> find(conn, deploymentId, replicaIndex) }
    }

    override fun listByDeployment(deploymentId: UUID, status: String?): List<Placement> = runSql {
        dataSource.withConnection { conn ->
            val sql = buildString {
                append(
                    """
                    SELECT id, deployment_id, replica_index, node_id, strategy, reason, created_at,
                           status, anti_affinity, slots, service_id
                    FROM placements
                    WHERE deployment_id = ?
                    """.trimIndent(),
                )
                if (status != null) append(" AND status = ?")
                append(" ORDER BY replica_index")
            }
            conn.prepareStatement(sql).use { ps ->
                ps.setObject(1, deploymentId)
                if (status != null) ps.setString(2, status)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(readPlacement(rs))
                    }
                }
            }
        }
    }

    override fun delete(deploymentId: UUID, replicaIndex: Int): Placement? = runSql {
        dataSource.withConnection { conn ->
            val existing = find(conn, deploymentId, replicaIndex) ?: return@withConnection null
            conn.prepareStatement(
                """
                DELETE FROM placements
                WHERE deployment_id = ? AND replica_index = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, deploymentId)
                ps.setInt(2, replicaIndex)
                ps.executeUpdate()
            }
            existing
        }
    }

    override fun nodeIdsWithPlacedService(serviceId: String): Set<String> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT DISTINCT node_id
                FROM placements
                WHERE status = 'placed'
                  AND node_id IS NOT NULL
                  AND service_id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, serviceId)
                ps.executeQuery().use { rs ->
                    buildSet {
                        while (rs.next()) {
                            rs.getString("node_id")?.let { add(it) }
                        }
                    }
                }
            }
        }
    }

    override fun listPendingFifo(limit: Int): List<Placement> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, deployment_id, replica_index, node_id, strategy, reason, created_at,
                       status, anti_affinity, slots, service_id
                FROM placements
                WHERE status = 'pending'
                ORDER BY created_at ASC, replica_index ASC
                LIMIT ?
                """.trimIndent(),
            ).use { ps ->
                ps.setInt(1, limit.coerceAtLeast(1))
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(readPlacement(rs))
                    }
                }
            }
        }
    }

    override fun countPending(): Int = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                "SELECT COUNT(*) FROM placements WHERE status = 'pending'",
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    if (rs.next()) rs.getInt(1) else 0
                }
            }
        }
    }

    override fun markPlaced(
        deploymentId: UUID,
        replicaIndex: Int,
        nodeId: String,
        strategy: String,
        reason: String?,
    ): Placement? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE placements
                SET node_id = ?, strategy = ?, reason = ?, status = 'placed'
                WHERE deployment_id = ? AND replica_index = ? AND status = 'pending'
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, nodeId)
                ps.setString(2, strategy)
                ps.setString(3, reason)
                ps.setObject(4, deploymentId)
                ps.setInt(5, replicaIndex)
                if (ps.executeUpdate() == 0) return@withConnection null
            }
            find(conn, deploymentId, replicaIndex)
        }
    }

    private fun find(
        conn: java.sql.Connection,
        deploymentId: UUID,
        replicaIndex: Int,
    ): Placement? {
        conn.prepareStatement(
            """
            SELECT id, deployment_id, replica_index, node_id, strategy, reason, created_at,
                   status, anti_affinity, slots, service_id
            FROM placements
            WHERE deployment_id = ? AND replica_index = ?
            """.trimIndent(),
        ).use { ps ->
            ps.setObject(1, deploymentId)
            ps.setInt(2, replicaIndex)
            ps.executeQuery().use { rs ->
                if (!rs.next()) return null
                return readPlacement(rs)
            }
        }
    }

    private fun readPlacement(rs: java.sql.ResultSet): Placement =
        Placement(
            id = rs.getString("id"),
            deploymentId = rs.uuid("deployment_id"),
            replicaIndex = rs.getInt("replica_index"),
            nodeId = rs.getString("node_id"),
            strategy = rs.getString("strategy"),
            reason = rs.getString("reason"),
            createdAt = rs.instant("created_at"),
            status = rs.getString("status") ?: PendingQueue.STATUS_PLACED,
            antiAffinity = rs.getString("anti_affinity") ?: "soft",
            slots = rs.getInt("slots").takeIf { !rs.wasNull() } ?: 1,
            serviceId = rs.getString("service_id"),
        )
}

/** In-memory store for unit tests. */
class InMemoryPlacementStore : PlacementStore {
    private val rows = ConcurrentHashMap<String, Placement>()

    private fun key(deploymentId: UUID, replicaIndex: Int): String =
        "$deploymentId:$replicaIndex"

    override fun upsert(placement: Placement): Placement {
        val k = key(placement.deploymentId, placement.replicaIndex)
        return rows.compute(k) { _, existing -> existing ?: placement }!!
    }

    override fun find(deploymentId: UUID, replicaIndex: Int): Placement? =
        rows[key(deploymentId, replicaIndex)]

    override fun listByDeployment(deploymentId: UUID, status: String?): List<Placement> =
        rows.values
            .filter { it.deploymentId == deploymentId }
            .filter { status == null || it.status == status }
            .sortedBy { it.replicaIndex }

    override fun delete(deploymentId: UUID, replicaIndex: Int): Placement? =
        rows.remove(key(deploymentId, replicaIndex))

    override fun nodeIdsWithPlacedService(serviceId: String): Set<String> =
        rows.values
            .filter {
                it.status == PendingQueue.STATUS_PLACED &&
                    it.serviceId == serviceId &&
                    !it.nodeId.isNullOrBlank()
            }
            .mapNotNullTo(mutableSetOf()) { it.nodeId }

    override fun listPendingFifo(limit: Int): List<Placement> =
        rows.values
            .filter { it.status == PendingQueue.STATUS_PENDING }
            .sortedWith(compareBy({ it.createdAt }, { it.replicaIndex }))
            .take(limit.coerceAtLeast(1))

    override fun countPending(): Int =
        rows.values.count { it.status == PendingQueue.STATUS_PENDING }

    override fun markPlaced(
        deploymentId: UUID,
        replicaIndex: Int,
        nodeId: String,
        strategy: String,
        reason: String?,
    ): Placement? {
        val k = key(deploymentId, replicaIndex)
        val existing = rows[k] ?: return null
        if (existing.status != PendingQueue.STATUS_PENDING) return null
        val updated = existing.copy(
            nodeId = nodeId,
            strategy = strategy,
            reason = reason,
            status = PendingQueue.STATUS_PLACED,
        )
        rows[k] = updated
        return updated
    }
}
