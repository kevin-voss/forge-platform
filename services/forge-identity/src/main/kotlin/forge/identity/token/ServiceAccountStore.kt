package forge.identity.token

import forge.identity.authz.Role
import forge.identity.db.StoreException
import forge.identity.db.instant
import forge.identity.db.runSql
import forge.identity.db.withConnection
import forge.identity.http.ApiException
import forge.identity.logging.JsonLog
import forge.identity.project.ProjectMembershipStore
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource

data class ServiceAccount(
    val id: String,
    val projectId: String,
    val name: String,
    val role: String,
    val createdAt: Instant,
)

class ServiceAccountStore(
    private val dataSource: DataSource,
    private val projects: ProjectMembershipStore,
    private val log: JsonLog? = null,
) {
    fun create(projectId: String, name: String, role: String, now: Instant = Instant.now()): ServiceAccount {
        val pid = projectId.trim()
        val normalizedName = name.trim()
        val normalizedRole = role.trim()
        if (pid.isEmpty()) {
            throw ApiException.BadRequest("project_id must not be blank", mapOf("field" to "project_id"))
        }
        if (normalizedName.isEmpty()) {
            throw ApiException.BadRequest("name must not be blank", mapOf("field" to "name"))
        }
        if (normalizedRole.isEmpty()) {
            throw ApiException.BadRequest("role must not be blank", mapOf("field" to "role"))
        }
        val parsedRole = Role.fromWire(normalizedRole)
            ?: throw ApiException.BadRequest(
                "role must be a known membership role",
                mapOf("field" to "role", "role" to normalizedRole),
            )
        if (parsedRole !in Role.membershipRoles) {
            throw ApiException.BadRequest(
                "role must be a known membership role",
                mapOf("field" to "role", "role" to normalizedRole),
            )
        }
        projects.getProject(pid)

        val id = UUID.randomUUID().toString()
        return try {
            runSql {
                dataSource.withConnection { conn ->
                    conn.prepareStatement(
                        """
                        INSERT INTO service_accounts (id, project_id, name, role, created_at)
                        VALUES (?, ?, ?, ?, ?)
                        """.trimIndent(),
                    ).use { ps ->
                        ps.setString(1, id)
                        ps.setString(2, pid)
                        ps.setString(3, normalizedName)
                        ps.setString(4, normalizedRole)
                        ps.setTimestamp(5, Timestamp.from(now))
                        ps.executeUpdate()
                    }
                }
            }
            log?.info(
                "service account created",
                "service_account_id" to id,
                "project_id" to pid,
                "name" to normalizedName,
                "role" to normalizedRole,
            )
            ServiceAccount(id, pid, normalizedName, normalizedRole, now)
        } catch (_: StoreException.Conflict) {
            throw ApiException.Conflict(
                "service account name already exists in project",
                mapOf("project_id" to pid, "name" to normalizedName),
            )
        } catch (_: StoreException.ConstraintViolation) {
            throw ApiException.NotFound("project not found", mapOf("project_id" to pid))
        }
    }

    fun findById(id: String): ServiceAccount? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, project_id, name, role, created_at
                FROM service_accounts WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) null else rs.toServiceAccount()
                }
            }
        }
    }

    fun get(id: String): ServiceAccount =
        findById(id) ?: throw ApiException.NotFound(
            "service account not found",
            mapOf("id" to id),
        )

    private fun java.sql.ResultSet.toServiceAccount(): ServiceAccount =
        ServiceAccount(
            id = getString("id"),
            projectId = getString("project_id"),
            name = getString("name"),
            role = getString("role"),
            createdAt = instant("created_at"),
        )
}
