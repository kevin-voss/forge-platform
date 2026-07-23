package forge.control.scheduler

import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.uuid
import forge.control.repo.withConnection
import forge.control.scheduler.model.PlacementTrace
import forge.control.scheduler.model.PlatformSpec
import forge.control.scheduler.model.ResourceBundle
import forge.control.scheduler.model.Toleration
import forge.control.scheduler.model.UnschedulableReasonEntry
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import javax.sql.DataSource

private val placementJson = Json {
    encodeDefaults = true
    ignoreUnknownKeys = true
    explicitNulls = false
}

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
    val rescheduledFromNode: String? = null,
    val requests: ResourceBundle? = null,
    val limits: ResourceBundle? = null,
    val trace: PlacementTrace? = null,
    val nodeSelector: Map<String, String>? = null,
    val tolerations: List<Toleration> = emptyList(),
    val platform: PlatformSpec? = null,
) {
    val unschedulableReasons: List<UnschedulableReasonEntry>
        get() = trace?.filters
            ?.flatMap { it.eliminated }
            .orEmpty()
}

interface PlacementStore {
    /** Idempotent upsert of an active (placed|pending) row; returns existing active on conflict. */
    fun upsert(placement: Placement): Placement

    /** Active (placed|pending) placement for (deployment, replica), or null. */
    fun find(deploymentId: UUID, replicaIndex: Int): Placement?

    fun listByDeployment(deploymentId: UUID, status: String? = null): List<Placement>

    /**
     * Delete the active placement row (capacity release is caller's responsibility via
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
        trace: PlacementTrace? = null,
    ): Placement?

    /** Active placement by id, or null. */
    fun findById(id: String): Placement?

    /** Placed (or filtered) placements currently assigned to [nodeId]. */
    fun listByNode(nodeId: String, status: String? = PendingQueue.STATUS_PLACED): List<Placement>

    /** Mark an active placed row as lost (keeps node_id for audit). */
    fun markLost(deploymentId: UUID, replicaIndex: Int): Placement?

