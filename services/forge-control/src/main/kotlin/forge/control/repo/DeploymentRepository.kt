package forge.control.repo

import forge.control.domain.Deployment
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource

interface DeploymentRepository {
    fun create(
        serviceId: UUID,
        environmentId: UUID,
        image: String,
        desiredReplicas: Int = 1,
        status: String = "pending",
        rolloutBatchSize: Int = 1,
        rolloutTimeoutSeconds: Int = 120,
        name: String,
    ): Deployment

    fun findById(id: UUID): Deployment?
    fun findByEnvironmentAndName(environmentId: UUID, name: String): Deployment?
    fun listByService(serviceId: UUID): List<Deployment>
    fun listAll(): List<Deployment>
    fun update(
        id: UUID,
        image: String? = null,
        desiredReplicas: Int? = null,
        status: String? = null,
    ): Deployment

    fun delete(id: UUID)
}

class JdbcDeploymentRepository(
    private val dataSource: DataSource,
) : DeploymentRepository {
    override fun create(
        serviceId: UUID,
        environmentId: UUID,
        image: String,
        desiredReplicas: Int,
        status: String,
        rolloutBatchSize: Int,
        rolloutTimeoutSeconds: Int,
        name: String,
    ): Deployment = runSql {
        val id = UUID.randomUUID()
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO deployments (
                    id, service_id, environment_id, image, desired_replicas, status,
                    rollout_batch_size, rollout_timeout_s, name, created_at, updated_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.setObject(2, serviceId)
                ps.setObject(3, environmentId)
                ps.setString(4, image)
                ps.setInt(5, desiredReplicas)
                ps.setString(6, status)
                ps.setInt(7, rolloutBatchSize)
                ps.setInt(8, rolloutTimeoutSeconds)
                ps.setString(9, name)
                ps.setTimestamp(10, java.sql.Timestamp.from(now))
                ps.setTimestamp(11, java.sql.Timestamp.from(now))
                ps.executeUpdate()
            }
        }
        Deployment(
            id, serviceId, environmentId, image, desiredReplicas, status, now, now,
            rolloutBatchSize, rolloutTimeoutSeconds, name,
        )
    }

    override fun findById(id: UUID): Deployment? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, service_id, environment_id, image, desired_replicas, status,
                       rollout_batch_size, rollout_timeout_s, name, created_at, updated_at
                FROM deployments WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapRow(rs) else null
                }
            }
        }
    }

    override fun findByEnvironmentAndName(environmentId: UUID, name: String): Deployment? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, service_id, environment_id, image, desired_replicas, status,
                       rollout_batch_size, rollout_timeout_s, name, created_at, updated_at
                FROM deployments WHERE environment_id = ? AND name = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, environmentId)
                ps.setString(2, name)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapRow(rs) else null
                }
            }
        }
    }

    override fun listByService(serviceId: UUID): List<Deployment> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, service_id, environment_id, image, desired_replicas, status,
                       rollout_batch_size, rollout_timeout_s, name, created_at, updated_at
                FROM deployments WHERE service_id = ? ORDER BY created_at
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, serviceId)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(mapRow(rs))
                    }
                }
            }
        }
    }

    override fun listAll(): List<Deployment> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, service_id, environment_id, image, desired_replicas, status,
                       rollout_batch_size, rollout_timeout_s, name, created_at, updated_at
                FROM deployments ORDER BY created_at
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

    override fun update(
        id: UUID,
        image: String?,
        desiredReplicas: Int?,
        status: String?,
    ): Deployment = runSql {
        val existing = findById(id) ?: throw RepositoryException.NotFound("deployment", id)
        val newImage = image ?: existing.image
        val newReplicas = desiredReplicas ?: existing.desiredReplicas
        val newStatus = status ?: existing.status
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE deployments
                SET image = ?, desired_replicas = ?, status = ?, updated_at = ?
                WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, newImage)
                ps.setInt(2, newReplicas)
                ps.setString(3, newStatus)
                ps.setTimestamp(4, java.sql.Timestamp.from(now))
                ps.setObject(5, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("deployment", id)
                }
            }
        }
        existing.copy(
            image = newImage,
            desiredReplicas = newReplicas,
            status = newStatus,
            updatedAt = now,
        )
    }

    override fun delete(id: UUID) = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement("DELETE FROM deployments WHERE id = ?").use { ps ->
                ps.setObject(1, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("deployment", id)
                }
            }
        }
    }

    private fun mapRow(rs: java.sql.ResultSet): Deployment =
        Deployment(
            id = rs.uuid("id"),
            serviceId = rs.uuid("service_id"),
            environmentId = rs.uuid("environment_id"),
            image = rs.getString("image"),
            desiredReplicas = rs.getInt("desired_replicas"),
            status = rs.getString("status"),
            createdAt = rs.instant("created_at"),
            updatedAt = rs.instant("updated_at"),
            rolloutBatchSize = rs.getInt("rollout_batch_size"),
            rolloutTimeoutSeconds = rs.getInt("rollout_timeout_s"),
            name = rs.getString("name") ?: "",
        )
}
