package forge.control.manageddb

import forge.control.repo.RepositoryException
import forge.control.repo.instant
import forge.control.repo.runSql
import forge.control.repo.uuid
import forge.control.repo.withConnection
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource

interface ManagedDbRepository {
    fun createInstance(
        projectId: UUID,
        name: String,
        status: DbInstanceStatus = DbInstanceStatus.Provisioning,
        engine: String = "postgres",
        deletionProtection: Boolean = true,
    ): DbInstance

    fun findInstanceById(id: UUID): DbInstance?
    fun listInstances(projectId: UUID): List<DbInstance>
    fun updateInstanceStatus(
        id: UUID,
        status: DbInstanceStatus,
        statusReason: String? = null,
        endpointRef: String? = null,
    ): DbInstance

    fun listDatabases(instanceId: UUID): List<DbDatabase>
    fun createDatabase(instanceId: UUID, name: String): DbDatabase
}

class JdbcManagedDbRepository(
    private val dataSource: DataSource,
) : ManagedDbRepository {
    override fun createInstance(
        projectId: UUID,
        name: String,
        status: DbInstanceStatus,
        engine: String,
        deletionProtection: Boolean,
    ): DbInstance = runSql {
        val id = UUID.randomUUID()
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO db_instance (
                    id, project_id, name, status, engine, deletion_protection, created_at, updated_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.setObject(2, projectId)
                ps.setString(3, name)
                ps.setString(4, status.wire)
                ps.setString(5, engine)
                ps.setBoolean(6, deletionProtection)
                ps.setTimestamp(7, java.sql.Timestamp.from(now))
                ps.setTimestamp(8, java.sql.Timestamp.from(now))
                ps.executeUpdate()
            }
        }
        DbInstance(
            id = id,
            projectId = projectId,
            name = name,
            status = status,
            engine = engine,
            deletionProtection = deletionProtection,
            createdAt = now,
            updatedAt = now,
        )
    }

    override fun findInstanceById(id: UUID): DbInstance? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, project_id, name, status, engine, deletion_protection,
                       status_reason, endpoint_ref, created_at, updated_at
                FROM db_instance WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapInstance(rs) else null
                }
            }
        }
    }

    override fun listInstances(projectId: UUID): List<DbInstance> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, project_id, name, status, engine, deletion_protection,
                       status_reason, endpoint_ref, created_at, updated_at
                FROM db_instance WHERE project_id = ? ORDER BY created_at
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, projectId)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(mapInstance(rs))
                    }
                }
            }
        }
    }

    override fun updateInstanceStatus(
        id: UUID,
        status: DbInstanceStatus,
        statusReason: String?,
        endpointRef: String?,
    ): DbInstance = runSql {
        val existing = findInstanceById(id)
            ?: throw RepositoryException.NotFound("db_instance", id)
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE db_instance
                SET status = ?, status_reason = ?, endpoint_ref = COALESCE(?, endpoint_ref),
                    updated_at = ?
                WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, status.wire)
                ps.setString(2, statusReason)
                ps.setString(3, endpointRef)
                ps.setTimestamp(4, java.sql.Timestamp.from(now))
                ps.setObject(5, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("db_instance", id)
                }
            }
        }
        existing.copy(
            status = status,
            statusReason = statusReason,
            endpointRef = endpointRef ?: existing.endpointRef,
            updatedAt = now,
        )
    }

    override fun listDatabases(instanceId: UUID): List<DbDatabase> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, instance_id, name, created_at
                FROM db_database WHERE instance_id = ? ORDER BY created_at
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, instanceId)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(mapDatabase(rs))
                    }
                }
            }
        }
    }

    override fun createDatabase(instanceId: UUID, name: String): DbDatabase = runSql {
        val id = UUID.randomUUID()
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO db_database (id, instance_id, name, created_at)
                VALUES (?, ?, ?, ?)
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.setObject(2, instanceId)
                ps.setString(3, name)
                ps.setTimestamp(4, java.sql.Timestamp.from(now))
                ps.executeUpdate()
            }
        }
        DbDatabase(id = id, instanceId = instanceId, name = name, createdAt = now)
    }

    private fun mapInstance(rs: java.sql.ResultSet): DbInstance =
        DbInstance(
            id = rs.uuid("id"),
            projectId = rs.uuid("project_id"),
            name = rs.getString("name"),
            status = DbInstanceStatus.parse(rs.getString("status")),
            engine = rs.getString("engine"),
            deletionProtection = rs.getBoolean("deletion_protection"),
            statusReason = rs.getString("status_reason"),
            endpointRef = rs.getString("endpoint_ref"),
            createdAt = rs.instant("created_at"),
            updatedAt = rs.instant("updated_at"),
        )

    private fun mapDatabase(rs: java.sql.ResultSet): DbDatabase =
        DbDatabase(
            id = rs.uuid("id"),
            instanceId = rs.uuid("instance_id"),
            name = rs.getString("name"),
            createdAt = rs.instant("created_at"),
        )
}
