package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.repo.runSql
import forge.control.repo.withConnection
import forge.control.scheduler.model.MigrationPolicy
import forge.control.scheduler.model.StatefulRole
import forge.control.scheduler.model.StatefulSpec
import forge.control.telemetry.Telemetry
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import javax.sql.DataSource

data class MigrationApproval(
    val deploymentId: UUID,
    val replicaIndex: Int,
    val approvedAt: Instant = Instant.now(),
    val approvedBy: String? = null,
)

interface MigrationApprovalStore {
    fun approve(approval: MigrationApproval): MigrationApproval

    fun hasApproval(deploymentId: UUID, replicaIndex: Int): Boolean

    fun consume(deploymentId: UUID, replicaIndex: Int): Boolean
}

class InMemoryMigrationApprovalStore : MigrationApprovalStore {
    private val rows = ConcurrentHashMap<String, MigrationApproval>()

    private fun key(deploymentId: UUID, replicaIndex: Int) = "$deploymentId:$replicaIndex"

    override fun approve(approval: MigrationApproval): MigrationApproval {
        rows[key(approval.deploymentId, approval.replicaIndex)] = approval
        return approval
    }

    override fun hasApproval(deploymentId: UUID, replicaIndex: Int): Boolean =
        rows.containsKey(key(deploymentId, replicaIndex))

    override fun consume(deploymentId: UUID, replicaIndex: Int): Boolean =
        rows.remove(key(deploymentId, replicaIndex)) != null
}

class JdbcMigrationApprovalStore(
    private val dataSource: DataSource,
) : MigrationApprovalStore {
    override fun approve(approval: MigrationApproval): MigrationApproval = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO control.migration_approvals(
                    deployment_id, replica_index, approved_at, approved_by
                ) VALUES (?, ?, ?, ?)
                ON CONFLICT (deployment_id, replica_index) DO UPDATE SET
                    approved_at = EXCLUDED.approved_at,
                    approved_by = EXCLUDED.approved_by
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, approval.deploymentId)
                ps.setInt(2, approval.replicaIndex)
                ps.setTimestamp(3, Timestamp.from(approval.approvedAt))
                ps.setString(4, approval.approvedBy)
                ps.executeUpdate()
            }
            approval
        }
    }

    override fun hasApproval(deploymentId: UUID, replicaIndex: Int): Boolean = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT 1 FROM control.migration_approvals
                WHERE deployment_id = ? AND replica_index = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, deploymentId)
                ps.setInt(2, replicaIndex)
                ps.executeQuery().use { it.next() }
            }
        }
    }

    override fun consume(deploymentId: UUID, replicaIndex: Int): Boolean = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                DELETE FROM control.migration_approvals
                WHERE deployment_id = ? AND replica_index = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, deploymentId)
                ps.setInt(2, replicaIndex)
                ps.executeUpdate() > 0
            }
        }
    }
}

data class StatefulGuardCheck(
    val allowed: Boolean,
    val reason: String? = null,
)

/**
 * Blocks automatic preemption and voluntary drain of protected stateful primaries
 * unless an explicit migration approval exists.
 */
class StatefulPrimaryGuard(
    private val approvals: MigrationApprovalStore = InMemoryMigrationApprovalStore(),
    private val log: JsonLog? = null,
    private val telemetry: Telemetry = Telemetry.current(),
) {
    fun allowsVoluntaryRemoval(placement: Placement): StatefulGuardCheck {
        val stateful = placement.stateful
        if (stateful == null || !stateful.isProtectedPrimary()) {
            return StatefulGuardCheck(allowed = true)
        }
        if (approvals.hasApproval(placement.deploymentId, placement.replicaIndex)) {
            return StatefulGuardCheck(allowed = true)
        }
        log?.info(
            "stateful primary protected",
            "event" to "stateful_primary_protected",
            "deployment" to placement.deploymentId.toString(),
            "replica_index" to placement.replicaIndex,
            "node" to (placement.nodeId ?: ""),
            "reason" to "manual-approval required",
        )
        telemetry.recordStatefulPrimaryProtected()
        return StatefulGuardCheck(
            allowed = false,
            reason = "StatefulPrimaryProtected: migration approval required",
        )
    }

    fun isProtectedPrimary(stateful: StatefulSpec?): Boolean =
        stateful?.isProtectedPrimary() == true

    fun isProtectedPrimary(placement: Placement): Boolean =
        isProtectedPrimary(placement.stateful)

    fun approveMigration(
        deploymentId: UUID,
        replicaIndex: Int,
        approvedBy: String? = null,
    ): MigrationApproval =
        approvals.approve(
            MigrationApproval(
                deploymentId = deploymentId,
                replicaIndex = replicaIndex,
                approvedBy = approvedBy,
            ),
        )

    fun consumeApproval(deploymentId: UUID, replicaIndex: Int): Boolean =
        approvals.consume(deploymentId, replicaIndex)

    companion object {
        fun looksLikePrimary(stateful: StatefulSpec?): Boolean =
            stateful?.resolvedRole() == StatefulRole.Primary &&
                stateful.resolvedMigrationPolicy() == MigrationPolicy.ManualApproval
    }
}