    /** Lost placements that have no active replacement row. */
    fun listLostWithoutActive(): List<Placement>
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
                    status, anti_affinity, slots, service_id, rescheduled_from_node,
                    requests_json, limits_json, trace_json,
                    node_selector_json, tolerations_json, platform_json
                ) VALUES (
                    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
                    ?::jsonb, ?::jsonb, ?::jsonb, ?::jsonb, ?::jsonb, ?::jsonb
                )
                ON CONFLICT (deployment_id, replica_index)
                    WHERE status IN ('placed', 'pending')
                DO NOTHING
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
                ps.setString(12, placement.rescheduledFromNode)
                ps.setString(13, placement.requests?.let { placementJson.encodeToString(it) })
                ps.setString(14, placement.limits?.let { placementJson.encodeToString(it) })
                ps.setString(15, placement.trace?.let { placementJson.encodeToString(it) })
                ps.setString(
                    16,
                    placement.nodeSelector?.takeIf { it.isNotEmpty() }?.let {
                        placementJson.encodeToString(
                            MapSerializer(String.serializer(), String.serializer()),
                            it,
                        )
                    },
                )
                ps.setString(
                    17,
                    placementJson.encodeToString(
                        ListSerializer(Toleration.serializer()),
                        placement.tolerations,
                    ),
                )
                ps.setString(18, placement.platform?.let { placementJson.encodeToString(it) })
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
                           status, anti_affinity, slots, service_id, rescheduled_from_node,
                           requests_json, limits_json, trace_json,
                           node_selector_json, tolerations_json, platform_json
                    FROM placements
                    WHERE deployment_id = ?
                    """.trimIndent(),
                )
                if (status != null) append(" AND status = ?")
                append(" ORDER BY replica_index, created_at")
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
                WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, existing.id)
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
                       status, anti_affinity, slots, service_id, rescheduled_from_node,
                       requests_json, limits_json, trace_json,
                       node_selector_json, tolerations_json, platform_json
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
        trace: PlacementTrace?,
    ): Placement? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE placements
                SET node_id = ?, strategy = ?, reason = ?, status = 'placed',
                    trace_json = COALESCE(?::jsonb, trace_json)
                WHERE deployment_id = ? AND replica_index = ? AND status = 'pending'
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, nodeId)
                ps.setString(2, strategy)
                ps.setString(3, reason)
                ps.setString(4, trace?.let { placementJson.encodeToString(it) })
                ps.setObject(5, deploymentId)
                ps.setInt(6, replicaIndex)
                if (ps.executeUpdate() == 0) return@withConnection null
            }
            find(conn, deploymentId, replicaIndex)
        }
    }

    override fun findById(id: String): Placement? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, deployment_id, replica_index, node_id, strategy, reason, created_at,
                       status, anti_affinity, slots, service_id, rescheduled_from_node,
                       requests_json, limits_json, trace_json,
                       node_selector_json, tolerations_json, platform_json
                FROM placements
                WHERE id = ?
                ORDER BY created_at DESC
                LIMIT 1
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) return@withConnection null
                    readPlacement(rs)
                }
            }
        }
    }

    override fun listByNode(nodeId: String, status: String?): List<Placement> = runSql {
        dataSource.withConnection { conn ->
            val sql = buildString {
                append(
                    """
                    SELECT id, deployment_id, replica_index, node_id, strategy, reason, created_at,
                           status, anti_affinity, slots, service_id, rescheduled_from_node,
                           requests_json, limits_json, trace_json,
                           node_selector_json, tolerations_json, platform_json
                    FROM placements
                    WHERE node_id = ?
                    """.trimIndent(),
                )
                if (status != null) append(" AND status = ?")
                append(" ORDER BY deployment_id, replica_index")
            }
            conn.prepareStatement(sql).use { ps ->
                ps.setString(1, nodeId)
                if (status != null) ps.setString(2, status)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(readPlacement(rs))
                    }
                }
            }
        }
    }

    override fun markLost(deploymentId: UUID, replicaIndex: Int): Placement? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE placements
                SET status = 'lost'
                WHERE deployment_id = ? AND replica_index = ? AND status = 'placed'
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, deploymentId)
                ps.setInt(2, replicaIndex)
                if (ps.executeUpdate() == 0) return@withConnection null
            }
            findAny(conn, deploymentId, replicaIndex, PendingQueue.STATUS_LOST)
        }
    }

    override fun listLostWithoutActive(): List<Placement> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT l.id, l.deployment_id, l.replica_index, l.node_id, l.strategy, l.reason,
                       l.created_at, l.status, l.anti_affinity, l.slots, l.service_id,
                       l.rescheduled_from_node, l.requests_json, l.limits_json, l.trace_json,
                       l.node_selector_json, l.tolerations_json, l.platform_json
                FROM placements l
                WHERE l.status = 'lost'
                  AND NOT EXISTS (
                      SELECT 1 FROM placements a
                      WHERE a.deployment_id = l.deployment_id
                        AND a.replica_index = l.replica_index
                        AND a.status IN ('placed', 'pending')
                  )
                ORDER BY l.created_at ASC, l.replica_index ASC
                """.trimIndent(),
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(readPlacement(rs))
                    }
                }
            }
        }
    }

    private fun find(
        conn: java.sql.Connection,
        deploymentId: UUID,
        replicaIndex: Int,
    ): Placement? =
        findAny(conn, deploymentId, replicaIndex, status = null, activeOnly = true)

    private fun findAny(
        conn: java.sql.Connection,
        deploymentId: UUID,
        replicaIndex: Int,
        status: String? = null,
        activeOnly: Boolean = false,
    ): Placement? {
        val sql = buildString {
            append(
                """
                SELECT id, deployment_id, replica_index, node_id, strategy, reason, created_at,
                       status, anti_affinity, slots, service_id, rescheduled_from_node,
                       requests_json, limits_json, trace_json,
                       node_selector_json, tolerations_json, platform_json
                FROM placements
                WHERE deployment_id = ? AND replica_index = ?
                """.trimIndent(),
            )
            when {
                status != null -> append(" AND status = ?")
                activeOnly -> append(" AND status IN ('placed', 'pending')")
            }
            append(" ORDER BY created_at DESC LIMIT 1")
        }
        conn.prepareStatement(sql).use { ps ->
            ps.setObject(1, deploymentId)
            ps.setInt(2, replicaIndex)
            if (status != null) ps.setString(3, status)
            ps.executeQuery().use { rs ->
                if (!rs.next()) return null
                return readPlacement(rs)
            }
        }
    }

    private fun readPlacement(rs: java.sql.ResultSet): Placement {
        val requestsRaw = runCatching { rs.getString("requests_json") }.getOrNull()
        val limitsRaw = runCatching { rs.getString("limits_json") }.getOrNull()
        val traceRaw = runCatching { rs.getString("trace_json") }.getOrNull()
        val selectorRaw = runCatching { rs.getString("node_selector_json") }.getOrNull()
        val tolerationsRaw = runCatching { rs.getString("tolerations_json") }.getOrNull()
        val platformRaw = runCatching { rs.getString("platform_json") }.getOrNull()
        return Placement(
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
            rescheduledFromNode = rs.getString("rescheduled_from_node"),
            requests = decodeBundle(requestsRaw),
            limits = decodeBundle(limitsRaw),
            trace = decodeTrace(traceRaw),
            nodeSelector = decodeSelector(selectorRaw),
            tolerations = decodeTolerations(tolerationsRaw),
            platform = decodePlatform(platformRaw),
        )
    }

    private fun decodeBundle(raw: String?): ResourceBundle? {
        if (raw.isNullOrBlank() || raw == "{}") return null
        return runCatching {
            placementJson.decodeFromString(ResourceBundle.serializer(), raw)
        }.getOrNull()
    }

    private fun decodeTrace(raw: String?): PlacementTrace? {
        if (raw.isNullOrBlank() || raw == "{}") return null
        return runCatching {
            placementJson.decodeFromString(PlacementTrace.serializer(), raw)
        }.getOrNull()
    }

    private fun decodeSelector(raw: String?): Map<String, String>? {
        if (raw.isNullOrBlank() || raw == "{}") return null
        return runCatching {
            placementJson.decodeFromString(
                MapSerializer(String.serializer(), String.serializer()),
                raw,
            )
        }.getOrNull()
    }

    private fun decodeTolerations(raw: String?): List<Toleration> {
        if (raw.isNullOrBlank() || raw == "[]") return emptyList()
        return runCatching {
            placementJson.decodeFromString(ListSerializer(Toleration.serializer()), raw)
        }.getOrDefault(emptyList())
    }

    private fun decodePlatform(raw: String?): PlatformSpec? {
        if (raw.isNullOrBlank() || raw == "{}") return null
        return runCatching {
            placementJson.decodeFromString(PlatformSpec.serializer(), raw)
        }.getOrNull()
    }
}

