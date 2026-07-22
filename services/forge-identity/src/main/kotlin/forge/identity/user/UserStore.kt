package forge.identity.user

import forge.identity.db.StoreException
import forge.identity.db.instant
import forge.identity.db.runSql
import forge.identity.db.withConnection
import forge.identity.http.ApiException
import forge.identity.logging.JsonLog
import forge.identity.metrics.IdentityMetrics
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource

data class User(
    val id: String,
    val email: String,
    val displayName: String,
    val createdAt: Instant,
)

data class OrgMembershipView(
    val orgId: String,
    val orgName: String,
    val role: String,
)

data class ProjectMembershipView(
    val projectId: String,
    val projectName: String,
    val orgId: String,
    val role: String,
)

data class UserMemberships(
    val orgs: List<OrgMembershipView>,
    val projects: List<ProjectMembershipView>,
)

class UserStore(
    private val dataSource: DataSource,
    private val log: JsonLog? = null,
) {
    fun create(email: String, displayName: String): User {
        val normalizedEmail = email.trim()
        val normalizedName = displayName.trim()
        if (normalizedEmail.isEmpty()) {
            throw ApiException.BadRequest("email must not be blank", mapOf("field" to "email"))
        }
        if (!normalizedEmail.contains('@')) {
            throw ApiException.BadRequest("email must be a valid address", mapOf("field" to "email"))
        }
        if (normalizedName.isEmpty()) {
            throw ApiException.BadRequest(
                "display_name must not be blank",
                mapOf("field" to "display_name"),
            )
        }

        val id = UUID.randomUUID().toString()
        val now = Instant.now()
        return try {
            runSql {
                dataSource.withConnection { conn ->
                    conn.prepareStatement(
                        """
                        INSERT INTO users (id, email, display_name, created_at)
                        VALUES (?, ?, ?, ?)
                        """.trimIndent(),
                    ).use { ps ->
                        ps.setString(1, id)
                        ps.setString(2, normalizedEmail)
                        ps.setString(3, normalizedName)
                        ps.setTimestamp(4, java.sql.Timestamp.from(now))
                        ps.executeUpdate()
                    }
                }
            }
            IdentityMetrics.recordUserCreated()
            log?.info("user created", "user_id" to id, "email_domain" to emailDomain(normalizedEmail))
            User(id, normalizedEmail, normalizedName, now)
        } catch (_: StoreException.Conflict) {
            throw ApiException.Conflict(
                "email already registered",
                mapOf("email" to normalizedEmail),
            )
        }
    }

    fun get(id: String): User =
        findById(id) ?: throw ApiException.NotFound("user not found", mapOf("id" to id))

    fun findById(id: String): User? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, email::text AS email, display_name, created_at
                FROM users WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) null else rs.toUser()
                }
            }
        }
    }

    fun findByEmail(email: String): User? {
        val normalized = email.trim()
        if (normalized.isEmpty()) return null
        return runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    SELECT id, email::text AS email, display_name, created_at
                    FROM users WHERE email = ?
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, normalized)
                    ps.executeQuery().use { rs ->
                        if (!rs.next()) null else rs.toUser()
                    }
                }
            }
        }
    }

    fun list(): List<User> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, email::text AS email, display_name, created_at
                FROM users ORDER BY created_at, id
                """.trimIndent(),
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) add(rs.toUser())
                    }
                }
            }
        }
    }

    fun memberships(userId: String): UserMemberships {
        get(userId) // 404 if missing
        val orgs = runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    SELECT o.id AS org_id, o.name AS org_name, m.role
                    FROM org_memberships m
                    JOIN organizations o ON o.id = m.org_id
                    WHERE m.user_id = ?
                    ORDER BY o.name, o.id
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, userId)
                    ps.executeQuery().use { rs ->
                        buildList {
                            while (rs.next()) {
                                add(
                                    OrgMembershipView(
                                        orgId = rs.getString("org_id"),
                                        orgName = rs.getString("org_name"),
                                        role = rs.getString("role"),
                                    ),
                                )
                            }
                        }
                    }
                }
            }
        }
        val projects = runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    SELECT p.id AS project_id, p.name AS project_name, p.org_id, m.role
                    FROM project_memberships m
                    JOIN projects p ON p.id = m.project_id
                    WHERE m.user_id = ?
                    ORDER BY p.name, p.id
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, userId)
                    ps.executeQuery().use { rs ->
                        buildList {
                            while (rs.next()) {
                                add(
                                    ProjectMembershipView(
                                        projectId = rs.getString("project_id"),
                                        projectName = rs.getString("project_name"),
                                        orgId = rs.getString("org_id"),
                                        role = rs.getString("role"),
                                    ),
                                )
                            }
                        }
                    }
                }
            }
        }
        return UserMemberships(orgs = orgs, projects = projects)
    }

    fun exists(id: String): Boolean = findById(id) != null

    private fun java.sql.ResultSet.toUser(): User =
        User(
            id = getString("id"),
            email = getString("email"),
            displayName = getString("display_name"),
            createdAt = instant("created_at"),
        )

    private fun emailDomain(email: String): String =
        email.substringAfter('@', missingDelimiterValue = "").ifEmpty { "unknown" }
}
