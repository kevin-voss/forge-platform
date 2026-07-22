package forge.control.scheduler

import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.withConnection
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.time.Instant
import java.util.concurrent.ConcurrentHashMap
import javax.sql.DataSource

@Serializable
data class NodeCapacity(
    val slots: Int,
    @SerialName("cpu_millis") val cpuMillis: Int? = null,
    @SerialName("mem_mb") val memMb: Int? = null,
)

@Serializable
data class NodeAllocation(
    val slots: Int = 0,
    @SerialName("cpu_millis") val cpuMillis: Int? = null,
    @SerialName("mem_mb") val memMb: Int? = null,
    @SerialName("running_replicas") val runningReplicas: List<String> = emptyList(),
)

data class FleetNode(
    val id: String,
    val address: String,
    val capacity: NodeCapacity,
    val allocation: NodeAllocation,
    val status: String,
    val lastHeartbeatAt: Instant,
    val registeredAt: Instant,
)

interface NodeStore {
    /** Idempotent upsert by node id; refreshes address/capacity and marks online. */
    fun register(
        id: String,
        address: String,
        capacity: NodeCapacity,
        at: Instant = Instant.now(),
    ): FleetNode

    fun heartbeat(
        id: String,
        allocation: NodeAllocation,
        at: Instant = Instant.now(),
    ): FleetNode?

    fun find(id: String): FleetNode?

    fun list(): List<FleetNode>

    fun listOnlineIds(): List<String>

    /** Mark nodes with last_heartbeat older than cutoff as offline; returns transitioned ids. */
    fun markStaleOffline(cutoff: Instant): List<String>

    /** Recompute online/offline for every row from last_heartbeat vs cutoff. */
    fun recomputeLiveness(cutoff: Instant): List<Pair<String, String>>
}

private val json = Json {
    encodeDefaults = true
    ignoreUnknownKeys = true
    explicitNulls = false
}

class JdbcNodeStore(
    private val dataSource: DataSource,
) : NodeStore {
    override fun register(
        id: String,
        address: String,
        capacity: NodeCapacity,
        at: Instant,
    ): FleetNode = runSql {
        dataSource.withConnection { conn ->
            val capacityJson = json.encodeToString(capacity)
            conn.prepareStatement(
                """
                INSERT INTO nodes (
                    id, address, capacity_json, allocation_json, status, last_heartbeat_at, registered_at
                ) VALUES (?, ?, ?::jsonb, '{}'::jsonb, 'online', ?, ?)
                ON CONFLICT (id) DO UPDATE SET
                    address = EXCLUDED.address,
                    capacity_json = EXCLUDED.capacity_json,
                    status = 'online',
                    last_heartbeat_at = EXCLUDED.last_heartbeat_at
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.setString(2, address)
                ps.setString(3, capacityJson)
                ps.setTimestamp(4, java.sql.Timestamp.from(at))
                ps.setTimestamp(5, java.sql.Timestamp.from(at))
                ps.executeUpdate()
            }
            find(conn, id) ?: error("node missing after register")
        }
    }

    override fun heartbeat(
        id: String,
        allocation: NodeAllocation,
        at: Instant,
    ): FleetNode? = runSql {
        dataSource.withConnection { conn ->
            val allocationJson = json.encodeToString(allocation)
            val updated = conn.prepareStatement(
                """
                UPDATE nodes
                SET allocation_json = ?::jsonb,
                    last_heartbeat_at = ?,
                    status = 'online'
                WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, allocationJson)
                ps.setTimestamp(2, java.sql.Timestamp.from(at))
                ps.setString(3, id)
                ps.executeUpdate()
            }
            if (updated == 0) return@withConnection null
            find(conn, id)
        }
    }

    override fun find(id: String): FleetNode? = runSql {
        dataSource.withConnection { conn -> find(conn, id) }
    }

    override fun list(): List<FleetNode> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, address, capacity_json, allocation_json, status,
                       last_heartbeat_at, registered_at
                FROM nodes
                ORDER BY id
                """.trimIndent(),
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(mapRow(rs))
                    }
                }
            }
        }
    }

    override fun listOnlineIds(): List<String> =
        list().filter { it.status == "online" }.map { it.id }

    override fun markStaleOffline(cutoff: Instant): List<String> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE nodes
                SET status = 'offline'
                WHERE status = 'online'
                  AND last_heartbeat_at < ?
                RETURNING id
                """.trimIndent(),
            ).use { ps ->
                ps.setTimestamp(1, java.sql.Timestamp.from(cutoff))
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(rs.getString("id"))
                    }
                }
            }
        }
    }

    override fun recomputeLiveness(cutoff: Instant): List<Pair<String, String>> = runSql {
        dataSource.withConnection { conn ->
            val transitions = mutableListOf<Pair<String, String>>()
            conn.prepareStatement(
                """
                UPDATE nodes
                SET status = 'offline'
                WHERE last_heartbeat_at < ?
                  AND status <> 'offline'
                RETURNING id
                """.trimIndent(),
            ).use { ps ->
                ps.setTimestamp(1, java.sql.Timestamp.from(cutoff))
                ps.executeQuery().use { rs ->
                    while (rs.next()) transitions.add(rs.getString("id") to "offline")
                }
            }
            conn.prepareStatement(
                """
                UPDATE nodes
                SET status = 'online'
                WHERE last_heartbeat_at >= ?
                  AND status = 'offline'
                RETURNING id
                """.trimIndent(),
            ).use { ps ->
                ps.setTimestamp(1, java.sql.Timestamp.from(cutoff))
                ps.executeQuery().use { rs ->
                    while (rs.next()) transitions.add(rs.getString("id") to "online")
                }
            }
            transitions
        }
    }

    private fun find(conn: java.sql.Connection, id: String): FleetNode? {
        conn.prepareStatement(
            """
            SELECT id, address, capacity_json, allocation_json, status,
                   last_heartbeat_at, registered_at
            FROM nodes
            WHERE id = ?
            """.trimIndent(),
        ).use { ps ->
            ps.setString(1, id)
            ps.executeQuery().use { rs ->
                if (!rs.next()) return null
                return mapRow(rs)
            }
        }
    }

    private fun mapRow(rs: java.sql.ResultSet): FleetNode {
        val capacityRaw = rs.getString("capacity_json")
        val allocationRaw = rs.getString("allocation_json")
        return FleetNode(
            id = rs.getString("id"),
            address = rs.getString("address"),
            capacity = json.decodeFromString(NodeCapacity.serializer(), capacityRaw),
            allocation = if (allocationRaw.isNullOrBlank() || allocationRaw == "{}") {
                NodeAllocation()
            } else {
                json.decodeFromString(NodeAllocation.serializer(), allocationRaw)
            },
            status = rs.getString("status"),
            lastHeartbeatAt = rs.instant("last_heartbeat_at"),
            registeredAt = rs.instant("registered_at"),
        )
    }
}

