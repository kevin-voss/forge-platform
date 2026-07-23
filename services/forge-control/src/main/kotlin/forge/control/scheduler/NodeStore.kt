package forge.control.scheduler

import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.withConnection
import forge.control.scheduler.model.NodeTaint
import forge.control.scheduler.model.ResourceRequirements
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.time.Instant
import java.util.concurrent.ConcurrentHashMap
import javax.sql.DataSource

/** Optional scheduling identity supplied at node registration (25.02). */
data class NodeSchedulingFacts(
    val agentLabels: Map<String, String> = emptyMap(),
    val poolLabels: Map<String, String> = emptyMap(),
    val taints: List<NodeTaint> = emptyList(),
    val architecture: String = "amd64",
    val os: String = "linux",
    val provider: String = "unknown",
) {
    /** True when the agent sent no operator labels/taints (safe to preserve prior). */
    fun lacksOperatorIdentity(): Boolean =
        agentLabels.isEmpty() && poolLabels.isEmpty() && taints.isEmpty()

    fun withPreservedOperatorIdentity(node: FleetNode): NodeSchedulingFacts =
        copy(
            agentLabels = node.labels.filterKeys { !it.startsWith("forge.dev/") },
            taints = node.taints,
            provider = if (provider == "unknown") {
                node.labels[NodeLabelMerger.LABEL_PROVIDER] ?: "unknown"
            } else {
                provider
            },
        )
}

@Serializable
data class NodeCapacity(
    val slots: Int,
    @SerialName("cpu_millis") val cpuMillis: Int? = null,
    @SerialName("mem_mb") val memMb: Int? = null,
    @SerialName("disk_mb") val diskMb: Int? = null,
)

@Serializable
data class NodeAllocation(
    val slots: Int = 0,
    @SerialName("cpu_millis") val cpuMillis: Int? = null,
    @SerialName("mem_mb") val memMb: Int? = null,
    @SerialName("disk_mb") val diskMb: Int? = null,
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
    val wireguardPublicKey: String? = null,
    val networkCidr: String? = null,
    val networkGateway: String? = null,
    val joinedAt: Instant? = null,
    val keyRevokedAt: Instant? = null,
    val allocatable: NodeCapacity? = null,
    val reserved: NodeReserved = NodeReserved(),
    val labels: Map<String, String> = emptyMap(),
    val taints: List<NodeTaint> = emptyList(),
    val architecture: String = "amd64",
    val os: String = "linux",
) {
    val keyRevoked: Boolean get() = keyRevokedAt != null
}

interface NodeStore {
    /** Idempotent upsert by node id; refreshes address/capacity and marks online. */
    fun register(
        id: String,
        address: String,
        capacity: NodeCapacity,
        at: Instant = Instant.now(),
        reserved: NodeReserved = NodeReserved(),
        facts: NodeSchedulingFacts = NodeSchedulingFacts(),
    ): FleetNode

    /**
     * Join-path upsert: sets status + WireGuard/network fields without forcing `online`.
     * Used by [NodeJoinOrchestrator] for pending-network → joining.
     */
    fun registerJoin(
        id: String,
        address: String,
        capacity: NodeCapacity,
        status: String,
        wireguardPublicKey: String?,
        networkCidr: String?,
        networkGateway: String?,
        joinedAt: Instant?,
        at: Instant = Instant.now(),
        clearKeyRevocation: Boolean = false,
        reserved: NodeReserved = NodeReserved(),
        facts: NodeSchedulingFacts = NodeSchedulingFacts(),
    ): FleetNode

    fun heartbeat(
        id: String,
        allocation: NodeAllocation,
        at: Instant = Instant.now(),
    ): FleetNode?

    /** Evict a joined node: clear WireGuard key, mark key revoked, set offline. */
    fun revokeKey(id: String, at: Instant = Instant.now()): FleetNode?

    fun find(id: String): FleetNode?

    fun list(): List<FleetNode>

    fun listOnlineIds(): List<String>

    /** Mark nodes with last_heartbeat older than cutoff as offline; returns transitioned ids. */
    fun markStaleOffline(cutoff: Instant): List<String>

    /** Force a node offline (idempotent). Used before reschedule so it cannot receive replacements. */
    fun markOffline(id: String): Boolean

