package forge.control.repo

import forge.control.domain.Project
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource

interface ProjectRepository {
    fun create(name: String, slug: String): Project
    fun findById(id: UUID): Project?
    fun list(): List<Project>
    fun update(id: UUID, name: String?, slug: String?): Project
    fun delete(id: UUID)
}

class JdbcProjectRepository(
    private val dataSource: DataSource,
) : ProjectRepository {
    override fun create(name: String, slug: String): Project = runSql {
        val id = UUID.randomUUID()
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                INSERT INTO projects (id, name, slug, created_at, updated_at)
                VALUES (?, ?, ?, ?, ?)
                """.trimIndent(),
            ).use { ps ->
                ps.setObject(1, id)
                ps.setString(2, name)
                ps.setString(3, slug)
                ps.setTimestamp(4, java.sql.Timestamp.from(now))
                ps.setTimestamp(5, java.sql.Timestamp.from(now))
                ps.executeUpdate()
            }
        }
        Project(id, name, slug, now, now)
    }

    override fun findById(id: UUID): Project? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                "SELECT id, name, slug, created_at, updated_at FROM projects WHERE id = ?",
            ).use { ps ->
                ps.setObject(1, id)
                ps.executeQuery().use { rs ->
                    if (rs.next()) mapRow(rs) else null
                }
            }
        }
    }

    override fun list(): List<Project> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                "SELECT id, name, slug, created_at, updated_at FROM projects ORDER BY created_at",
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(mapRow(rs))
                    }
                }
            }
        }
    }

    override fun update(id: UUID, name: String?, slug: String?): Project = runSql {
        val existing = findById(id) ?: throw RepositoryException.NotFound("project", id)
        val newName = name ?: existing.name
        val newSlug = slug ?: existing.slug
        val now = Instant.now()
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                UPDATE projects SET name = ?, slug = ?, updated_at = ?
                WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, newName)
                ps.setString(2, newSlug)
                ps.setTimestamp(3, java.sql.Timestamp.from(now))
                ps.setObject(4, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("project", id)
                }
            }
        }
        existing.copy(name = newName, slug = newSlug, updatedAt = now)
    }

    override fun delete(id: UUID) = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement("DELETE FROM projects WHERE id = ?").use { ps ->
                ps.setObject(1, id)
                if (ps.executeUpdate() == 0) {
                    throw RepositoryException.NotFound("project", id)
                }
            }
        }
    }

    private fun mapRow(rs: java.sql.ResultSet): Project =
        Project(
            id = rs.uuid("id"),
            name = rs.getString("name"),
            slug = rs.getString("slug"),
            createdAt = rs.instant("created_at"),
            updatedAt = rs.instant("updated_at"),
        )
}
