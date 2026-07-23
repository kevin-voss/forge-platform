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
        host: String? = null,
        port: Int? = null,
        containerId: String? = null,
    ): DbInstance

    fun listDatabases(instanceId: UUID): List<DbDatabase>
    fun findDatabaseById(id: UUID): DbDatabase?
    fun createDatabase(
        instanceId: UUID,
        name: String,
        status: DbDatabaseStatus = DbDatabaseStatus.Provisioning,
    ): DbDatabase

    fun updateDatabaseStatus(
        id: UUID,
        status: DbDatabaseStatus,
        statusReason: String? = null,
    ): DbDatabase

    fun createCredential(
        databaseId: UUID,
        username: String,
        secretRef: String?,
        status: String = "active",
    ): DbCredential

    fun findActiveCredential(databaseId: UUID): DbCredential?

    fun createAttachment(
        databaseId: UUID,
        applicationId: UUID,
        envVar: String,
        secretRef: String?,
        id: UUID = UUID.randomUUID(),
    ): DbAttachment

    fun findAttachmentById(id: UUID): DbAttachment?
    fun listAttachmentsByApplication(applicationId: UUID): List<DbAttachment>
    fun deleteAttachment(id: UUID)

    fun deleteDatabase(id: UUID)
    fun deleteCredential(id: UUID)
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
                       status_reason, endpoint_ref, host, port, container_id,
                       created_at, updated_at
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
                       status_reason, endpoint_ref, host, port, container_id,
                       created_at, updated_at
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
        host: String?,
        port: Int?,
        containerId: String?,
    ): DbInstance = runSql {
        val existing = findInstanceById(id)
            ?: throw RepositoryException.NotFound("db_instance", id)
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE db_instance
                SET status = ?,
                    status_reason = ?,
                    endpoint_ref = COALESCE(?, endpoint_ref),
                    host = COALESCE(?, host),
                    port = COALESCE(?, port),
                    container_id = COALESCE(?, container_id),
                    updated_at = ?
                WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, status.wire)
                ps.setString(2, statusReason)
                ps.setString(3, endpointRef)
                ps.setString(4, host)
                if (port != null) ps.setInt(5, port) else ps.setObject(5, null)
                ps.setString(6, containerId)
                ps.setTimestamp(7, java.sql.Timestamp.from(now))
                ps.setObject(8, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("db_instance", id)
                }
            }
        }
        existing.copy(
            status = status,
            statusReason = statusReason,
            endpointRef = endpointRef ?: existing.endpointRef,
            host = host ?: existing.host,
            port = port ?: existing.port,
            containerId = containerId ?: existing.containerId,
            updatedAt = now,
        )
    }

    override fun listDatabases(instanceId: UUID): List<DbDatabase> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, instance_id, name, status, status_reason, created_at
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

    override fun findDatabaseById(id: UUID): DbDatabase? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, instance_id, name, status, status_reason, created_at
                FROM db_database WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapDatabase(rs) else null
                }
            }
        }
    }

    override fun createDatabase(
        instanceId: UUID,
        name: String,
        status: DbDatabaseStatus,
    ): DbDatabase = runSql {
        val id = UUID.randomUUID()
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO db_database (id, instance_id, name, status, created_at)
                VALUES (?, ?, ?, ?, ?)
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.setObject(2, instanceId)
                ps.setString(3, name)
                ps.setString(4, status.wire)
                ps.setTimestamp(5, java.sql.Timestamp.from(now))
                ps.executeUpdate()
            }
        }
        DbDatabase(
            id = id,
            instanceId = instanceId,
            name = name,
            status = status,
            createdAt = now,
        )
    }

    override fun updateDatabaseStatus(
        id: UUID,
        status: DbDatabaseStatus,
        statusReason: String?,
    ): DbDatabase = runSql {
        val existing = findDatabaseById(id)
            ?: throw RepositoryException.NotFound("db_database", id)
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE db_database SET status = ?, status_reason = ? WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, status.wire)
                ps.setString(2, statusReason)
                ps.setObject(3, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("db_database", id)
                }
            }
        }
        existing.copy(status = status, statusReason = statusReason)
    }

    override fun createCredential(
        databaseId: UUID,
        username: String,
        secretRef: String?,
        status: String,
    ): DbCredential = runSql {
        val id = UUID.randomUUID()
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO db_credential (id, database_id, username, secret_ref, status, created_at)
                VALUES (?, ?, ?, ?, ?, ?)
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.setObject(2, databaseId)
                ps.setString(3, username)
                ps.setString(4, secretRef)
                ps.setString(5, status)
                ps.setTimestamp(6, java.sql.Timestamp.from(now))
                ps.executeUpdate()
            }
        }
        DbCredential(
            id = id,
            databaseId = databaseId,
            username = username,
            secretRef = secretRef,
            status = status,
            createdAt = now,
        )
    }

    override fun findActiveCredential(databaseId: UUID): DbCredential? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, database_id, username, secret_ref, status, created_at
                FROM db_credential
                WHERE database_id = ? AND status = 'active'
                ORDER BY created_at DESC
                LIMIT 1
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, databaseId)
                ps.executeQuery().use { rs ->
                    if (rs.next()) {
                        DbCredential(
                            id = rs.uuid("id"),
                            databaseId = rs.uuid("database_id"),
                            username = rs.getString("username"),
                            secretRef = rs.getString("secret_ref"),
                            status = rs.getString("status"),
                            createdAt = rs.instant("created_at"),
                        )
                    } else {
                        null
                    }
                }
            }
        }
    }

    override fun createAttachment(
        databaseId: UUID,
        applicationId: UUID,
        envVar: String,
        secretRef: String?,
        id: UUID,
    ): DbAttachment = runSql {
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO db_attachment (id, database_id, application_id, env_var, secret_ref, created_at)
                VALUES (?, ?, ?, ?, ?, ?)
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.setObject(2, databaseId)
                ps.setObject(3, applicationId)
                ps.setString(4, envVar)
                ps.setString(5, secretRef)
                ps.setTimestamp(6, java.sql.Timestamp.from(now))
                ps.executeUpdate()
            }
        }
        DbAttachment(
            id = id,
            databaseId = databaseId,
            applicationId = applicationId,
            envVar = envVar,
            secretRef = secretRef,
            createdAt = now,
        )
    }

    override fun findAttachmentById(id: UUID): DbAttachment? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, database_id, application_id, env_var, secret_ref, created_at
                FROM db_attachment WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapAttachment(rs) else null
                }
            }
        }
    }

    override fun listAttachmentsByApplication(applicationId: UUID): List<DbAttachment> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, database_id, application_id, env_var, secret_ref, created_at
                FROM db_attachment WHERE application_id = ? ORDER BY created_at
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, applicationId)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(mapAttachment(rs))
                    }
                }
            }
        }
    }

    override fun deleteAttachment(id: UUID) {
        runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement("DELETE FROM db_attachment WHERE id = ?").use { ps ->
                    ps.setObject(1, id)
                    if (ps.executeUpdate() == 0) {
                        throw RepositoryException.NotFound("db_attachment", id)
                    }
                }
            }
        }
    }

    override fun deleteDatabase(id: UUID) {
        runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement("DELETE FROM db_database WHERE id = ?").use { ps ->
                    ps.setObject(1, id)
                    ps.executeUpdate()
                }
            }
        }
    }

    override fun deleteCredential(id: UUID) {
        runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement("DELETE FROM db_credential WHERE id = ?").use { ps ->
                    ps.setObject(1, id)
                    ps.executeUpdate()
                }
            }
        }
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
            host = rs.getString("host"),
            port = rs.getObject("port")?.let { (it as Number).toInt() },
            containerId = rs.getString("container_id"),
            createdAt = rs.instant("created_at"),
            updatedAt = rs.instant("updated_at"),
        )

    private fun mapDatabase(rs: java.sql.ResultSet): DbDatabase =
        DbDatabase(
            id = rs.uuid("id"),
            instanceId = rs.uuid("instance_id"),
            name = rs.getString("name"),
            status = DbDatabaseStatus.parse(rs.getString("status")),
            statusReason = rs.getString("status_reason"),
            createdAt = rs.instant("created_at"),
        )

    private fun mapAttachment(rs: java.sql.ResultSet): DbAttachment =
        DbAttachment(
            id = rs.uuid("id"),
            databaseId = rs.uuid("database_id"),
            applicationId = rs.uuid("application_id"),
            envVar = rs.getString("env_var"),
            secretRef = rs.getString("secret_ref"),
            createdAt = rs.instant("created_at"),
        )
}
