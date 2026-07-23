package forge.control.scheduler

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.withConnection
import forge.control.scheduler.model.GpuRequest
import forge.control.scheduler.model.ResourceBundle
import forge.control.scheduler.model.ResourceQuantity
import forge.control.scheduler.model.ResourceRequirements
import forge.control.telemetry.Telemetry
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import javax.sql.DataSource

@Serializable
data class ReservationResources(
    val cpu: String? = null,
    val memory: String? = null,
    @SerialName("cpu_millis") val cpuMillis: Int? = null,
    @SerialName("memory_mb") val memoryMb: Int? = null,
    val slots: Int? = null,
    val gpu: GpuRequest? = null,
) {
    fun toRequirements(): ResourceRequirements {
        val cpuM = cpuMillis ?: cpu?.let { ResourceQuantity.parseCpuMillis(it) }
        val memM = memoryMb ?: memory?.let { ResourceQuantity.parseMemoryMb(it) }
        val hasReal = cpuM != null || memM != null || gpu != null
        return if (hasReal) {
            ResourceRequirements(
                slots = (slots ?: 1).coerceAtLeast(1),
                requests = ResourceBundle(cpuMillis = cpuM, memoryMb = memM),
                gpu = gpu,
                slotsExplicit = slots != null,
            )
        } else {
            ResourceRequirements(
                slots = (slots ?: 1).coerceAtLeast(1),
                gpu = gpu,
                slotsExplicit = true,
            )
        }
    }
}

data class CapacityHold(
    val name: String,
    val resources: ReservationResources,
    val expiresAt: Instant,
    val ownerRef: String? = null,
    val nodeId: String? = null,
    val status: String = STATUS_ACTIVE,
    val createdAt: Instant = Instant.now(),
    val consumedByPlacementId: String? = null,
) {
    companion object {
        const val STATUS_ACTIVE: String = "active"
        const val STATUS_CONSUMED: String = "consumed"
        const val STATUS_EXPIRED: String = "expired"
    }
}

interface ReservationStore {
    fun create(hold: CapacityHold): CapacityHold

    fun find(name: String): CapacityHold?

    fun listActive(): List<CapacityHold>

    fun consume(name: String, placementId: String): CapacityHold?

    fun expire(name: String): CapacityHold?

    fun countActive(): Int
}

class InMemoryReservationStore : ReservationStore {
    private val rows = ConcurrentHashMap<String, CapacityHold>()

    override fun create(hold: CapacityHold): CapacityHold {
        if (rows.putIfAbsent(hold.name, hold) != null) {
            throw ApiException.Conflict(
                "reservation already exists",
                mapOf("name" to hold.name),
            )
        }
        return hold
    }

    override fun find(name: String): CapacityHold? = rows[name]

    override fun listActive(): List<CapacityHold> =
        rows.values.filter { it.status == CapacityHold.STATUS_ACTIVE }.sortedBy { it.name }

    override fun consume(name: String, placementId: String): CapacityHold? {
        val current = rows[name] ?: return null
        if (current.status != CapacityHold.STATUS_ACTIVE) return null
        val next = current.copy(
            status = CapacityHold.STATUS_CONSUMED,
            consumedByPlacementId = placementId,
        )
        rows[name] = next
        return next
    }

    override fun expire(name: String): CapacityHold? {
        val current = rows[name] ?: return null
        if (current.status != CapacityHold.STATUS_ACTIVE) return null
        val next = current.copy(status = CapacityHold.STATUS_EXPIRED)
        rows[name] = next
        return next
    }

    override fun countActive(): Int =
        rows.values.count { it.status == CapacityHold.STATUS_ACTIVE }
}

private val reservationJson = Json {
    encodeDefaults = true
    ignoreUnknownKeys = true
    explicitNulls = false
}

