package forge.control.repo

import forge.control.domain.Service
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource

interface ServiceRepository {
    fun create(applicationId: UUID, name: String, port: Int): Service
    fun findById(id: UUID): Service?
    fun list(applicationId: UUID): List<Service>
    fun update(id: UUID, name: String?, port: Int?): Service
    fun delete(id: UUID)
}

class JdbcServiceRepository(
    private val dataSource: DataSource,
) : ServiceRepository {
    override fun create(applicationId: UUID, name: String, port: Int): Service = runSql {
        val id = UUID.randomUUID()
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO services (id, application_id, name, port, created_at, updated_at)
                VALUES (?, ?, ?, ?, ?, ?)
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.setObject(2, applicationId)
                ps.setString(3, name)
                ps.setInt(4, port)
                ps.setTimestamp(5, java.sql.Timestamp.from(now))
                ps.setTimestamp(6, java.sql.Timestamp.from(now))
                ps.executeUpdate()
            }
        }
        Service(id, applicationId, name, port, now, now)
    }

    override fun findById(id: UUID): Service? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, application_id, name, port, created_at, updated_at
                FROM services WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapRow(rs) else null
                }
            }
        }
    }

    override fun list(applicationId: UUID): List<Service> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, application_id, name, port, created_at, updated_at
                FROM services WHERE application_id = ? ORDER BY created_at
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, applicationId)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(mapRow(rs))
                    }
                }
            }
        }
    }

    override fun update(id: UUID, name: String?, port: Int?): Service = runSql {
        val existing = findById(id) ?: throw RepositoryException.NotFound("service", id)
        val newName = name ?: existing.name
        val newPort = port ?: existing.port
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE services SET name = ?, port = ?, updated_at = ?
                WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, newName)
                ps.setInt(2, newPort)
                ps.setTimestamp(3, java.sql.Timestamp.from(now))
                ps.setObject(4, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("service", id)
                }
            }
        }
        existing.copy(name = newName, port = newPort, updatedAt = now)
    }

    override fun delete(id: UUID) = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement("DELETE FROM services WHERE id = ?").use { ps ->
                ps.setObject(1, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("service", id)
                }
            }
        }
    }

    private fun mapRow(rs: java.sql.ResultSet): Service =
        Service(
            id = rs.uuid("id"),
            applicationId = rs.uuid("application_id"),
            name = rs.getString("name"),
            port = rs.getInt("port"),
            createdAt = rs.instant("created_at"),
            updatedAt = rs.instant("updated_at"),
        )
}