/** In-memory store for unit tests. */
class InMemoryPlacementStore : PlacementStore {
    private val rows = ConcurrentHashMap<String, Placement>()

    override fun upsert(placement: Placement): Placement {
        find(placement.deploymentId, placement.replicaIndex)?.let { return it }
        rows[placement.id] = placement
        return placement
    }

    override fun find(deploymentId: UUID, replicaIndex: Int): Placement? =
        rows.values.firstOrNull {
            it.deploymentId == deploymentId &&
                it.replicaIndex == replicaIndex &&
                it.status != PendingQueue.STATUS_LOST
        }

    override fun listByDeployment(deploymentId: UUID, status: String?): List<Placement> =
        rows.values
            .filter { it.deploymentId == deploymentId }
            .filter { status == null || it.status == status }
            .sortedWith(compareBy({ it.replicaIndex }, { it.createdAt }))

    override fun delete(deploymentId: UUID, replicaIndex: Int): Placement? {
        val existing = find(deploymentId, replicaIndex) ?: return null
        rows.remove(existing.id)
        return existing
    }

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
        trace: PlacementTrace?,
    ): Placement? {
        val existing = find(deploymentId, replicaIndex) ?: return null
        if (existing.status != PendingQueue.STATUS_PENDING) return null
        val updated = existing.copy(
            nodeId = nodeId,
            strategy = strategy,
            reason = reason,
            status = PendingQueue.STATUS_PLACED,
            trace = trace ?: existing.trace,
        )
        rows[existing.id] = updated
        return updated
    }

    override fun findById(id: String): Placement? = rows[id]

    override fun listByNode(nodeId: String, status: String?): List<Placement> =
        rows.values
            .filter { it.nodeId == nodeId }
            .filter { status == null || it.status == status }
            .sortedWith(compareBy({ it.deploymentId }, { it.replicaIndex }))

    override fun markLost(deploymentId: UUID, replicaIndex: Int): Placement? {
        val existing = find(deploymentId, replicaIndex) ?: return null
        if (existing.status != PendingQueue.STATUS_PLACED) return null
        val lost = existing.copy(status = PendingQueue.STATUS_LOST)
        rows[existing.id] = lost
        return lost
    }

    override fun listLostWithoutActive(): List<Placement> =
        rows.values
            .filter { it.status == PendingQueue.STATUS_LOST }
            .filter { find(it.deploymentId, it.replicaIndex) == null }
            .sortedWith(compareBy({ it.createdAt }, { it.replicaIndex }))
}