/** In-memory store for unit tests. */
class InMemoryNodeStore : NodeStore {
    private val rows = ConcurrentHashMap<String, FleetNode>()

    override fun register(
        id: String,
        address: String,
        capacity: NodeCapacity,
        at: Instant,
    ): FleetNode {
        val existing = rows[id]
        val node = FleetNode(
            id = id,
            address = address,
            capacity = capacity,
            allocation = existing?.allocation ?: NodeAllocation(),
            status = "online",
            lastHeartbeatAt = at,
            registeredAt = existing?.registeredAt ?: at,
        )
        rows[id] = node
        return node
    }

    override fun heartbeat(
        id: String,
        allocation: NodeAllocation,
        at: Instant,
    ): FleetNode? {
        val existing = rows[id] ?: return null
        val updated = existing.copy(
            allocation = allocation,
            status = "online",
            lastHeartbeatAt = at,
        )
        rows[id] = updated
        return updated
    }

    override fun find(id: String): FleetNode? = rows[id]

    override fun list(): List<FleetNode> = rows.values.sortedBy { it.id }

    override fun listOnlineIds(): List<String> =
        list().filter { it.status == "online" }.map { it.id }

    override fun markStaleOffline(cutoff: Instant): List<String> {
        val transitioned = mutableListOf<String>()
        rows.replaceAll { id, node ->
            if (node.status == "online" && node.lastHeartbeatAt.isBefore(cutoff)) {
                transitioned.add(id)
                node.copy(status = "offline")
            } else {
                node
            }
        }
        return transitioned
    }

    override fun recomputeLiveness(cutoff: Instant): List<Pair<String, String>> {
        val transitions = mutableListOf<Pair<String, String>>()
        rows.replaceAll { id, node ->
            val shouldBeOnline = !node.lastHeartbeatAt.isBefore(cutoff)
            when {
                shouldBeOnline && node.status == "offline" -> {
                    transitions.add(id to "online")
                    node.copy(status = "online")
                }
                !shouldBeOnline && node.status != "offline" -> {
                    transitions.add(id to "offline")
                    node.copy(status = "offline")
                }
                else -> node
            }
        }
        return transitions
    }
}
