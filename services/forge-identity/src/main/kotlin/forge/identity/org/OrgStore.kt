package forge.identity.org

import forge.identity.db.StoreException
import forge.identity.db.instant
import forge.identity.db.runSql
import forge.identity.db.withConnection
import forge.identity.http.ApiException
import forge.identity.logging.JsonLog
import forge.identity.metrics.IdentityMetrics
import forge.identity.user.UserStore
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource

data class Organization(
    val id: String,
    val name: String,
    val createdAt: Instant,
)

data class OrgMembership(
    val orgId: String,
    val userId: String,
    val role: String,
)

class OrgStore(
    private val dataSource: DataSource,
    private val users: UserStore,
    private val log: JsonLog? = null,
) {
    fun create(name: String): Organization {
        val normalized = name.trim()
        if (normalized.isEmpty()) {
            throw ApiException.BadRequest("name must not be blank", mapOf("field" to "name"))
        }
        val id = UUID.randomUUID().toString()
        val now = Instant.now()
        runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    INSERT INTO organizations (id, name, created_at)
                    VALUES (?, ?, ?)
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, id)
                    ps.setString(2, normalized)
                    ps.setTimestamp(3, java.sql.Timestamp.from(now))
                    ps.executeUpdate()
                }
            }
        }
        IdentityMetrics.recordOrgCreated()
        log?.info("org created", "org_id" to id)
        return Organization(id, normalized, now)
    }

    fun get(id: String): Organization =
        findById(id) ?: throw ApiException.NotFound("organization not found", mapOf("id" to id))

    fun findById(id: String): Organization? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, name, created_at FROM organizations WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) {
                        null
                    } else {
                        Organization(
                            id = rs.getString("id"),
                            name = rs.getString("name"),
                            createdAt = rs.instant("created_at"),
                        )
                    }
                }
            }
        }
    }

    fun list(): List<Organization> = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, name, created_at FROM organizations ORDER BY created_at, id
                """.trimIndent(),
            ).use { ps ->
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) {
                            add(
                                Organization(
                                    id = rs.getString("id"),
                                    name = rs.getString("name"),
                                    createdAt = rs.instant("created_at"),
                                ),
                            )
                        }
                    }
                }
            }
        }
    }

    fun addMember(orgId: String, userId: String, role: String): OrgMembership {
        val normalizedRole = role.trim()
        if (normalizedRole.isEmpty()) {
            throw ApiException.BadRequest("role must not be blank", mapOf("field" to "role"))
        }
        get(orgId)
        if (!users.exists(userId)) {
            throw ApiException.NotFound("user not found", mapOf("user_id" to userId))
        }

        val existing = findMembership(orgId, userId)
        if (existing != null) {
            return existing
        }

        return try {
            runSql {
                dataSource.withConnection { conn ->
                    conn.prepareStatement(
                        """
                        INSERT INTO org_memberships (org_id, user_id, role)
                        VALUES (?, ?, ?)
                        """.trimIndent(),
                    ).use { ps ->
                        ps.setString(1, orgId)
                        ps.setString(2, userId)
                        ps.setString(3, normalizedRole)
                        ps.executeUpdate()
                    }
                }
            }
            log?.info(
                "org membership created",
                "org_id" to orgId,
                "user_id" to userId,
                "role" to normalizedRole,
            )
            OrgMembership(orgId, userId, normalizedRole)
        } catch (e: StoreException.Conflict) {
            findMembership(orgId, userId)
                ?: throw ApiException.Conflict("org membership conflict", mapOf("org_id" to orgId))
        } catch (e: StoreException.ConstraintViolation) {
            throw ApiException.NotFound(
                "user or organization not found",
                mapOf("org_id" to orgId, "user_id" to userId),
            )
        }
    }

    fun removeMember(orgId: String, userId: String) {
        get(orgId)
        val deleted = runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    DELETE FROM org_memberships WHERE org_id = ? AND user_id = ?
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, orgId)
                    ps.setString(2, userId)
                    ps.executeUpdate()
                }
            }
        }
        if (deleted == 0) {
            throw ApiException.NotFound(
                "org membership not found",
                mapOf("org_id" to orgId, "user_id" to userId),
            )
        }
        log?.info("org membership deleted", "org_id" to orgId, "user_id" to userId)
    }

    fun listMembers(orgId: String): List<OrgMembership> {
        get(orgId)
        return runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    SELECT org_id, user_id, role FROM org_memberships
                    WHERE org_id = ? ORDER BY user_id
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, orgId)
                    ps.executeQuery().use { rs ->
                        buildList {
                            while (rs.next()) {
                                add(
                                    OrgMembership(
                                        orgId = rs.getString("org_id"),
                                        userId = rs.getString("user_id"),
                                        role = rs.getString("role"),
                                    ),
                                )
                            }
                        }
                    }
                }
            }
        }
    }

    fun findMembership(orgId: String, userId: String): OrgMembership? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT org_id, user_id, role FROM org_memberships
                WHERE org_id = ? AND user_id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, orgId)
                ps.setString(2, userId)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) {
                        null
                    } else {
                        OrgMembership(
                            orgId = rs.getString("org_id"),
                            userId = rs.getString("user_id"),
                            role = rs.getString("role"),
                        )
                    }
                }
            }
        }
    }

    fun exists(id: String): Boolean = findById(id) != null
}
