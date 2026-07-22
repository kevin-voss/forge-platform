package forge.identity.project

import forge.identity.db.StoreException
import forge.identity.db.runSql
import forge.identity.db.withConnection
import forge.identity.http.ApiException
import forge.identity.logging.JsonLog
import forge.identity.org.OrgStore
import forge.identity.user.UserStore
import javax.sql.DataSource

data class IdentityProject(
    val id: String,
    val orgId: String,
    val name: String,
)

data class ProjectMembership(
    val projectId: String,
    val userId: String,
    val role: String,
)

class ProjectMembershipStore(
    private val dataSource: DataSource,
    private val users: UserStore,
    private val orgs: OrgStore,
    private val log: JsonLog? = null,
) {
    fun createProject(id: String, orgId: String, name: String): IdentityProject {
        val projectId = id.trim()
        val normalizedName = name.trim()
        if (projectId.isEmpty()) {
            throw ApiException.BadRequest("id must not be blank", mapOf("field" to "id"))
        }
        if (normalizedName.isEmpty()) {
            throw ApiException.BadRequest("name must not be blank", mapOf("field" to "name"))
        }
        if (!orgs.exists(orgId)) {
            throw ApiException.NotFound("organization not found", mapOf("org_id" to orgId))
        }

        val existing = findProject(projectId)
        if (existing != null) {
            if (existing.orgId != orgId || existing.name != normalizedName) {
                throw ApiException.Conflict(
                    "project id already registered",
                    mapOf("id" to projectId),
                )
            }
            return existing
        }

        return try {
            runSql {
                dataSource.withConnection { conn ->
                    conn.prepareStatement(
                        """
                        INSERT INTO projects (id, org_id, name)
                        VALUES (?, ?, ?)
                        """.trimIndent(),
                    ).use { ps ->
                        ps.setString(1, projectId)
                        ps.setString(2, orgId)
                        ps.setString(3, normalizedName)
                        ps.executeUpdate()
                    }
                }
            }
            log?.info("project registered", "project_id" to projectId, "org_id" to orgId)
            IdentityProject(projectId, orgId, normalizedName)
        } catch (e: StoreException.Conflict) {
            findProject(projectId)
                ?: throw ApiException.Conflict("project id already registered", mapOf("id" to projectId))
        } catch (e: StoreException.ConstraintViolation) {
            throw ApiException.NotFound("organization not found", mapOf("org_id" to orgId))
        }
    }

    fun getProject(id: String): IdentityProject =
        findProject(id) ?: throw ApiException.NotFound("project not found", mapOf("id" to id))

    fun findProject(id: String): IdentityProject? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT id, org_id, name FROM projects WHERE id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, id)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) {
                        null
                    } else {
                        IdentityProject(
                            id = rs.getString("id"),
                            orgId = rs.getString("org_id"),
                            name = rs.getString("name"),
                        )
                    }
                }
            }
        }
    }

    fun listProjects(orgId: String? = null): List<IdentityProject> = runSql {
        dataSource.withConnection { conn ->
            val sql = if (orgId != null) {
                """
                SELECT id, org_id, name FROM projects WHERE org_id = ? ORDER BY name, id
                """.trimIndent()
            } else {
                """
                SELECT id, org_id, name FROM projects ORDER BY name, id
                """.trimIndent()
            }
            conn.prepareStatement(sql).use { ps ->
                if (orgId != null) ps.setString(1, orgId)
                ps.executeQuery().use { rs ->
                    buildList {
                        while (rs.next()) {
                            add(
                                IdentityProject(
                                    id = rs.getString("id"),
                                    orgId = rs.getString("org_id"),
                                    name = rs.getString("name"),
                                ),
                            )
                        }
                    }
                }
            }
        }
    }

    fun addMember(projectId: String, userId: String, role: String): ProjectMembership {
        val normalizedRole = role.trim()
        if (normalizedRole.isEmpty()) {
            throw ApiException.BadRequest("role must not be blank", mapOf("field" to "role"))
        }
        getProject(projectId)
        if (!users.exists(userId)) {
            throw ApiException.NotFound("user not found", mapOf("user_id" to userId))
        }

        val existing = findMembership(projectId, userId)
        if (existing != null) {
            return existing
        }

        return try {
            runSql {
                dataSource.withConnection { conn ->
                    conn.prepareStatement(
                        """
                        INSERT INTO project_memberships (project_id, user_id, role)
                        VALUES (?, ?, ?)
                        """.trimIndent(),
                    ).use { ps ->
                        ps.setString(1, projectId)
                        ps.setString(2, userId)
                        ps.setString(3, normalizedRole)
                        ps.executeUpdate()
                    }
                }
            }
            log?.info(
                "project membership created",
                "project_id" to projectId,
                "user_id" to userId,
                "role" to normalizedRole,
            )
            ProjectMembership(projectId, userId, normalizedRole)
        } catch (e: StoreException.Conflict) {
            findMembership(projectId, userId)
                ?: throw ApiException.Conflict(
                    "project membership conflict",
                    mapOf("project_id" to projectId),
                )
        } catch (e: StoreException.ConstraintViolation) {
            throw ApiException.NotFound(
                "user or project not found",
                mapOf("project_id" to projectId, "user_id" to userId),
            )
        }
    }

    fun removeMember(projectId: String, userId: String) {
        getProject(projectId)
        val deleted = runSql {
            dataSource.withConnection { conn ->
                conn.prepareStatement(
                    """
                    DELETE FROM project_memberships WHERE project_id = ? AND user_id = ?
                    """.trimIndent(),
                ).use { ps ->
                    ps.setString(1, projectId)
                    ps.setString(2, userId)
                    ps.executeUpdate()
                }
            }
        }
        if (deleted == 0) {
            throw ApiException.NotFound(
                "project membership not found",
                mapOf("project_id" to projectId, "user_id" to userId),
            )
        }
        log?.info("project membership deleted", "project_id" to projectId, "user_id" to userId)
    }

    fun findMembership(projectId: String, userId: String): ProjectMembership? = runSql {
        dataSource.withConnection { conn ->
            conn.prepareStatement(
                """
                SELECT project_id, user_id, role FROM project_memberships
                WHERE project_id = ? AND user_id = ?
                """.trimIndent(),
            ).use { ps ->
                ps.setString(1, projectId)
                ps.setString(2, userId)
                ps.executeQuery().use { rs ->
                    if (!rs.next()) {
                        null
                    } else {
                        ProjectMembership(
                            projectId = rs.getString("project_id"),
                            userId = rs.getString("user_id"),
                            role = rs.getString("role"),
                        )
                    }
                }
            }
        }
    }
}