    /** Recompute online/offline for every row from last_heartbeat vs cutoff. */
    fun recomputeLiveness(cutoff: Instant): List<Pair<String, String>>

    /**
     * Atomically bump allocation when the node is online and has enough free
     * capacity. Returns false if the node is missing, offline, or over-committed.
     */
    fun tryReserve(id: String, requirements: ResourceRequirements): Boolean

    /** Decrement reserved allocation (floors at zero). Hook for stop/reschedule. */
    fun release(id: String, requirements: ResourceRequirements): Boolean
}

private val json = Json {
    encodeDefaults = true
    ignoreUnknownKeys = true
    explicitNulls = false
}

class JdbcNodeStore(
    private val dataSource: DataSource,
    private val overcommit: OvercommitConfig = OvercommitConfig(),
) : NodeStore {
    override fun register(
        id: String,
        address: String,
        capacity: NodeCapacity,
        at: Instant,
        reserved: NodeReserved,
        facts: NodeSchedulingFacts,
    ): FleetNode = runSql {
        dataSource.withConnection { conn ->
            val capacityJson = json.encodeToString(capacity)
            val reservedJson = json.encodeToString(reserved)
            val allocatableJson = json.encodeToString(
                CapacityAccounting.allocatable(capacity, reserved, overcommit),
            )
            val existing = find(conn, id)
            val effective = if (facts.lacksOperatorIdentity() && existing != null) {
                facts.withPreservedOperatorIdentity(existing)
            } else {
                facts
            }
            val arch = effective.architecture.ifBlank { "amd64" }
            val os = effective.os.ifBlank { "linux" }
            val merged = NodeLabelMerger.merge(
                nodeId = id,
                architecture = arch,
                os = os,
                provider = effective.provider,
                poolLabels = effective.poolLabels,
                agentLabels = effective.agentLabels,
            )
            val labelsJson = json.encodeToString(
                MapSerializer(String.serializer(), String.serializer()),
                merged.labels,
            )
            val taintsJson = json.encodeToString(
                ListSerializer(NodeTaint.serializer()),
                effective.taints,
            )
            conn.prepareStatement(
                """
                INSERT INTO nodes (
                    id, address, capacity_json, allocation_json, status, last_heartbeat_at, registered_at,
                    allocatable_json, reserved_json, labels_json, taints_json, architecture, os
                ) VALUES (?, ?, ?::jsonb, '{}'::jsonb, 'online', ?, ?, ?::jsonb, ?::jsonb, ?::jsonb, ?::jsonb, ?, ?)
                ON CONFLICT (id) DO UPDATE SET
                    address = EXCLUDED.address,
                    capacity_json = EXCLUDED.capacity_json,
                    status = 'online',
                    last_heartbeat_at = EXCLUDED.last_heartbeat_at,
                    allocatable_json = EXCLUDED.allocatable_json,
                    reserved_json = EXCLUDED.reserved_json,
                    labels_json = EXCLUDED.labels_json,
                    taints_json = EXCLUDED.taints_json,
                    architecture = EXCLUDED.architecture,
                    os = EXCLUDED.os
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.setString(2, address)
                ps.setString(3, capacityJson)
                ps.setTimestamp(4, java.sql.Timestamp.from(at))
                ps.setTimestamp(5, java.sql.Timestamp.from(at))
                ps.setString(6, allocatableJson)
                ps.setString(7, reservedJson)
                ps.setString(8, labelsJson)
                ps.setString(9, taintsJson)
                ps.setString(10, arch)
                ps.setString(11, os)
                ps.executeUpdate()
            }
            find(conn, id) ?: error("node missing after register")
        }
    }

    override fun registerJoin(
        id: String,
        address: String,
        capacity: NodeCapacity,
        status: String,
        wireguardPublicKey: String?,
        networkCidr: String?,
        networkGateway: String?,
        joinedAt: Instant?,
        at: Instant,
        clearKeyRevocation: Boolean,
        reserved: NodeReserved,
        facts: NodeSchedulingFacts,
    ): FleetNode = runSql {
        dataSource.withConnection { conn ->
            val capacityJson = json.encodeToString(capacity)
            val reservedJson = json.encodeToString(reserved)
            val allocatableJson = json.encodeToString(
                CapacityAccounting.allocatable(capacity, reserved, overcommit),
            )
            val existing = find(conn, id)
            val effective = if (facts.lacksOperatorIdentity() && existing != null) {
                facts.withPreservedOperatorIdentity(existing)
            } else {
                facts
            }
            val arch = effective.architecture.ifBlank { "amd64" }
            val os = effective.os.ifBlank { "linux" }
            val merged = NodeLabelMerger.merge(
                nodeId = id,
                architecture = arch,
                os = os,
                provider = effective.provider,
                poolLabels = effective.poolLabels,
                agentLabels = effective.agentLabels,
            )
            val labelsJson = json.encodeToString(
                MapSerializer(String.serializer(), String.serializer()),
                merged.labels,
            )
            val taintsJson = json.encodeToString(
                ListSerializer(NodeTaint.serializer()),
                effective.taints,
            )
            conn.prepareStatement(
                """
                INSERT INTO nodes (
                    id, address, capacity_json, allocation_json, status,
                    last_heartbeat_at, registered_at,
                    wireguard_public_key, network_cidr, network_gateway, joined_at, key_revoked_at,
                    allocatable_json, reserved_json, labels_json, taints_json, architecture, os
                ) VALUES (
                    ?, ?, ?::jsonb, '{}'::jsonb, ?,
                    ?, ?,
                    ?, ?::cidr, ?::inet, ?, NULL,
                    ?::jsonb, ?::jsonb, ?::jsonb, ?::jsonb, ?, ?
                )
                ON CONFLICT (id) DO UPDATE SET
                    address = EXCLUDED.address,
                    capacity_json = EXCLUDED.capacity_json,
                    status = EXCLUDED.status,
                    last_heartbeat_at = EXCLUDED.last_heartbeat_at,
                    wireguard_public_key = EXCLUDED.wireguard_public_key,
                    network_cidr = EXCLUDED.network_cidr,
                    network_gateway = EXCLUDED.network_gateway,
                    joined_at = COALESCE(EXCLUDED.joined_at, nodes.joined_at),
                    key_revoked_at = CASE
                        WHEN ? THEN NULL
                        ELSE nodes.key_revoked_at
                    END,
                    allocatable_json = EXCLUDED.allocatable_json,
                    reserved_json = EXCLUDED.reserved_json,
                    labels_json = EXCLUDED.labels_json,
                    taints_json = EXCLUDED.taints_json,
                    architecture = EXCLUDED.architecture,
                    os = EXCLUDED.os
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.setString(2, address)
                ps.setString(3, capacityJson)
                ps.setString(4, status)
                ps.setTimestamp(5, java.sql.Timestamp.from(at))
                ps.setTimestamp(6, java.sql.Timestamp.from(at))
                ps.setString(7, wireguardPublicKey)
                ps.setString(8, networkCidr)
                ps.setString(9, networkGateway)
                if (joinedAt != null) {
                    ps.setTimestamp(10, java.sql.Timestamp.from(joinedAt))
                } else {
                    ps.setTimestamp(10, null)
                }
                ps.setString(11, allocatableJson)
                ps.setString(12, reservedJson)
                ps.setString(13, labelsJson)
                ps.setString(14, taintsJson)
                ps.setString(15, arch)
                ps.setString(16, os)
                ps.setBoolean(17, clearKeyRevocation)
                ps.executeUpdate()
            }
            find(conn, id) ?: error("node missing after join register")
        }
    }

    override fun heartbeat(
        id: String,
        allocation: NodeAllocation,
        at: Instant,
    ): FleetNode? = runSql {
        dataSource.withConnection { conn ->
            val existing = find(conn, id) ?: return@withConnection null
            if (existing.keyRevoked) return@withConnection existing
            val allocationJson = json.encodeToString(allocation)
            val allocatableJson = json.encodeToString(
                CapacityAccounting.allocatable(existing.capacity, existing.reserved, overcommit),
            )
            val nextStatus = when (existing.status) {
                "joining" -> "online"
                "pending-network" -> "pending-network"
                "draining" -> "draining"
                "offline" -> "online"
                else -> "online"
            }
            conn.prepareStatement(
                """
                UPDATE nodes
                SET allocation_json = ?::jsonb,
                    last_heartbeat_at = ?,
                    status = ?,
                    allocatable_json = ?::jsonb
                WHERE id = ?
                  AND key_revoked_at IS NULL
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, allocationJson)
                ps.setTimestamp(2, java.sql.Timestamp.from(at))
                ps.setString(3, nextStatus)
                ps.setString(4, allocatableJson)
                ps.setString(5, id)
                if (ps.executeUpdate() == 0) return@withConnection null
            }
            find(conn, id)
        }
    }

    override fun revokeKey(id: String, at: Instant): FleetNode? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE nodes
                SET wireguard_public_key = NULL,
                    key_revoked_at = ?,
                    status = 'offline',
                    network_cidr = NULL,
                    network_gateway = NULL
                WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setTimestamp(1, java.sql.Timestamp.from(at))
                ps.setString(2, id)
                if (ps.executeUpdate() == 0) return@withConnection null
            }
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
                       last_heartbeat_at, registered_at,
                       wireguard_public_key, network_cidr::text, network_gateway::text,
                       joined_at, key_revoked_at, allocatable_json, reserved_json,
                       labels_json, taints_json, architecture, os
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

    override fun markOffline(id: String): Boolean = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE nodes
                SET status = 'offline'
                WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.executeUpdate() > 0
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

    override fun tryReserve(id: String, requirements: ResourceRequirements): Boolean = runSql {
        dataSource.withConnection { conn ->
            conn.autoCommit = false
            try {
                val node = find(conn, id) ?: return@withConnection false
                if (!PlacementCapacity.fits(node, requirements)) {
                    conn.rollback()
                    return@withConnection false
                }
                val next = bumpAllocation(node.allocation, requirements, release = false)
                val updated = conn.prepareStatement(
                    """
                    UPDATE nodes
                    SET allocation_json = ?::jsonb
                    WHERE id = ?
                      AND status = 'online'
                      AND COALESCE((allocation_json->>'slots')::int, 0) = ?
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, json.encodeToString(next))
                    ps.setString(2, id)
                    ps.setInt(3, node.allocation.slots)
                    ps.executeUpdate()
                }
                if (updated == 1) {
                    conn.commit()
                    true
                } else {
                    conn.rollback()
                    false
                }
            } catch (e: Exception) {
                conn.rollback()
                throw e
            } finally {
                conn.autoCommit = true
            }
        }
    }

    override fun release(id: String, requirements: ResourceRequirements): Boolean = runSql {
        dataSource.withConnection { conn ->
            conn.autoCommit = false
            try {
                val node = find(conn, id) ?: return@withConnection false
                val next = bumpAllocation(node.allocation, requirements, release = true)
                conn.prepareStatement(
                    """
                    UPDATE nodes
                    SET allocation_json = ?::jsonb
                    WHERE id = ?
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, json.encodeToString(next))
                    ps.setString(2, id)
                    ps.executeUpdate()
                }
                conn.commit()
                true
            } catch (e: Exception) {
                conn.rollback()
                throw e
            } finally {
                conn.autoCommit = true
            }
        }
    }

    private fun find(conn: java.sql.Connection, id: String): FleetNode? {
        conn.prepareStatement(
            """
            SELECT id, address, capacity_json, allocation_json, status,
                   last_heartbeat_at, registered_at,
                   wireguard_public_key, network_cidr::text, network_gateway::text,
                   joined_at, key_revoked_at, allocatable_json, reserved_json,
                   labels_json, taints_json, architecture, os
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
        val capacity = json.decodeFromString(NodeCapacity.serializer(), capacityRaw)
        val reservedRaw = rs.getString("reserved_json")
        val reserved = if (reservedRaw.isNullOrBlank() || reservedRaw == "{}") {
            NodeReserved()
        } else {
            json.decodeFromString(NodeReserved.serializer(), reservedRaw)
        }
        val allocatableRaw = rs.getString("allocatable_json")
        val allocatable = if (allocatableRaw.isNullOrBlank() || allocatableRaw == "{}") {
            CapacityAccounting.allocatable(capacity, reserved, overcommit)
        } else {
            json.decodeFromString(NodeCapacity.serializer(), allocatableRaw)
        }
        val labelsRaw = runCatching { rs.getString("labels_json") }.getOrNull()
        val taintsRaw = runCatching { rs.getString("taints_json") }.getOrNull()
        val architecture = runCatching { rs.getString("architecture") }.getOrNull()
            ?.takeIf { it.isNotBlank() } ?: "amd64"
        val os = runCatching { rs.getString("os") }.getOrNull()
            ?.takeIf { it.isNotBlank() } ?: "linux"
        return FleetNode(
            id = rs.getString("id"),
            address = rs.getString("address"),
            capacity = capacity,
            allocation = if (allocationRaw.isNullOrBlank() || allocationRaw == "{}") {
                NodeAllocation()
            } else {
                json.decodeFromString(NodeAllocation.serializer(), allocationRaw)
            },
            status = rs.getString("status"),
            lastHeartbeatAt = rs.instant("last_heartbeat_at"),
            registeredAt = rs.instant("registered_at"),
            wireguardPublicKey = rs.getString("wireguard_public_key"),
            networkCidr = rs.getString("network_cidr"),
            networkGateway = rs.getString("network_gateway"),
            joinedAt = rs.getTimestamp("joined_at")?.toInstant(),
            keyRevokedAt = rs.getTimestamp("key_revoked_at")?.toInstant(),
            allocatable = allocatable,
            reserved = reserved,
            labels = decodeLabels(labelsRaw),
            taints = decodeTaints(taintsRaw),
            architecture = architecture,
            os = os,
        )
    }
}

/** In-memory store for unit tests. */
class InMemoryNodeStore(
    private val overcommit: OvercommitConfig = OvercommitConfig(),
) : NodeStore {
    private val rows = ConcurrentHashMap<String, FleetNode>()

    override fun register(
        id: String,
        address: String,
        capacity: NodeCapacity,
        at: Instant,
        reserved: NodeReserved,
        facts: NodeSchedulingFacts,
    ): FleetNode {
        val existing = rows[id]
        val effective = if (facts.lacksOperatorIdentity() && existing != null) {
            facts.withPreservedOperatorIdentity(existing)
        } else {
            facts
        }
        val arch = effective.architecture.ifBlank { "amd64" }
        val os = effective.os.ifBlank { "linux" }
        val merged = NodeLabelMerger.merge(
            nodeId = id,
            architecture = arch,
            os = os,
            provider = effective.provider,
            poolLabels = effective.poolLabels,
            agentLabels = effective.agentLabels,
        )
        val node = FleetNode(
            id = id,
            address = address,
            capacity = capacity,
            allocation = existing?.allocation ?: NodeAllocation(),
            status = "online",
            lastHeartbeatAt = at,
            registeredAt = existing?.registeredAt ?: at,
            wireguardPublicKey = existing?.wireguardPublicKey,
            networkCidr = existing?.networkCidr,
            networkGateway = existing?.networkGateway,
            joinedAt = existing?.joinedAt,
            keyRevokedAt = existing?.keyRevokedAt,
            allocatable = CapacityAccounting.allocatable(capacity, reserved, overcommit),
            reserved = reserved,
            labels = merged.labels,
            taints = effective.taints,
            architecture = arch,
            os = os,
        )
        rows[id] = node
        return node
    }

    override fun registerJoin(
        id: String,
        address: String,
        capacity: NodeCapacity,
        status: String,
        wireguardPublicKey: String?,
        networkCidr: String?,
        networkGateway: String?,
        joinedAt: Instant?,
        at: Instant,
        clearKeyRevocation: Boolean,
        reserved: NodeReserved,
        facts: NodeSchedulingFacts,
    ): FleetNode {
        val existing = rows[id]
        val effectiveFacts = if (facts.lacksOperatorIdentity() && existing != null) {
            facts.withPreservedOperatorIdentity(existing)
        } else {
            facts
        }
        val arch = effectiveFacts.architecture.ifBlank { "amd64" }
        val os = effectiveFacts.os.ifBlank { "linux" }
        val merged = NodeLabelMerger.merge(
            nodeId = id,
            architecture = arch,
            os = os,
            provider = effectiveFacts.provider,
            poolLabels = effectiveFacts.poolLabels,
            agentLabels = effectiveFacts.agentLabels,
        )
        val node = FleetNode(
            id = id,
            address = address,
            capacity = capacity,
            allocation = existing?.allocation ?: NodeAllocation(),
            status = status,
            lastHeartbeatAt = at,
            registeredAt = existing?.registeredAt ?: at,
            wireguardPublicKey = wireguardPublicKey,
            networkCidr = networkCidr,
            networkGateway = networkGateway,
            joinedAt = joinedAt ?: existing?.joinedAt,
            keyRevokedAt = if (clearKeyRevocation) null else existing?.keyRevokedAt,
            allocatable = CapacityAccounting.allocatable(capacity, reserved, overcommit),
            reserved = reserved,
            labels = merged.labels,
            taints = effectiveFacts.taints,
            architecture = arch,
            os = os,
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
        if (existing.keyRevoked) return existing
        val nextStatus = when (existing.status) {
            "joining" -> "online"
            "pending-network" -> "pending-network"
            "draining" -> "draining"
            "offline" -> "online"
            else -> "online"
        }
        val updated = existing.copy(
            allocation = allocation,
            status = nextStatus,
            lastHeartbeatAt = at,
            allocatable = CapacityAccounting.allocatable(
                existing.capacity,
                existing.reserved,
                overcommit,
            ),
        )
        rows[id] = updated
        return updated
    }

    override fun revokeKey(id: String, at: Instant): FleetNode? {
        val existing = rows[id] ?: return null
        val updated = existing.copy(
            wireguardPublicKey = null,
            keyRevokedAt = at,
            status = "offline",
            networkCidr = null,
            networkGateway = null,
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

    override fun markOffline(id: String): Boolean {
        val existing = rows[id] ?: return false
        rows[id] = existing.copy(status = "offline")
        return true
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

    override fun tryReserve(id: String, requirements: ResourceRequirements): Boolean {
        synchronized(rows) {
            val node = rows[id] ?: return false
            if (!PlacementCapacity.fits(node, requirements)) return false
            rows[id] = node.copy(
                allocation = bumpAllocation(node.allocation, requirements, release = false),
            )
            return true
        }
    }

    override fun release(id: String, requirements: ResourceRequirements): Boolean {
        synchronized(rows) {
            val node = rows[id] ?: return false
            rows[id] = node.copy(
                allocation = bumpAllocation(node.allocation, requirements, release = true),
            )
            return true
        }
    }
}

private fun decodeLabels(raw: String?): Map<String, String> {
    if (raw.isNullOrBlank() || raw == "{}") return emptyMap()
    return runCatching {
        json.decodeFromString(MapSerializer(String.serializer(), String.serializer()), raw)
    }.getOrDefault(emptyMap())
}

private fun decodeTaints(raw: String?): List<NodeTaint> {
    if (raw.isNullOrBlank() || raw == "[]") return emptyList()
    return runCatching {
        json.decodeFromString(ListSerializer(NodeTaint.serializer()), raw)
    }.getOrDefault(emptyList())
}

private fun bumpAllocation(
    current: NodeAllocation,
    requirements: ResourceRequirements,
    release: Boolean,
): NodeAllocation {
    val resolved = RequirementsResolver.resolve(requirements)
    val deltaSlots = if (release) -resolved.slots else resolved.slots
    val nextSlots = (current.slots + deltaSlots).coerceAtLeast(0)
    // Slots-authoritative path reserves slots only (epic-08 compatibility).
    // Real-unit reservations apply only when requests are authoritative.
    if (!resolved.requestsAuthoritative) {
        return current.copy(slots = nextSlots)
    }
    val needCpu = resolved.cpuMillis
    val nextCpu = when {
        needCpu == null -> current.cpuMillis
        release -> current.cpuMillis?.let { (it - needCpu).coerceAtLeast(0) } ?: 0
        else -> (current.cpuMillis ?: 0) + needCpu
    }
    val needMem = resolved.memoryMb
    val nextMem = when {
        needMem == null -> current.memMb
        release -> current.memMb?.let { (it - needMem).coerceAtLeast(0) } ?: 0
        else -> (current.memMb ?: 0) + needMem
    }
    val needDisk = resolved.diskMb
    val nextDisk = when {
        needDisk == null -> current.diskMb
        release -> current.diskMb?.let { (it - needDisk).coerceAtLeast(0) } ?: 0
        else -> (current.diskMb ?: 0) + needDisk
    }
    return current.copy(slots = nextSlots, cpuMillis = nextCpu, memMb = nextMem, diskMb = nextDisk)
}
