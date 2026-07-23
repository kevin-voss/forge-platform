package forge.control.scheduler

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.uuid
import forge.control.repo.withConnection
import forge.control.telemetry.Telemetry
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import javax.sql.DataSource

data class DisruptionBudget(
    val deploymentId: UUID,
    val minAvailable: Int? = null,
    val maxUnavailable: Int? = null,
    val createdAt: Instant = Instant.EPOCH,
) {
    init {
        require(minAvailable != null || maxUnavailable != null) {
            "disruption budget requires min_available or max_unavailable"
        }
        require(minAvailable == null || maxUnavailable == null) {
            "disruption budget cannot set both min_available and max_unavailable"
        }
        minAvailable?.let { require(it >= 0) { "min_available must be >= 0" } }
        maxUnavailable?.let { require(it >= 0) { "max_unavailable must be >= 0" } }
    }
}

data class DisruptionBudgetCheck(
    val allowed: Boolean,
    val deploymentId: UUID,
    val running: Int,
    val unavailable: Int,
    val minAvailable: Int? = null,
    val maxUnavailable: Int? = null,
    val reason: String? = null,
)

interface DisruptionBudgetStore {
    fun upsert(budget: DisruptionBudget): DisruptionBudget

    fun find(deploymentId: UUID): DisruptionBudget?

    fun delete(deploymentId: UUID): Boolean
}

/**
 * Guards voluntary removals (preemption victim, rolling drain/stop, drain).
 * Never consulted for involuntary node-loss (08.05).
 */
class DisruptionBudgetGuard(
    private val store: DisruptionBudgetStore,
    private val placements: PlacementStore,
    private val log: JsonLog? = null,
    private val telemetry: Telemetry = Telemetry.current(),
) {
    /**
     * Whether removing one currently-running replica of [deploymentId] is allowed.
     *
     * [runningOverride] / [unavailableOverride] let callers supply observed replica
     * counts (reconciler) instead of placement-store counts (scheduler).
     */
    fun allowsVoluntaryRemoval(
        deploymentId: UUID,
        runningOverride: Int? = null,
        unavailableOverride: Int? = null,
    ): DisruptionBudgetCheck {
        val budget = store.find(deploymentId)
        val placed = placements.listByDeployment(deploymentId, PendingQueue.STATUS_PLACED)
        val pending = placements.listByDeployment(deploymentId, PendingQueue.STATUS_PENDING)
        val running = runningOverride ?: placed.size
        val unavailable = unavailableOverride ?: pending.size
        if (budget == null) {
            return DisruptionBudgetCheck(
                allowed = true,
                deploymentId = deploymentId,
                running = running,
                unavailable = unavailable,
            )
        }
        val check = when {
            budget.minAvailable != null -> {
                val after = running - 1
                val allowed = after >= budget.minAvailable
                DisruptionBudgetCheck(
                    allowed = allowed,
                    deploymentId = deploymentId,
                    running = running,
                    unavailable = unavailable,
                    minAvailable = budget.minAvailable,
                    reason = if (allowed) {
                        null
                    } else {
                        "min_available=${budget.minAvailable} would be violated " +
                            "(running=$running -> $after)"
                    },
                )
            }
            budget.maxUnavailable != null -> {
                val after = unavailable + 1
                val allowed = after <= budget.maxUnavailable
                DisruptionBudgetCheck(
                    allowed = allowed,
                    deploymentId = deploymentId,
                    running = running,
                    unavailable = unavailable,
                    maxUnavailable = budget.maxUnavailable,
                    reason = if (allowed) {
                        null
                    } else {
                        "max_unavailable=${budget.maxUnavailable} would be violated " +
                            "(unavailable=$unavailable -> $after)"
                    },
                )
            }
            else -> DisruptionBudgetCheck(
                allowed = true,
                deploymentId = deploymentId,
                running = running,
                unavailable = unavailable,
            )
        }
        log?.info(
            "disruption budget check",
            "event" to "disruption_budget_check",
            "deployment" to deploymentId.toString(),
            "allowed" to check.allowed,
            "min_available" to (check.minAvailable ?: ""),
            "max_unavailable" to (check.maxUnavailable ?: ""),
            "running" to running,
            "unavailable" to unavailable,
        )
        if (!check.allowed) {
            telemetry.recordDisruptionBudgetBlocked(deploymentId.toString())
        }
        return check
    }
}

class JdbcDisruptionBudgetStore(
    private val dataSource: DataSource,
) : DisruptionBudgetStore {
    override fun upsert(budget: DisruptionBudget): DisruptionBudget = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO disruption_budgets(
                    deployment_id, min_available, max_unavailable, created_at
                ) VALUES (?, ?, ?, ?)
                ON CONFLICT (deployment_id) DO UPDATE SET
                    min_available = EXCLUDED.min_available,
                    max_unavailable = EXCLUDED.max_unavailable
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, budget.deploymentId)
                if (budget.minAvailable != null) ps.setInt(2, budget.minAvailable) else ps.setObject(2, null)
                if (budget.maxUnavailable != null) {
                    ps.setInt(3, budget.maxUnavailable)
                } else {
                    ps.setObject(3, null)
                }
                ps.setTimestamp(4, Timestamp.from(budget.createdAt))
                try {
                    ps.executeUpdate()
                } catch (e: java.sql.SQLException) {
                    if (e.sqlState == "23503") {
                        throw ApiException.NotFound(
                            "deployment not found",
                            mapOf("deployment_id" to budget.deploymentId.toString()),
                        )
                    }
                    throw e
                }
            }
            find(budget.deploymentId) ?: error("disruption budget missing after upsert")
        }
    }

    override fun find(deploymentId: UUID): DisruptionBudget? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT deployment_id, min_available, max_unavailable, created_at
                FROM disruption_budgets
                WHERE deployment_id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, deploymentId)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) return@withConnection null
                    DisruptionBudget(
                        deploymentId = rs.uuid("deployment_id"),
                        minAvailable = rs.getInt("min_available").takeIf { !rs.wasNull() },
                        maxUnavailable = rs.getInt("max_unavailable").takeIf { !rs.wasNull() },
                        createdAt = rs.instant("created_at"),
                    )
                }
            }
        }
    }

    override fun delete(deploymentId: UUID): Boolean = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                "DELETE FROM disruption_budgets WHERE deployment_id = ?",
            ).use { ps ->
                ps.setObject(1, deploymentId)
                ps.executeUpdate() > 0
            }
        }
    }
}

class InMemoryDisruptionBudgetStore : DisruptionBudgetStore {
    private val rows = ConcurrentHashMap<UUID, DisruptionBudget>()

    override fun upsert(budget: DisruptionBudget): DisruptionBudget {
        rows[budget.deploymentId] = budget
        return budget
    }

    override fun find(deploymentId: UUID): DisruptionBudget? = rows[deploymentId]

    override fun delete(deploymentId: UUID): Boolean = rows.remove(deploymentId) != null
}
