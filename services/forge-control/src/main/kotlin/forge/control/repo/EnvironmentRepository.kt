package forge.control.repo

import forge.control.domain.Environment
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource

interface EnvironmentRepository {
    fun create(projectId: UUID, name: String): Environment
    fun findById(id: UUID): Environment?
    fun list(projectId: UUID): List<Environment>
    fun update(id: UUID, name: String): Environment
    fun delete(id: UUID)
}

class JdbcEnvironmentRepository(
    private val dataSource: DataSource,
) : EnvironmentRepository {
    override fun create(projectId: UUID, name: String): Environment = runSql {
        val id = UUID.randomUUID()
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO environments (id, project_id, name, created_at, updated_at)
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
        Environment(id, projectId, name, now, now)
    }

    override fun findById(id: UUID): Environment? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, project_id, name, created_at, updated_at
                FROM environments WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapRow(rs) else null
                }
            }
        }
    }

    override fun list(projectId: UUID): List<Environment> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, project_id, name, created_at, updated_at
                FROM environments WHERE project_id = ? ORDER BY created_at
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

    override fun update(id: UUID, name: String): Environment = runSql {
        val existing = findById(id) ?: throw RepositoryException.NotFound("environment", id)
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                "UPDATE environments SET name = ?, updated_at = ? WHERE id = ?",
            ).use { ps ->
                ps.setString(1, name)
                ps.setTimestamp(2, java.sql.Timestamp.from(now))
                ps.setObject(3, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("environment", id)
                }
            }
        }
        existing.copy(name = name, updatedAt = now)
    }

    override fun delete(id: UUID) = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement("DELETE FROM environments WHERE id = ?").use { ps ->
                ps.setObject(1, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("environment", id)
                }
            }
        }
    }

    private fun mapRow(rs: java.sql.ResultSet): Environment =
        Environment(
            id = rs.uuid("id"),
            projectId = rs.uuid("project_id"),
            name = rs.getString("name"),
            createdAt = rs.instant("created_at"),
            updatedAt = rs.instant("updated_at"),
        )
}
