package forge.control.reconcile

import forge.control.repo.runSql
import forge.control.repo.withConnection
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import javax.sql.DataSource

data class LastHealthyDeployment(
    val serviceId: UUID,
    val deploymentId: UUID,
    val image: String,
    val replicas: Int,
) {
    init {
        require(image.isNotBlank()) { "last healthy image must not be blank" }
        require(replicas >= 0) { "last healthy replicas must be >= 0" }
    }
}

/** Persist/read last known-good deployment (image + replica count) per service. */
interface LastHealthyStore {
    fun get(serviceId: UUID): LastHealthyDeployment?
    fun put(record: LastHealthyDeployment)
}

class JdbcLastHealthyStore(
    private val dataSource: DataSource,
) : LastHealthyStore {
    override fun get(serviceId: UUID): LastHealthyDeployment? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT last_healthy_deployment_id, last_healthy_image, last_healthy_replicas
                FROM services WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, serviceId)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) return@use null
                    val deploymentId = rs.getObject("last_healthy_deployment_id") as? UUID
                        ?: return@use null
                    val image = rs.getString("last_healthy_image") ?: return@use null
                    val replicas = rs.getObject("last_healthy_replicas") as? Int ?: return@use null
                    LastHealthyDeployment(
                        serviceId = serviceId,
                        deploymentId = deploymentId,
                        image = image,
                        replicas = replicas,
                    )
                }
            }
        }
    }

    override fun put(record: LastHealthyDeployment) {
        runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    UPDATE services
                    SET last_healthy_deployment_id = ?,
                        last_healthy_image = ?,
                        last_healthy_replicas = ?,
                        updated_at = NOW()
                    WHERE id = ?
                    """.trimIndent(),
                ).use { ps ->
                    ps.setObject(1, record.deploymentId)
                    ps.setString(2, record.image)
                    ps.setInt(3, record.replicas)
                    ps.setObject(4, record.serviceId)
                    ps.executeUpdate()
                }
            }
        }
    }
}

class InMemoryLastHealthyStore : LastHealthyStore {
    private val rows = ConcurrentHashMap<UUID, LastHealthyDeployment>()

    override fun get(serviceId: UUID): LastHealthyDeployment? = rows[serviceId]

    override fun put(record: LastHealthyDeployment) {
        rows[record.serviceId] = record
    }

    fun clear() = rows.clear()
}
