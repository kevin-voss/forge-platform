package forge.control.repo

import forge.control.domain.Application
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource

interface ApplicationRepository {
    fun create(projectId: UUID, name: String): Application
    fun findById(id: UUID): Application?
    fun findByProjectAndName(projectId: UUID, name: String): Application?
    fun list(projectId: UUID): List<Application>
    fun update(id: UUID, name: String): Application
    fun delete(id: UUID)
}

class JdbcApplicationRepository(
    private val dataSource: DataSource,
) : ApplicationRepository {
    override fun create(projectId: UUID, name: String): Application = runSql {
        val id = UUID.randomUUID()
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO applications (id, project_id, name, created_at, updated_at)
                VALUES (?, ?, ?, ?, ?)
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.setObject(2, projectId)
                ps.setString(3, name)
                ps.setTimestamp(4, java.sql.Timestamp.from(now))
                ps.setTimestamp(5, java.sql.Timestamp.from(now))
                ps.executeUpdate()
            }
        }
        Application(id, projectId, name, now, now)
    }

    override fun findById(id: UUID): Application? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, project_id, name, created_at, updated_at
                FROM applications WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapRow(rs) else null
                }
            }
        }
    }

    override fun findByProjectAndName(projectId: UUID, name: String): Application? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, project_id, name, created_at, updated_at
                FROM applications WHERE project_id = ? AND name = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, projectId)
                ps.setString(2, name)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapRow(rs) else null
                }
            }
        }
    }

    override fun list(projectId: UUID): List<Application> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, project_id, name, created_at, updated_at
                FROM applications WHERE project_id = ? ORDER BY created_at
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, projectId)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(mapRow(rs))
                    }
                }
            }
        }
    }

    override fun update(id: UUID, name: String): Application = runSql {
        val existing = findById(id) ?: throw RepositoryException.NotFound("application", id)
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                "UPDATE applications SET name = ?, updated_at = ? WHERE id = ?",
            ).use { ps ->
                ps.setString(1, name)
                ps.setTimestamp(2, java.sql.Timestamp.from(now))
                ps.setObject(3, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("application", id)
                }
            }
        }
        existing.copy(name = name, updatedAt = now)
    }

    override fun delete(id: UUID) = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement("DELETE FROM applications WHERE id = ?").use { ps ->
                ps.setObject(1, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("application", id)
                }
            }
        }
    }

    private fun mapRow(rs: java.sql.ResultSet): Application =
        Application(
            id = rs.uuid("id"),
            projectId = rs.uuid("project_id"),
            name = rs.getString("name"),
            createdAt = rs.instant("created_at"),
            updatedAt = rs.instant("updated_at"),
        )
}
