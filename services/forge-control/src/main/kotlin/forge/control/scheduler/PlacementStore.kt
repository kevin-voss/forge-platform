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
    val nodeId: String,
    val strategy: String,
    val reason: String?,
    val createdAt: Instant,
)

interface PlacementStore {
    /** Idempotent upsert on (deployment_id, replica_index); returns existing row on conflict. */
    fun upsert(placement: Placement): Placement

    fun find(deploymentId: UUID, replicaIndex: Int): Placement?

    fun listByDeployment(deploymentId: UUID): List<Placement>

    /**
     * Delete a placement row (capacity release is caller's responsibility via
     * [CapacityReservation]). Returns the deleted row, or null if absent.
     * Hook for stop/reschedule (08.05).
     */
    fun delete(deploymentId: UUID, replicaIndex: Int): Placement?
}

class JdbcPlacementStore(
    private val dataSource: DataSource,
) : PlacementStore {
    override fun upsert(placement: Placement): Placement = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO placements (
                    id, deployment_id, replica_index, node_id, strategy, reason, created_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?)
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
                ps.executeUpdate()
            }
            find(conn, placement.deploymentId, placement.replicaIndex)
                ?: error("placement missing after upsert")
        }
    }

    override fun find(deploymentId: UUID, replicaIndex: Int): Placement? = runSql {
        dataSource.withConnection { conn -> find(conn, deploymentId, replicaIndex) }
    }

    override fun listByDeployment(deploymentId: UUID): List<Placement> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, deployment_id, replica_index, node_id, strategy, reason, created_at
                FROM placements
                WHERE deployment_id = ?
                ORDER BY replica_index
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, deploymentId)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) {
                            add(
                                Placement(
                                    id = rs.getString("id"),
                                    deploymentId = rs.uuid("deployment_id"),
                                    replicaIndex = rs.getInt("replica_index"),
                                    nodeId = rs.getString("node_id"),
                                    strategy = rs.getString("strategy"),
                                    reason = rs.getString("reason"),
                                    createdAt = rs.instant("created_at"),
                                ),
                            )
                        }
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

    private fun find(
        conn: java.sql.Connection,
        deploymentId: UUID,
        replicaIndex: Int,
    ): Placement? {
        conn.prepareStatement(
            """
            SELECT id, deployment_id, replica_index, node_id, strategy, reason, created_at
            FROM placements
            WHERE deployment_id = ? AND replica_index = ?
            """.trimIndent(),
        ).use { ps ->
            ps.setObject(1, deploymentId)
            ps.setInt(2, replicaIndex)
            ps.executeQuery().use { rs ->
                if (!rs.next()) return null
                return Placement(
                    id = rs.getString("id"),
                    deploymentId = rs.uuid("deployment_id"),
                    replicaIndex = rs.getInt("replica_index"),
                    nodeId = rs.getString("node_id"),
                    strategy = rs.getString("strategy"),
                    reason = rs.getString("reason"),
                    createdAt = rs.instant("created_at"),
                )
            }
        }
    }
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

    override fun listByDeployment(deploymentId: UUID): List<Placement> =
        rows.values
            .filter { it.deploymentId == deploymentId }
            .sortedBy { it.replicaIndex }

    override fun delete(deploymentId: UUID, replicaIndex: Int): Placement? =
        rows.remove(key(deploymentId, replicaIndex))
}