class JdbcReservationStore(
    private val dataSource: DataSource,
) : ReservationStore {
    override fun create(hold: CapacityHold): CapacityHold = runSql {
        dataSource.withConnection { conn ->
            try {
                conn.prepareStatement(
                    """
                    INSERT INTO control.reservations(
                        name, resources_json, expires_at, owner_ref, node_id,
                        status, created_at, consumed_by_placement_id
                    ) VALUES (?, ?::jsonb, ?, ?, ?, ?, ?, ?)
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, hold.name)
                    ps.setString(2, reservationJson.encodeToString(hold.resources))
                    ps.setTimestamp(3, Timestamp.from(hold.expiresAt))
                    ps.setString(4, hold.ownerRef)
                    ps.setString(5, hold.nodeId)
                    ps.setString(6, hold.status)
                    ps.setTimestamp(7, Timestamp.from(hold.createdAt))
                    ps.setString(8, hold.consumedByPlacementId)
                    ps.executeUpdate()
                }
            } catch (e: java.sql.SQLException) {
                if (e.sqlState == "23505") {
                    throw ApiException.Conflict(
                        "reservation already exists",
                        mapOf("name" to hold.name),
                    )
                }
                throw e
            }
            find(hold.name) ?: error("reservation missing after create")
        }
    }

    override fun find(name: String): CapacityHold? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT name, resources_json, expires_at, owner_ref, node_id,
                       status, created_at, consumed_by_placement_id
                FROM control.reservations WHERE name = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, name)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) return@withConnection null
                    mapRow(rs)
                }
            }
        }
    }

    override fun listActive(): List<CapacityHold> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT name, resources_json, expires_at, owner_ref, node_id,
                       status, created_at, consumed_by_placement_id
                FROM control.reservations
                WHERE status = 'active'
                ORDER BY name
                """.trimIndent(),
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    val out = mutableListOf<CapacityHold>()
                    while (rs.next()) out += mapRow(rs)
                    out
                }
            }
        }
    }

    override fun consume(name: String, placementId: String): CapacityHold? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE control.reservations
                SET status = 'consumed', consumed_by_placement_id = ?
                WHERE name = ? AND status = 'active'
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, placementId)
                ps.setString(2, name)
                if (ps.executeUpdate() == 0) return@withConnection null
            }
            find(name)
        }
    }

    override fun expire(name: String): CapacityHold? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE control.reservations
                SET status = 'expired'
                WHERE name = ? AND status = 'active'
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, name)
                if (ps.executeUpdate() == 0) return@withConnection null
            }
            find(name)
        }
    }

    override fun countActive(): Int = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                "SELECT COUNT(*) FROM control.reservations WHERE status = 'active'",
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    rs.next()
                    rs.getInt(1)
                }
            }
        }
    }

    private fun mapRow(rs: java.sql.ResultSet): CapacityHold {
        val resourcesRaw = rs.getString("resources_json")
        val resources = reservationJson.decodeFromString(
            ReservationResources.serializer(),
            resourcesRaw,
        )
        return CapacityHold(
            name = rs.getString("name"),
            resources = resources,
            expiresAt = rs.instant("expires_at"),
            ownerRef = rs.getString("owner_ref"),
            nodeId = rs.getString("node_id"),
            status = rs.getString("status"),
            createdAt = rs.instant("created_at"),
            consumedByPlacementId = rs.getString("consumed_by_placement_id"),
        )
    }
}

/**
 * Places a reservation hold onto a node (holds capacity until consume/expire).
 */
