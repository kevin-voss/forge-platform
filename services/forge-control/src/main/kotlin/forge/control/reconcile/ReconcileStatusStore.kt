package forge.control.reconcile

import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.uuid
import forge.control.repo.withConnection
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource
import kotlinx.serialization.json.Json

data class ReconcileSnapshot(
    val deploymentId: UUID,
    val lastRunAt: Instant,
    val desired: DesiredState,
    val actual: ActualState,
    val plan: ReconcilePlan,
    val controllerHealthy: Boolean,
)

interface ReconcileStatusStore {
    fun upsert(snapshot: ReconcileSnapshot)
    fun findByDeploymentId(deploymentId: UUID): ReconcileSnapshot?
}

class JdbcReconcileStatusStore(
    private val dataSource: DataSource,
    private val json: Json = Json {
        encodeDefaults = true
        ignoreUnknownKeys = true
    },
) : ReconcileStatusStore {
    override fun upsert(snapshot: ReconcileSnapshot) {
        runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    INSERT INTO reconcile_status (
                        deployment_id, last_run_at, desired_json, actual_json, plan_json, controller_healthy
                    ) VALUES (?, ?, ?::jsonb, ?::jsonb, ?::jsonb, ?)
                    ON CONFLICT (deployment_id) DO UPDATE SET
                        last_run_at = EXCLUDED.last_run_at,
                        desired_json = EXCLUDED.desired_json,
                        actual_json = EXCLUDED.actual_json,
                        plan_json = EXCLUDED.plan_json,
                        controller_healthy = EXCLUDED.controller_healthy
                    """.trimIndent(),
                ).use { ps ->
                    ps.setObject(1, snapshot.deploymentId)
                    ps.setTimestamp(2, java.sql.Timestamp.from(snapshot.lastRunAt))
                    ps.setString(3, json.encodeToString(DesiredState.serializer(), snapshot.desired))
                    ps.setString(4, json.encodeToString(ActualState.serializer(), snapshot.actual))
                    ps.setString(5, json.encodeToString(ReconcilePlan.serializer(), snapshot.plan))
                    ps.setBoolean(6, snapshot.controllerHealthy)
                    ps.executeUpdate()
                }
            }
        }
    }

    override fun findByDeploymentId(deploymentId: UUID): ReconcileSnapshot? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT deployment_id, last_run_at, desired_json, actual_json, plan_json, controller_healthy
                FROM reconcile_status WHERE deployment_id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, deploymentId)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) return@use null
                    ReconcileSnapshot(
                        deploymentId = rs.uuid("deployment_id"),
                        lastRunAt = rs.instant("last_run_at"),
                        desired = json.decodeFromString(
                            DesiredState.serializer(),
                            rs.getString("desired_json"),
                        ),
                        actual = json.decodeFromString(
                            ActualState.serializer(),
                            rs.getString("actual_json"),
                        ),
                        plan = json.decodeFromString(
                            ReconcilePlan.serializer(),
                            rs.getString("plan_json"),
                        ),
                        controllerHealthy = rs.getBoolean("controller_healthy"),
                    )
                }
            }
        }
    }
}

/** In-memory store for unit tests. */
class InMemoryReconcileStatusStore : ReconcileStatusStore {
    private val rows = linkedMapOf<UUID, ReconcileSnapshot>()

    override fun upsert(snapshot: ReconcileSnapshot) {
        rows[snapshot.deploymentId] = snapshot
    }

    override fun findByDeploymentId(deploymentId: UUID): ReconcileSnapshot? = rows[deploymentId]

    fun all(): List<ReconcileSnapshot> = rows.values.toList()
}