class ReservationService(
    private val store: ReservationStore,
    private val nodes: NodeStore,
    private val capacityReservation: CapacityReservation,
    private val log: JsonLog? = null,
    private val telemetry: Telemetry = Telemetry.current(),
    private val clock: () -> Instant = { Instant.now() },
) {
    fun create(
        name: String,
        resources: ReservationResources,
        expiresAfter: Duration,
        ownerRef: String? = null,
        preferredNodeId: String? = null,
    ): CapacityHold {
        require(name.isNotBlank()) { "reservation name must not be blank" }
        require(!expiresAfter.isNegative && !expiresAfter.isZero) {
            "expiresAfter must be > 0"
        }
        val reqs = resources.toRequirements()
        val nodeId = preferredNodeId?.takeIf { it.isNotBlank() }
            ?: pickNode(reqs)
            ?: throw ApiException.Conflict(
                "no node available for reservation",
                mapOf("reason" to "InsufficientCapacity"),
            )
        if (!capacityReservation.tryReserve(nodeId, reqs)) {
            throw ApiException.Conflict(
                "failed to reserve capacity on node",
                mapOf("node_id" to nodeId),
            )
        }
        val hold = try {
            store.create(
                CapacityHold(
                    name = name,
                    resources = resources,
                    expiresAt = clock().plus(expiresAfter),
                    ownerRef = ownerRef,
                    nodeId = nodeId,
                    createdAt = clock(),
                ),
            )
        } catch (e: Exception) {
            capacityReservation.release(nodeId, reqs)
            throw e
        }
        telemetry.setReservationsActive(store.countActive())
        log?.info(
            "reservation created",
            "event" to "reservation_created",
            "name" to name,
            "node" to nodeId,
            "expires_at" to hold.expiresAt.toString(),
        )
        return hold
    }

    /**
     * Release held capacity so a placement can [CapacityReservation.tryReserve] it,
     * without marking the reservation consumed yet.
     */
    fun prepareForConsume(name: String): CapacityHold? {
        val hold = store.find(name) ?: return null
        if (hold.status != CapacityHold.STATUS_ACTIVE) return null
        val nodeId = hold.nodeId
        if (!nodeId.isNullOrBlank()) {
            capacityReservation.release(nodeId, hold.resources.toRequirements())
        }
        return hold
    }

    /** Re-hold capacity after a failed consume attempt. */
    fun restoreHold(hold: CapacityHold): Boolean {
        if (hold.status != CapacityHold.STATUS_ACTIVE) return false
        val nodeId = hold.nodeId ?: return false
        return capacityReservation.tryReserve(nodeId, hold.resources.toRequirements())
    }

    fun consume(name: String, placementId: String): CapacityHold? {
        val hold = store.find(name) ?: return null
        if (hold.status != CapacityHold.STATUS_ACTIVE) return null
        val consumed = store.consume(name, placementId) ?: return null
        telemetry.setReservationsActive(store.countActive())
        return consumed
    }

    fun find(name: String): CapacityHold? = store.find(name)

    fun listActive(): List<CapacityHold> = store.listActive()

    fun releaseExpired(now: Instant = clock()): Int {
        var released = 0
        for (hold in store.listActive()) {
            if (!hold.expiresAt.isAfter(now)) {
                if (expireOne(hold)) released++
            }
        }
        if (released > 0) {
            telemetry.setReservationsActive(store.countActive())
        }
        return released
    }

    private fun expireOne(hold: CapacityHold): Boolean {
        val expired = store.expire(hold.name) ?: return false
        val nodeId = expired.nodeId
        if (!nodeId.isNullOrBlank()) {
            capacityReservation.release(nodeId, expired.resources.toRequirements())
        }
        log?.info(
            "reservation expired",
            "event" to "reservation_expired",
            "name" to expired.name,
            "node" to (nodeId ?: ""),
        )
        return true
    }

    private fun pickNode(reqs: ResourceRequirements): String? =
        PlacementCapacity.candidates(nodes, reqs).firstOrNull()?.id
}

/** Background TTL loop that releases expired reservation holds. */
class ReservationReaper(
    private val service: ReservationService,
    private val interval: Duration = Duration.ofSeconds(15),
    private val log: JsonLog? = null,
) : AutoCloseable {
    private val running = AtomicBoolean(false)
    private val executor = Executors.newSingleThreadScheduledExecutor { r ->
        Thread(r, "reservation-reaper").apply { isDaemon = true }
    }

    fun start() {
        if (!running.compareAndSet(false, true)) return
        executor.scheduleWithFixedDelay(
            {
                try {
                    val n = service.releaseExpired()
                    if (n > 0) {
                        log?.info(
                            "reservation reaper released",
                            "event" to "reservation_reaper",
                            "released" to n,
                        )
                    }
                } catch (e: Exception) {
                    log?.error(
                        "reservation reaper failed",
                        "error" to (e.message ?: e.javaClass.simpleName),
                    )
                }
            },
            interval.toMillis(),
            interval.toMillis(),
            TimeUnit.MILLISECONDS,
        )
    }

    override fun close() {
        if (!running.compareAndSet(true, false)) return
        executor.shutdownNow()
    }
}
